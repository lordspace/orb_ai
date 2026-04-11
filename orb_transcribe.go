package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

var AppName = "orb_transcribe"
var AppVer = "0.1.0"
var AppBuildDate = ""
var AppGitCommit = ""

var supportedMediaExtensions = map[string]bool{
	".aac":  true,
	".aiff": true,
	".amr":  true,
	".ape":  true,
	".au":   true,
	".flac": true,
	".m4a":  true,
	".mka":  true,
	".mov":  true,
	".mp3":  true,
	".mp4":  true,
	".mpeg": true,
	".mpga": true,
	".ogg":  true,
	".oga":  true,
	".opus": true,
	".ra":   true,
	".wav":  true,
	".webm": true,
	".wma":  true,
}

// cliConfig keeps the full app configuration in one place.
type cliConfig struct {
	Files        fileRefs
	Provider     string
	Backend      backendConfig
	Language     string
	OutputFormat string
	Model        string
	SystemPrompt string
	OpenAiApiKey string
	Workers      int
	Progress     bool
	Debug        bool
}

// resultRec is the top-level JSON response shape printed by the app.
type resultRec struct {
	Status bool           `json:"status"`
	Msg    string         `json:"msg"`
	Data   map[string]any `json:"data"`
}

// fileRefs groups reusable input and output file and directory fields.
type fileRefs struct {
	InputFile  string
	InputDir   string
	OutputFile string
	OutputDir  string
}

// backendConfig keeps local backend-specific settings together.
type backendConfig struct {
	Name      string
	Cmd       string
	FfmpegCmd string
	ModelDir  string
	ModelFile string
}

// binaryConfig describes one executable override plus its smart fallback names.
type binaryConfig struct {
	Label string
	Value string
	Names []string
}

// jobInput represents one file-processing job.
type jobInput struct {
	Index int
	Files fileRefs
}

// workerResult carries one finished job result back to the coordinator.
type workerResult struct {
	Index int
	Data  map[string]any
	Err   error
}

// transcribeProvider defines one transcription backend.
type transcribeProvider interface {
	prepare(a *app) error
	transcribe(a *app, job jobInput) (map[string]any, error)
}

// localBackend defines one local transcription implementation.
type localBackend interface {
	prepare(a *app) error
	transcribe(a *app, job jobInput) (map[string]any, error)
}

// app owns the config, job list, and shared progress behavior.
type app struct {
	config     cliConfig
	jobs       []jobInput
	provider   transcribeProvider
	progressMu sync.Mutex
}

// dirCollector walks a directory and collects candidate media files.
type dirCollector struct {
	files []string
}

// outputTargetRequest carries output target inputs for resolution.
type outputTargetRequest struct {
	Files fileRefs
}

// inputRefRequest describes one required input file or directory.
type inputRefRequest struct {
	ItemType     string
	RawValue     string
	Name         string
	AbsoluteFile string
	ResolvedFile string
}

// openAiProvider sends audio files to the OpenAI transcription API.
type openAiProvider struct{}

// localProvider routes local transcription to the selected local backend.
type localProvider struct {
	backend localBackend
}

// localCmdBackend shells out to the local transcription binary.
type localCmdBackend struct{}

// localWhisperCppBackend is the future in-process Whisper backend.
type localWhisperCppBackend struct{}

// newCliConfig builds a config with sane baseline defaults.
func newCliConfig() cliConfig {
	workers := runtime.NumCPU()

	if workers < 1 {
		workers = 1
	}

	return cliConfig{
		Provider: "local",
		Backend: backendConfig{
			Name: "cmd",
		},
		Language:     "auto",
		OutputFormat: "txt",
		Model:        "medium",
		Workers:      workers,
		Progress:     false,
		Debug:        false,
	}
}

// newInputRefRequest builds an explicit file or directory input request.
func newInputRefRequest(value string, itemType string) inputRefRequest {
	request := inputRefRequest{
		ItemType: strings.TrimSpace(itemType),
		RawValue: strings.TrimSpace(value),
	}

	if request.ItemType == "" {
		request.ItemType = "file"
	}

	request.Name = filepath.Base(request.RawValue)

	return request
}

// validate fails fast on invalid top-level configuration before work starts.
func (config cliConfig) validate() error {
	if config.Files.InputFile == "" && config.Files.InputDir == "" {
		return fmt.Errorf("missing required option: -f file or -d dir")
	}

	if config.Files.InputFile != "" && config.Files.InputDir != "" {
		return fmt.Errorf("options -f/--file and -d/--dir cannot be used together")
	}

	if config.Provider == "" {
		return fmt.Errorf("provider is required")
	}

	if config.Workers < 1 {
		return fmt.Errorf("workers must be greater than zero")
	}

	if config.Provider == "openai" && config.OpenAiApiKey == "" {
		return fmt.Errorf("api key required for openai provider")
	}

	if config.Provider == "local" && config.OutputFormat != "txt" {
		return fmt.Errorf("local provider currently supports txt output only")
	}

	return nil
}

// main runs the app and prints the final JSON result.
func main() {
	resultRecord := newBaseResult()
	exitCode := 0

	config, parseErr := parseArgs(os.Args[1:])

	if parseErr != nil {
		resultRecord.Msg = "error: " + parseErr.Error()

		writeResult(resultRecord)
		os.Exit(255)
	}

	resultRecord.Data["provider"] = config.Provider

	if config.Provider == "local" {
		resultRecord.Data["local_backend"] = config.Backend.Name
	}

	application := app{config: config}
	processData, processErr := application.run()

	if processData != nil {
		mergeMaps(resultRecord.Data, processData)
	}

	if processErr != nil {
		resultRecord.Msg = "error: " + processErr.Error()
		exitCode = 255
	} else {
		resultRecord.Status = true
	}

	writeResult(resultRecord)
	os.Exit(exitCode)
}

// newBaseResult prepares the static metadata for every response.
func newBaseResult() resultRec {
	return resultRec{
		Status: false,
		Msg:    "",
		Data: map[string]any{
			"app_name":       AppName,
			"app_ver":        AppVer,
			"app_build_date": AppBuildDate,
			"app_git_commit": AppGitCommit,
		},
	}
}

// writeResult prints the final JSON response to stdout.
func writeResult(resultRecord resultRec) {
	jsonBuffer, marshalErr := json.MarshalIndent(resultRecord, "", "    ")

	if marshalErr != nil {
		fallbackResult := resultRec{
			Status: false,
			Msg:    "error: failed to encode JSON result",
			Data:   map[string]any{},
		}

		jsonBuffer, _ = json.MarshalIndent(fallbackResult, "", "    ")
	}

	jsonText := string(jsonBuffer)
	fmt.Println(jsonText)
}

// parseArgs reads flags, env fallbacks, and prompt file content into one config.
func parseArgs(args []string) (cliConfig, error) {
	config := newCliConfig()
	normalizedArgs := normalizeCliArgs(args)
	flagSet := flag.NewFlagSet(AppName, flag.ContinueOnError)
	flagSet.SetOutput(os.Stderr)

	fileShort := flagSet.String("f", "", "Input media file")
	fileLong := flagSet.String("file", "", "Input media file")
	dirShort := flagSet.String("d", "", "Input directory")
	dirLong := flagSet.String("dir", "", "Input directory")
	outputShort := flagSet.String("o", "", "Output transcript file")
	outputLong := flagSet.String("output-file", "", "Output transcript file")
	targetLong := flagSet.String("target", "", "Output transcript file")
	targetDirLong := flagSet.String("target-dir", "", "Target output directory")
	providerShort := flagSet.String("p", "", "Provider (local, openai)")
	providerLong := flagSet.String("provider", "", "Provider (local, openai)")
	openAiApiKeyShort := flagSet.String("k", "", "OpenAI API key")
	openAiApiKeyLong := flagSet.String("openai-api-key", "", "OpenAI API key")
	apiKeyLong := flagSet.String("api-key", "", "OpenAI API key")
	languageShort := flagSet.String("l", config.Language, "Language code")
	languageLong := flagSet.String("language", "", "Language code")
	langLong := flagSet.String("lang", "", "Language code")
	formatShort := flagSet.String("F", config.OutputFormat, "Output format")
	formatLong := flagSet.String("format", "", "Output format")
	localBackendLong := flagSet.String("local-backend", "", "Local backend (cmd, whispercpp)")
	localCmdLong := flagSet.String("local-cmd", "", "Local command backend binary override")
	ffmpegCmdLong := flagSet.String("ffmpeg-cmd", "", "Audio decode binary override")
	modelDirLong := flagSet.String("model-dir", "", "Local model directory")
	modelFileLong := flagSet.String("model-file", "", "Local model file")
	whisperModelDirLong := flagSet.String("whispercpp-model-dir", "", "Alias for --model-dir")
	whisperModelFileLong := flagSet.String("whispercpp-model-file", "", "Alias for --model-file")
	modelLong := flagSet.String("model", "", "Model name")
	systemPromptLong := flagSet.String("system-prompt", "", "Prompt context for models that support it")
	promptLong := flagSet.String("prompt", "", "Prompt context for models that support it")
	systemPromptFileLong := flagSet.String("system-prompt-file", "", "Read prompt context from file")
	promptFileLong := flagSet.String("prompt-file", "", "Read prompt context from file")
	workersLong := flagSet.Int("workers", config.Workers, "Parallel worker count")
	progressLong := flagSet.Bool("progress", false, "Show progress on stderr")
	debugLong := flagSet.Bool("debug", false, "Include debug fields")

	parseErr := flagSet.Parse(normalizedArgs)

	if parseErr != nil {
		return cliConfig{}, parseErr
	}

	inputFileRaw := firstString(*fileShort, *fileLong)
	inputDirRaw := firstString(*dirShort, *dirLong)
	positionalArgs := flagSet.Args()

	if inputFileRaw == "" && inputDirRaw == "" {
		positionalInput := firstArg(positionalArgs)

		if positionalInput != "" {
			if looksLikeDir(positionalInput) {
				inputDirRaw = positionalInput
			} else {
				inputFileRaw = positionalInput
			}
		}
	}

	if inputFileRaw == "" && inputDirRaw == "" {
		return cliConfig{}, fmt.Errorf("missing required option: -f file or -d dir")
	}

	if inputFileRaw != "" && inputDirRaw != "" {
		return cliConfig{}, fmt.Errorf("options -f/--file and -d/--dir cannot be used together")
	}

	if len(positionalArgs) > 1 {
		return cliConfig{}, fmt.Errorf("unexpected extra arguments: %s", strings.Join(positionalArgs[1:], " "))
	}

	outputFileRaw := firstString(*outputShort, *outputLong, *targetLong)
	targetDirRaw := firstString(*targetDirLong)
	systemPromptRaw := firstString(*systemPromptLong, *promptLong)
	systemPromptFileRaw := firstString(*systemPromptFileLong, *promptFileLong)

	outputTargetRequest := outputTargetRequest{
		Files: fileRefs{
			InputDir:   inputDirRaw,
			OutputFile: outputFileRaw,
			OutputDir:  targetDirRaw,
		},
	}

	resolvedPaths, outputErr := normalizeOutputTargets(outputTargetRequest)

	if outputErr != nil {
		return cliConfig{}, outputErr
	}

	inputFile := ""

	if inputFileRaw != "" {
		inputFileRequest := newInputRefRequest(inputFileRaw, "file")
		inputFileInfo, resolveErr := resolveInputRef(inputFileRequest)

		if resolveErr != nil {
			return cliConfig{}, resolveErr
		}

		inputFile = inputFileInfo.ResolvedFile
	}

	inputDir := ""

	if inputDirRaw != "" {
		inputDirRequest := newInputRefRequest(inputDirRaw, "dir")
		inputDirInfo, resolveErr := resolveInputRef(inputDirRequest)

		if resolveErr != nil {
			return cliConfig{}, resolveErr
		}

		inputDir = inputDirInfo.ResolvedFile
	}

	languageValue := firstString(*languageLong, *langLong, *languageShort)

	if !hasExplicitOption(normalizedArgs, "-l", "--language", "--lang") {
		languageValue = firstString(languageValue, envString("ORB_TRANSCRIBE_LANGUAGE"))
	}

	language := normalizeLanguage(languageValue)
	outputFormatValue := firstString(*formatLong, *formatShort)

	if !hasExplicitOption(normalizedArgs, "-F", "--format") {
		outputFormatValue = firstString(outputFormatValue, envString("ORB_TRANSCRIBE_FORMAT"))
	}

	outputFormat := normalizeOutputFormat(outputFormatValue)
	modelValue := firstString(*modelLong)

	if !hasExplicitOption(normalizedArgs, "--model") {
		modelValue = firstString(modelValue, envString("ORB_TRANSCRIBE_MODEL"))
	}
	systemPrompt, promptErr := resolveSystemPrompt(systemPromptRaw, systemPromptFileRaw)

	if promptErr != nil {
		return cliConfig{}, promptErr
	}

	openAiApiKey := firstString(*openAiApiKeyShort, *openAiApiKeyLong, *apiKeyLong)

	if !hasExplicitOption(normalizedArgs, "-k", "--openai-api-key", "--api-key") {
		openAiApiKey = firstString(
			openAiApiKey,
			envString(
				"ORB_TRANSCRIBE_PROVIDER_OPENAI_API_KEY",
				"ORB_TRANSCRIBE_OPENAI_API_KEY",
				"OPENAI_API_KEY",
			),
		)
	}

	providerValue := firstString(*providerShort, *providerLong)

	if !hasExplicitOption(normalizedArgs, "-p", "--provider") {
		providerValue = firstString(providerValue, envString("ORB_TRANSCRIBE_PROVIDER"))
	}

	provider := resolveProvider(providerValue, openAiApiKey)

	if provider == "" {
		return cliConfig{}, fmt.Errorf("unsupported provider: %s", providerValue)
	}

	model := resolveModel(provider, modelValue)

	localBackendValue := firstString(*localBackendLong)

	if !hasExplicitOption(normalizedArgs, "--local-backend") {
		localBackendValue = firstString(localBackendValue, envString("ORB_TRANSCRIBE_LOCAL_BACKEND"))
	}

	localBackend := normalizeLocalBackend(localBackendValue)

	if localBackend == "" {
		return cliConfig{}, fmt.Errorf("unsupported local backend: %s", localBackendValue)
	}

	backendCmd := firstString(*localCmdLong)

	if !hasExplicitOption(normalizedArgs, "--local-cmd") {
		backendCmd = firstString(backendCmd, envString("ORB_TRANSCRIBE_PROVIDER_LOCAL_CMD", "ORB_TRANSCRIBE_LOCAL_CMD"))
	}

	ffmpegCmd := firstString(*ffmpegCmdLong)

	if !hasExplicitOption(normalizedArgs, "--ffmpeg-cmd") {
		ffmpegCmd = firstString(ffmpegCmd, envString("ORB_TRANSCRIBE_FFMPEG_CMD"))
	}

	modelDir := firstString(*modelDirLong, *whisperModelDirLong)

	if !hasExplicitOption(normalizedArgs, "--model-dir", "--whispercpp-model-dir") {
		modelDir = firstString(modelDir, envString("ORB_TRANSCRIBE_MODEL_DIR", "ORB_TRANSCRIBE_WHISPERCPP_MODEL_DIR"))
	}

	modelFile := firstString(*modelFileLong, *whisperModelFileLong)

	if !hasExplicitOption(normalizedArgs, "--model-file", "--whispercpp-model-file") {
		modelFile = firstString(modelFile, envString("ORB_TRANSCRIBE_MODEL_FILE", "ORB_TRANSCRIBE_WHISPERCPP_MODEL_FILE"))
	}

	workers := *workersLong

	if !hasExplicitOption(normalizedArgs, "--workers") {
		envWorkers := envInt("ORB_TRANSCRIBE_WORKERS")

		if envWorkers > 0 {
			workers = envWorkers
		}
	}

	if workers < 1 {
		workers = config.Workers
	}

	progress := *progressLong

	if !hasExplicitOption(normalizedArgs, "--progress") && envBool("ORB_TRANSCRIBE_PROGRESS") {
		progress = true
	}

	debug := *debugLong

	if !hasExplicitOption(normalizedArgs, "--debug") && envBool("ORB_TRANSCRIBE_DEBUG") {
		debug = true
	}

	config.Files = fileRefs{
		InputFile:  inputFile,
		InputDir:   inputDir,
		OutputFile: resolvedPaths.OutputFile,
		OutputDir:  resolvedPaths.OutputDir,
	}
	config.Provider = provider
	config.Backend.Name = localBackend
	config.Backend.Cmd = backendCmd
	config.Backend.FfmpegCmd = ffmpegCmd
	config.Backend.ModelDir = modelDir
	config.Backend.ModelFile = modelFile
	config.Language = language
	config.OutputFormat = outputFormat
	config.Model = model
	config.SystemPrompt = systemPrompt
	config.OpenAiApiKey = openAiApiKey
	config.Workers = workers
	config.Progress = progress
	config.Debug = debug

	validateErr := config.validate()

	if validateErr != nil {
		return cliConfig{}, validateErr
	}

	return config, nil
}

// normalizeCliArgs accepts ozip-style underscore aliases for long flags.
func normalizeCliArgs(args []string) []string {
	normalizedArgs := make([]string, 0, len(args))

	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			normalizedArgs = append(normalizedArgs, arg)

			continue
		}

		flagKey := arg[2:]
		flagValue := ""

		if separatorIndex := strings.IndexByte(flagKey, '='); separatorIndex >= 0 {
			flagValue = flagKey[separatorIndex:]
			flagKey = flagKey[:separatorIndex]
		}

		flagKey = strings.ReplaceAll(flagKey, "_", "-")
		normalizedArgs = append(normalizedArgs, "--"+flagKey+flagValue)
	}

	return normalizedArgs
}

// firstString returns the first non-empty string from the provided values.
func firstString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)

		if trimmedValue != "" {
			return trimmedValue
		}
	}

	return ""
}

// normalizeLowerValue trims and lowercases one string in separate steps.
func normalizeLowerValue(value string) string {
	normalizedValue := strings.TrimSpace(value)
	normalizedValue = strings.ToLower(normalizedValue)

	return normalizedValue
}

// normalizeDashedValue trims, lowercases, and normalizes underscores to dashes.
func normalizeDashedValue(value string) string {
	normalizedValue := normalizeLowerValue(value)
	normalizedValue = strings.ReplaceAll(normalizedValue, "_", "-")

	return normalizedValue
}

// firstArg returns the first positional argument when available.
func firstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

// envString returns the first non-empty environment variable value.
func envString(names ...string) string {
	for _, envName := range names {
		envValue := strings.TrimSpace(os.Getenv(envName))

		if envValue != "" {
			return envValue
		}
	}

	return ""
}

// envInt returns the first valid positive integer from the provided env vars.
func envInt(names ...string) int {
	for _, envName := range names {
		envValue := strings.TrimSpace(os.Getenv(envName))

		if envValue == "" {
			continue
		}

		parsedValue, parseErr := strconv.Atoi(envValue)

		if parseErr == nil && parsedValue > 0 {
			return parsedValue
		}
	}

	return 0
}

// envBool returns true when any provided env var contains a truthy value.
func envBool(names ...string) bool {
	for _, envName := range names {
		envValue := strings.TrimSpace(os.Getenv(envName))

		if envValue == "" {
			continue
		}

		return isTrueString(envValue)
	}

	return false
}

// hasExplicitOption reports whether the user set any of the given flags directly.
func hasExplicitOption(args []string, optionNames ...string) bool {
	for _, arg := range args {
		for _, optionName := range optionNames {
			if arg == optionName {
				return true
			}

			if strings.HasPrefix(arg, optionName+"=") {
				return true
			}
		}
	}

	return false
}

// looksLikeDir uses a cheap stat check to detect directory positional inputs.
func looksLikeDir(inputRef string) bool {
	normalizedInput := strings.TrimSpace(inputRef)
	inputBase := filepath.Base(normalizedInput)

	if len(inputBase) > 0 && inputBase[0] == '.' {
		return false
	}

	if filepath.Ext(inputBase) != "" {
		return false
	}

	absoluteInput, absErr := filepath.Abs(inputRef)

	if absErr != nil {
		return false
	}

	fileInfo, statErr := os.Stat(absoluteInput)

	if statErr != nil {
		return false
	}

	return fileInfo.IsDir()
}

// resolveInputRef resolves symlinks and validates a required input file or directory.
func resolveInputRef(request inputRefRequest) (inputRefRequest, error) {
	result := request
	absoluteInput, absErr := filepath.Abs(request.RawValue)

	if absErr != nil {
		return inputRefRequest{}, fmt.Errorf("cannot resolve %s: %s", request.ItemType, request.RawValue)
	}

	result.AbsoluteFile = absoluteInput
	fileInfo, statErr := os.Stat(absoluteInput)

	if statErr != nil && request.ItemType == "file" {
		matchedInput, matchErr := tryMediaFileMatch(result)

		if matchErr != nil {
			return inputRefRequest{}, matchErr
		}

		if matchedInput != "" {
			absoluteInput = matchedInput
			result.AbsoluteFile = absoluteInput
			fileInfo, statErr = os.Stat(absoluteInput)
		}
	}

	if statErr != nil {
		return inputRefRequest{}, fmt.Errorf("%s not found: %s", request.ItemType, absoluteInput)
	}

	if request.ItemType == "dir" && !fileInfo.IsDir() {
		return inputRefRequest{}, fmt.Errorf("directory not found: %s", absoluteInput)
	}

	if request.ItemType == "file" && fileInfo.IsDir() {
		return inputRefRequest{}, fmt.Errorf("file not found: %s", absoluteInput)
	}

	resolvedInput, resolveErr := filepath.EvalSymlinks(absoluteInput)

	if resolveErr != nil {
		return inputRefRequest{}, fmt.Errorf("cannot resolve %s: %s", request.ItemType, absoluteInput)
	}

	result.ResolvedFile = resolvedInput

	return result, nil
}

// tryMediaFileMatch guesses one media file when the input omitted its extension.
func tryMediaFileMatch(request inputRefRequest) (string, error) {
	inputName := filepath.Base(request.AbsoluteFile)

	if len(inputName) == 0 || inputName[0] == '.' {
		return "", nil
	}

	if filepath.Ext(inputName) != "" {
		return "", nil
	}

	matches := make([]string, 0, 2)

	for fileExt := range supportedMediaExtensions {
		candidateFile := request.AbsoluteFile + fileExt
		fileInfo, statErr := os.Stat(candidateFile)

		if statErr != nil || fileInfo.IsDir() {
			continue
		}

		matches = append(matches, candidateFile)

		if len(matches) > 1 {
			break
		}
	}

	if len(matches) == 0 {
		return "", nil
	}

	if len(matches) > 1 {
		sort.Strings(matches)

		return "", fmt.Errorf("input file is ambiguous: %s", request.AbsoluteFile)
	}

	return matches[0], nil
}

// resolveOptionalRef returns an absolute file or directory and resolves symlinks when it exists.
func resolveOptionalRef(inputRef string) (string, error) {
	absoluteInput, absErr := filepath.Abs(inputRef)

	if absErr != nil {
		return "", fmt.Errorf("cannot resolve file or directory: %s", inputRef)
	}

	_, statErr := os.Stat(absoluteInput)

	if statErr != nil {
		return absoluteInput, nil
	}

	resolvedInput, resolveErr := filepath.EvalSymlinks(absoluteInput)

	if resolveErr != nil {
		return absoluteInput, nil
	}

	return resolvedInput, nil
}

// normalizeOutputTargets interprets existing directories as target-dir style outputs.
func normalizeOutputTargets(request outputTargetRequest) (fileRefs, error) {
	result := request.Files

	if result.OutputFile != "" {
		outputRef, outputErr := resolveOptionalRef(result.OutputFile)

		if outputErr != nil {
			return fileRefs{}, outputErr
		}

		fileInfo, statErr := os.Stat(outputRef)

		if statErr == nil && fileInfo.IsDir() {
			if result.OutputDir != "" {
				return fileRefs{}, fmt.Errorf("output directory passed twice: %s", outputRef)
			}

			result.OutputDir = outputRef
			result.OutputFile = ""
		} else {
			result.OutputFile = outputRef
		}
	}

	if request.Files.InputDir != "" && result.OutputFile != "" {
		return fileRefs{}, fmt.Errorf("option -o/--output-file cannot be used with --dir; use --target-dir")
	}

	if result.OutputDir != "" {
		outputDirRef, outputDirErr := resolveOptionalRef(result.OutputDir)

		if outputDirErr != nil {
			return fileRefs{}, outputDirErr
		}

		result.OutputDir = outputDirRef
	}

	return result, nil
}

// resolveSystemPrompt reads prompt text from flags or a prompt file.
func resolveSystemPrompt(promptText string, promptFile string) (string, error) {
	if promptText != "" && promptFile != "" {
		return "", fmt.Errorf("use either --system-prompt or --system-prompt-file, not both")
	}

	if promptText != "" {
		return strings.TrimSpace(promptText), nil
	}

	if promptFile == "" {
		return "", nil
	}

	promptFileRequest := newInputRefRequest(promptFile, "file")
	promptFileInfo, resolveErr := resolveInputRef(promptFileRequest)

	if resolveErr != nil {
		return "", resolveErr
	}

	promptBytes, readErr := os.ReadFile(promptFileInfo.ResolvedFile)

	if readErr != nil {
		return "", fmt.Errorf("failed to read prompt file: %s", promptFileInfo.ResolvedFile)
	}

	promptValue := string(promptBytes)
	promptValue = strings.TrimSpace(promptValue)

	return promptValue, nil
}

// normalizeProvider maps aliases to the internal provider names.
func normalizeProvider(value string) string {
	normalizedValue := normalizeDashedValue(value)

	switch normalizedValue {
	case "local", "whisper", "faster-whisper", "fw":
		return "local"
	case "openai", "api", "oai":
		return "openai"
	default:
		return ""
	}
}

// resolveProvider chooses the provider from explicit input or available credentials.
func resolveProvider(value string, openAiApiKey string) string {
	provider := normalizeProvider(value)

	if provider != "" {
		return provider
	}

	if strings.TrimSpace(value) != "" {
		return ""
	}

	if strings.TrimSpace(openAiApiKey) != "" {
		return "openai"
	}

	return "local"
}

// normalizeLocalBackend maps aliases to the internal local backend names.
func normalizeLocalBackend(value string) string {
	normalizedValue := normalizeDashedValue(value)

	switch normalizedValue {
	case "", "cmd", "shell":
		return "cmd"
	case "whispercpp", "whisper-cpp", "whisper.cpp":
		return "whispercpp"
	default:
		return ""
	}
}

// normalizeLanguage maps common aliases to the language code expected by providers.
func normalizeLanguage(value string) string {
	normalizedValue := normalizeLowerValue(value)

	switch normalizedValue {
	case "", "auto":
		return "auto"
	case "en", "english":
		return "en"
	case "bg", "bulgarian", "bulgaria":
		return "bg"
	default:
		return normalizedValue
	}
}

// normalizeOutputFormat keeps output format aliases predictable.
func normalizeOutputFormat(value string) string {
	normalizedValue := normalizeLowerValue(value)

	switch normalizedValue {
	case "", "txt", "text":
		return "txt"
	case "json":
		return "json"
	case "srt":
		return "srt"
	case "vtt":
		return "vtt"
	default:
		return normalizedValue
	}
}

// resolveModel normalizes known model aliases and falls back to sane defaults.
func resolveModel(provider string, value string) string {
	modelValue := normalizeDashedValue(value)

	if provider == "openai" {
		switch modelValue {
		case "", "default", "whisper1", "whisper-1":
			return "whisper-1"
		case "gpt4o-transcribe", "gpt-4o-transcribe":
			return "gpt-4o-transcribe"
		case "gpt4o-mini-transcribe", "gpt-4o-mini-transcribe":
			return "gpt-4o-mini-transcribe"
		default:
			return "whisper-1"
		}
	}

	switch modelValue {
	case "", "default", "medium":
		return "medium"
	case "tiny":
		return "tiny"
	case "base":
		return "base"
	case "small":
		return "small"
	case "large":
		return "large"
	case "largev2", "large-v2":
		return "large-v2"
	case "largev3", "large-v3":
		return "large-v3"
	case "turbo":
		return "turbo"
	default:
		return "medium"
	}
}

// mapApiResponseFormat converts local format names to the OpenAI response values.
func mapApiResponseFormat(value string) string {
	if value == "txt" {
		return "text"
	}

	return value
}

// isTrueString normalizes common truthy strings used in env fallbacks.
func isTrueString(value string) bool {
	normalizedValue := normalizeLowerValue(value)

	switch normalizedValue {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// resolveBinary finds one executable from an explicit override or fallback names.
func resolveBinary(config binaryConfig) (string, error) {
	if config.Value != "" {
		resolvedFile, lookErr := exec.LookPath(config.Value)

		if lookErr != nil {
			return "", fmt.Errorf("%s not found: %s", config.Label, config.Value)
		}

		return resolvedFile, nil
	}

	for _, fileName := range config.Names {
		if fileName == "" {
			continue
		}

		resolvedFile, lookErr := exec.LookPath(fileName)

		if lookErr == nil {
			return resolvedFile, nil
		}
	}

	return "", fmt.Errorf("%s not found", config.Label)
}

// mergeMaps copies all extraData fields into baseData.
func mergeMaps(baseData map[string]any, extraData map[string]any) {
	for extraKey, extraValue := range extraData {
		baseData[extraKey] = extraValue
	}
}

// run collects jobs, processes them, and returns the final data payload.
func (a *app) run() (map[string]any, error) {
	prepareErr := a.prepareProvider()

	if prepareErr != nil {
		return nil, prepareErr
	}

	collectErr := a.collectJobs()

	if collectErr != nil {
		return nil, collectErr
	}

	results, processErr := a.processJobs()

	return a.buildResultData(results), processErr
}

// prepareProvider selects the provider and resolves its runtime dependencies.
func (a *app) prepareProvider() error {
	switch a.config.Provider {
	case "openai":
		a.provider = openAiProvider{}
	case "local":
		a.provider = localProvider{}
	default:
		return fmt.Errorf("unsupported provider: %s", a.config.Provider)
	}

	return a.provider.prepare(a)
}

// collectJobs builds a unified job list for both single-file and directory mode.
func (a *app) collectJobs() error {
	if a.config.Files.InputFile != "" {
		a.jobs = []jobInput{a.newJobInput(0, a.config.Files.InputFile)}

		return nil
	}

	collector := &dirCollector{}
	walkErr := filepath.WalkDir(a.config.Files.InputDir, collector.walk)

	if walkErr != nil {
		return walkErr
	}

	if len(collector.files) == 0 {
		return fmt.Errorf("no media files found in directory: %s", a.config.Files.InputDir)
	}

	sort.Strings(collector.files)
	a.jobs = make([]jobInput, 0, len(collector.files))

	for fileIndex, inputFile := range collector.files {
		a.jobs = append(a.jobs, a.newJobInput(fileIndex, inputFile))
	}

	return nil
}

// newJobInput builds one job with the computed output file.
func (a *app) newJobInput(index int, inputFile string) jobInput {
	outputFile := a.config.Files.OutputFile

	if outputFile == "" || a.config.Files.InputDir != "" {
		outputFile = a.buildOutputFile(inputFile)
	}

	return jobInput{
		Index: index,
		Files: fileRefs{
			InputFile:  inputFile,
			OutputFile: outputFile,
		},
	}
}

// walk handles one filesystem entry during directory scanning.
func (c *dirCollector) walk(inputFile string, dirEntry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}

	entryName := dirEntry.Name()

	if len(entryName) > 0 && entryName[0] == '.' {
		if dirEntry.IsDir() {
			return filepath.SkipDir
		}

		return nil
	}

	if dirEntry.IsDir() {
		return nil
	}

	if !isSupportedMediaFile(inputFile) {
		return nil
	}

	c.files = append(c.files, inputFile)

	return nil
}

// isSupportedMediaFile uses a cheap extension filter during directory scans.
func isSupportedMediaFile(inputFile string) bool {
	fileExt := filepath.Ext(inputFile)
	fileExt = strings.ToLower(fileExt)

	if fileExt == "" {
		return false
	}

	return supportedMediaExtensions[fileExt]
}

// buildOutputFile calculates the output transcript file for one input file.
func (a *app) buildOutputFile(inputFile string) string {
	fileBase := strings.TrimSuffix(filepath.Base(inputFile), filepath.Ext(inputFile))
	outputBase := fileBase + "_transcript." + a.config.OutputFormat
	outputDir := filepath.Dir(inputFile)

	if a.config.Files.OutputDir != "" {
		outputDir = a.config.Files.OutputDir
		relativeDir := a.buildRelativeDir(inputFile)

		if relativeDir != "" {
			outputDir = filepath.Join(outputDir, relativeDir)
		}
	}

	return filepath.Join(outputDir, outputBase)
}

// buildRelativeDir preserves directory layout under --target-dir.
func (a *app) buildRelativeDir(inputFile string) string {
	if a.config.Files.InputDir == "" {
		return ""
	}

	inputDir := filepath.Dir(inputFile)
	relativeDir, relativeErr := filepath.Rel(a.config.Files.InputDir, inputDir)

	if relativeErr != nil || relativeDir == "." {
		return ""
	}

	return relativeDir
}

// processJobs runs jobs sequentially or in parallel depending on worker count.
func (a *app) processJobs() ([]map[string]any, error) {
	if len(a.jobs) == 1 || a.config.Workers < 2 {
		return a.processJobsSequential()
	}

	return a.processJobsParallel()
}

// processJobsSequential processes jobs in order and prints progress after each file.
func (a *app) processJobsSequential() ([]map[string]any, error) {
	results := make([]map[string]any, len(a.jobs))
	errorCount := 0

	for jobIndex, job := range a.jobs {
		jobResult, jobErr := a.processJob(job)
		results[job.Index] = jobResult

		if a.config.Progress {
			a.printProgress(jobIndex+1, len(a.jobs), filepath.Base(job.Files.InputFile))
		}

		if jobErr != nil {
			errorCount++
		}
	}

	if errorCount > 0 {
		return results, fmt.Errorf("%d file(s) failed to transcribe", errorCount)
	}

	return results, nil
}

// processJobsParallel processes jobs with a small worker pool and ordered results.
func (a *app) processJobsParallel() ([]map[string]any, error) {
	results := make([]map[string]any, len(a.jobs))
	inputChannel := make(chan jobInput)
	resultChannel := make(chan workerResult)
	waitGroup := &sync.WaitGroup{}
	workerCount := a.config.Workers

	if workerCount > len(a.jobs) {
		workerCount = len(a.jobs)
	}

	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		waitGroup.Add(1)
		go a.runWorker(inputChannel, resultChannel, waitGroup)
	}

	go feedJobs(a.jobs, inputChannel)
	go closeResults(waitGroup, resultChannel)

	completedJobs := 0
	errorCount := 0

	for workerOutput := range resultChannel {
		results[workerOutput.Index] = workerOutput.Data
		completedJobs++

		if a.config.Progress && workerOutput.Data != nil {
			fileValue, _ := workerOutput.Data["file"].(string)
			a.printProgress(completedJobs, len(a.jobs), filepath.Base(fileValue))
		}

		if workerOutput.Err != nil {
			errorCount++
		}
	}

	if errorCount > 0 {
		return results, fmt.Errorf("%d file(s) failed to transcribe", errorCount)
	}

	return results, nil
}

// runWorker processes jobs from the input channel until it is closed.
func (a *app) runWorker(inputChannel <-chan jobInput, resultChannel chan<- workerResult, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()

	for job := range inputChannel {
		jobResult, jobErr := a.processJob(job)
		resultChannel <- workerResult{
			Index: job.Index,
			Data:  jobResult,
			Err:   jobErr,
		}
	}
}

// feedJobs sends all jobs to the worker pool and then closes the input channel.
func feedJobs(jobs []jobInput, inputChannel chan<- jobInput) {
	for _, job := range jobs {
		inputChannel <- job
	}

	close(inputChannel)
}

// closeResults closes the result channel after all workers are done.
func closeResults(waitGroup *sync.WaitGroup, resultChannel chan workerResult) {
	waitGroup.Wait()
	close(resultChannel)
}

// processJob creates the output directory and runs the selected provider.
func (a *app) processJob(job jobInput) (map[string]any, error) {
	outputDir := filepath.Dir(job.Files.OutputFile)
	mkdirErr := os.MkdirAll(outputDir, 0755)

	if mkdirErr != nil {
		jobErr := fmt.Errorf("failed to create output directory: %s", outputDir)

		return a.newErrorResult(job, jobErr), jobErr
	}

	if a.provider == nil {
		jobErr := fmt.Errorf("provider is not initialized")

		return a.newErrorResult(job, jobErr), jobErr
	}

	resultData, jobErr := a.provider.transcribe(a, job)

	if jobErr != nil {
		return a.newErrorResult(job, jobErr), jobErr
	}

	return resultData, nil
}

// prepare resolves runtime dependencies for the OpenAI provider.
func (p openAiProvider) prepare(a *app) error {
	return nil
}

// transcribe sends one audio file to OpenAI and writes the response.
func (p openAiProvider) transcribe(a *app, job jobInput) (map[string]any, error) {
	fileHandle, openErr := os.Open(job.Files.InputFile)

	if openErr != nil {
		return nil, fmt.Errorf("failed to open file: %s", job.Files.InputFile)
	}

	defer fileHandle.Close()

	client := openai.NewClient(option.WithAPIKey(a.config.OpenAiApiKey))
	requestParams := openai.AudioTranscriptionNewParams{
		File:           fileHandle,
		Model:          openai.AudioModel(a.config.Model),
		ResponseFormat: openai.AudioResponseFormat(mapApiResponseFormat(a.config.OutputFormat)),
	}

	if a.config.Language != "" && a.config.Language != "auto" {
		requestParams.Language = openai.String(a.config.Language)
	}

	if a.config.SystemPrompt != "" {
		requestParams.Prompt = openai.String(a.config.SystemPrompt)
	}

	requestContext, cancelRequest := context.WithTimeout(context.Background(), 5*time.Minute)

	defer cancelRequest()

	var httpResponse *http.Response
	var responseBody []byte

	requestErr := client.Post(
		requestContext,
		"/audio/transcriptions",
		requestParams,
		&responseBody,
		option.WithResponseInto(&httpResponse),
	)

	if requestErr != nil {
		return nil, fmt.Errorf("openai request failed: %w", requestErr)
	}

	writeErr := os.WriteFile(job.Files.OutputFile, responseBody, 0644)

	if writeErr != nil {
		return nil, fmt.Errorf("failed to write transcript output: %s", job.Files.OutputFile)
	}

	transcriptText := string(responseBody)
	transcriptText = strings.TrimSpace(transcriptText)
	resultData := a.newSuccessResult(job, transcriptText, len(responseBody))

	if a.config.Debug && httpResponse != nil {
		resultData["api_url"] = "https://api.openai.com/v1/audio/transcriptions"
		resultData["http_status"] = httpResponse.StatusCode
	}

	return resultData, nil
}

// prepare selects the local backend and resolves its runtime dependencies.
func (p localProvider) prepare(a *app) error {
	switch a.config.Backend.Name {
	case "cmd":
		p.backend = localCmdBackend{}
	case "whispercpp":
		p.backend = localWhisperCppBackend{}
	default:
		return fmt.Errorf("unsupported local backend: %s", a.config.Backend.Name)
	}

	a.provider = p

	return p.backend.prepare(a)
}

// transcribe sends one audio file to the selected local backend.
func (p localProvider) transcribe(a *app, job jobInput) (map[string]any, error) {
	if p.backend == nil {
		return nil, fmt.Errorf("local backend is not initialized")
	}

	return p.backend.transcribe(a, job)
}

// prepare resolves the local transcription binary before work starts.
func (p localCmdBackend) prepare(a *app) error {
	localCmdConfig := binaryConfig{
		Label: "local transcribe binary",
		Value: a.config.Backend.Cmd,
		Names: []string{"qs_transcribe"},
	}
	localCmdFile, lookErr := resolveBinary(localCmdConfig)

	if lookErr != nil {
		return lookErr
	}

	a.config.Backend.Cmd = localCmdFile

	return nil
}

// transcribe shells out to the local qs_transcribe-compatible binary.
func (p localCmdBackend) transcribe(a *app, job jobInput) (map[string]any, error) {
	localCmdArgs := []string{
		"--file", job.Files.InputFile,
		"--lang", a.config.Language,
		"--model", a.config.Model,
		"--output-file", job.Files.OutputFile,
	}

	command := exec.Command(a.config.Backend.Cmd, localCmdArgs...)

	if a.shouldStreamLocalProgress() {
		command.Stdout = io.Discard
		command.Stderr = os.Stderr

		runErr := command.Run()

		if runErr != nil {
			return nil, fmt.Errorf("local transcription failed: %w", runErr)
		}
	} else {
		combinedOutput, runErr := command.CombinedOutput()

		if runErr != nil {
			errorDetail := string(combinedOutput)
			errorDetail = strings.TrimSpace(errorDetail)

			if errorDetail == "" {
				errorDetail = runErr.Error()
			}

			return nil, fmt.Errorf("local transcription failed: %s", errorDetail)
		}
	}

	transcriptBytes, readErr := os.ReadFile(job.Files.OutputFile)

	if readErr != nil {
		return nil, fmt.Errorf("failed to read transcript output: %s", job.Files.OutputFile)
	}

	transcriptText := string(transcriptBytes)
	transcriptText = strings.TrimSpace(transcriptText)
	resultData := a.newSuccessResult(job, transcriptText, len(transcriptBytes))

	if a.config.Debug {
		resultData["local_cmd"] = a.config.Backend.Cmd
		resultData["local_cmd_args"] = localCmdArgs
	}

	if a.config.SystemPrompt != "" {
		resultData["prompt_ignored"] = true
	}

	return resultData, nil
}

// prepare validates the future whisper.cpp backend configuration.
func (p localWhisperCppBackend) prepare(a *app) error {
	ffmpegConfig := binaryConfig{
		Label: "ffmpeg binary",
		Value: a.config.Backend.FfmpegCmd,
		Names: []string{"ffmpeg"},
	}
	ffmpegFile, lookErr := resolveBinary(ffmpegConfig)

	if lookErr != nil {
		return lookErr
	}

	a.config.Backend.FfmpegCmd = ffmpegFile

	return fmt.Errorf("local backend whispercpp is not implemented yet")
}

// transcribe keeps the localWhisperCppBackend interface complete.
func (p localWhisperCppBackend) transcribe(a *app, job jobInput) (map[string]any, error) {
	return nil, fmt.Errorf("local backend whispercpp is not implemented yet")
}

// shouldStreamLocalProgress lets a single local job show provider-native progress.
func (a *app) shouldStreamLocalProgress() bool {
	return a.config.Progress && len(a.jobs) == 1
}

// newSuccessResult builds the per-file JSON result on success.
func (a *app) newSuccessResult(job jobInput, transcriptText string, outputSize int) map[string]any {
	resultData := map[string]any{
		"file":          job.Files.InputFile,
		"output_file":   job.Files.OutputFile,
		"provider":      a.config.Provider,
		"language":      a.config.Language,
		"output_format": a.config.OutputFormat,
		"model":         a.config.Model,
		"transcript":    transcriptText,
		"output_size":   outputSize,
		"status":        true,
		"skipped":       false,
	}

	if a.config.SystemPrompt != "" {
		resultData["system_prompt_set"] = true
	}

	if a.config.Provider == "local" {
		resultData["local_backend"] = a.config.Backend.Name
	}

	return resultData
}

// newErrorResult builds the per-file JSON result on failure.
func (a *app) newErrorResult(job jobInput, jobErr error) map[string]any {
	resultData := map[string]any{
		"file":          job.Files.InputFile,
		"output_file":   job.Files.OutputFile,
		"provider":      a.config.Provider,
		"language":      a.config.Language,
		"output_format": a.config.OutputFormat,
		"model":         a.config.Model,
		"status":        false,
		"skipped":       false,
		"msg":           jobErr.Error(),
	}

	if a.config.SystemPrompt != "" {
		resultData["system_prompt_set"] = true
	}

	if a.config.Provider == "local" {
		resultData["local_backend"] = a.config.Backend.Name
	}

	return resultData
}

// buildResultData returns either a single-file payload or a batch summary payload.
func (a *app) buildResultData(results []map[string]any) map[string]any {
	if len(results) == 1 {
		return results[0]
	}

	processedCount := 0
	errorCount := 0

	for _, resultData := range results {
		statusValue, _ := resultData["status"].(bool)

		if statusValue {
			processedCount++
		} else {
			errorCount++
		}
	}

	return map[string]any{
		"source_dir":    a.config.Files.InputDir,
		"target_dir":    a.config.Files.OutputDir,
		"provider":      a.config.Provider,
		"local_backend": a.config.Backend.Name,
		"results":       results,
		"total":         len(results),
		"processed":     processedCount,
		"errors":        errorCount,
	}
}

// printProgress writes one stable progress line to stderr.
func (a *app) printProgress(current int, total int, label string) {
	if total < 1 {
		total = 1
	}

	percentValue := (float64(current) / float64(total)) * 100
	progressMessage := fmt.Sprintf("%d/%d (%.1f%%)", current, total, percentValue)

	if label != "" {
		progressMessage += " " + label
	}

	a.progressMu.Lock()
	_, _ = fmt.Fprintln(os.Stderr, progressMessage)
	a.progressMu.Unlock()
}
