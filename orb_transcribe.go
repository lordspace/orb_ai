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
	InputFile    string
	InputDir     string
	OutputFile   string
	TargetDir    string
	Provider     string
	Language     string
	OutputFormat string
	Model        string
	SystemPrompt string
	OpenAiApiKey string
	LocalCmd     string
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

// transcribeJob represents one file to transcribe.
type transcribeJob struct {
	Index      int
	InputFile  string
	OutputFile string
}

// workerResult carries one finished job result back to the coordinator.
type workerResult struct {
	Index int
	Data  map[string]any
	Err   error
}

// app owns the config, job list, and shared progress behavior.
type app struct {
	config     cliConfig
	jobs       []transcribeJob
	progressMu sync.Mutex
}

// dirCollector walks a directory and collects candidate media files.
type dirCollector struct {
	files []string
}

// main runs the app and prints the final JSON result.
func main() {
	resultRecord, exitCode := runMain(os.Args[1:])
	writeResult(resultRecord)
	os.Exit(exitCode)
}

// runMain builds the config, runs the app, and returns the final result record.
func runMain(args []string) (resultRec, int) {
	resultRecord := newBaseResult()
	exitCode := 0

	config, parseErr := parseArgs(args)

	if parseErr != nil {
		resultRecord.Msg = "error: " + parseErr.Error()

		return resultRecord, 255
	}

	resultRecord.Data["provider"] = config.Provider
	application := app{config: config}
	processData, processErr := application.run()

	if processData != nil {
		mergeMaps(resultRecord.Data, processData)
	}

	if processErr != nil {
		resultRecord.Msg = "error: " + processErr.Error()
		exitCode = 255

		return resultRecord, exitCode
	}

	resultRecord.Status = true

	return resultRecord, exitCode
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
		fmt.Println(`{"status":false,"msg":"error: failed to encode JSON result","data":{}}`)

		return
	}

	fmt.Println(string(jsonBuffer))
}

// parseArgs reads flags, env fallbacks, and prompt file content into one config.
func parseArgs(args []string) (cliConfig, error) {
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
	languageShort := flagSet.String("l", "auto", "Language code")
	languageLong := flagSet.String("language", "", "Language code")
	langLong := flagSet.String("lang", "", "Language code")
	formatShort := flagSet.String("F", "txt", "Output format")
	formatLong := flagSet.String("format", "", "Output format")
	modelLong := flagSet.String("model", "", "Model name")
	systemPromptLong := flagSet.String("system-prompt", "", "Prompt context for models that support it")
	promptLong := flagSet.String("prompt", "", "Prompt context for models that support it")
	systemPromptFileLong := flagSet.String("system-prompt-file", "", "Read prompt context from file")
	promptFileLong := flagSet.String("prompt-file", "", "Read prompt context from file")
	workersLong := flagSet.Int("workers", 0, "Parallel worker count")
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
	targetDirRaw := strings.TrimSpace(*targetDirLong)
	systemPromptRaw := firstString(*systemPromptLong, *promptLong)
	systemPromptFileRaw := firstString(*systemPromptFileLong, *promptFileLong)

	outputFile, targetDir, outputErr := normalizeOutputTargets(outputFileRaw, targetDirRaw, inputDirRaw != "")

	if outputErr != nil {
		return cliConfig{}, outputErr
	}

	inputFile := ""

	if inputFileRaw != "" {
		inputFileResolved, resolveErr := resolveInputPath(inputFileRaw, false)

		if resolveErr != nil {
			return cliConfig{}, resolveErr
		}

		inputFile = inputFileResolved
	}

	inputDir := ""

	if inputDirRaw != "" {
		inputDirResolved, resolveErr := resolveInputPath(inputDirRaw, true)

		if resolveErr != nil {
			return cliConfig{}, resolveErr
		}

		inputDir = inputDirResolved
	}

	providerValue := firstString(*providerShort, *providerLong)

	if !hasExplicitOption(normalizedArgs, "-p", "--provider") {
		providerValue = firstString(providerValue, envString("ORB_TRANSCRIBE_PROVIDER"))
	}

	provider := normalizeProvider(providerValue)

	if provider == "" {
		return cliConfig{}, fmt.Errorf("unsupported provider: %s", providerValue)
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
	modelValue := strings.TrimSpace(*modelLong)

	if !hasExplicitOption(normalizedArgs, "--model") {
		modelValue = firstString(modelValue, envString("ORB_TRANSCRIBE_MODEL"))
	}

	model := resolveDefaultModel(provider, modelValue)
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

	localCmd := envString("ORB_TRANSCRIBE_PROVIDER_LOCAL_CMD", "ORB_TRANSCRIBE_LOCAL_CMD")

	if localCmd == "" {
		localCmd = "qs_transcribe"
	}

	workers := *workersLong

	if !hasExplicitOption(normalizedArgs, "--workers") {
		envWorkers := envInt("ORB_TRANSCRIBE_WORKERS")

		if envWorkers > 0 {
			workers = envWorkers
		}
	}

	if workers < 1 {
		workers = runtime.NumCPU()
	}

	if workers < 1 {
		workers = 1
	}

	progress := *progressLong

	if !hasExplicitOption(normalizedArgs, "--progress") && envBool("ORB_TRANSCRIBE_PROGRESS") {
		progress = true
	}

	debug := *debugLong

	if !hasExplicitOption(normalizedArgs, "--debug") && envBool("ORB_TRANSCRIBE_DEBUG") {
		debug = true
	}

	if provider == "openai" && openAiApiKey == "" {
		return cliConfig{}, fmt.Errorf("api key required for openai provider")
	}

	if provider == "local" && outputFormat != "txt" {
		return cliConfig{}, fmt.Errorf("local provider currently supports txt output only")
	}

	return cliConfig{
		InputFile:    inputFile,
		InputDir:     inputDir,
		OutputFile:   outputFile,
		TargetDir:    targetDir,
		Provider:     provider,
		Language:     language,
		OutputFormat: outputFormat,
		Model:        model,
		SystemPrompt: systemPrompt,
		OpenAiApiKey: openAiApiKey,
		LocalCmd:     localCmd,
		Workers:      workers,
		Progress:     progress,
		Debug:        debug,
	}, nil
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
func looksLikeDir(path string) bool {
	absolutePath, absErr := filepath.Abs(path)

	if absErr != nil {
		return false
	}

	fileInfo, statErr := os.Stat(absolutePath)

	if statErr != nil {
		return false
	}

	return fileInfo.IsDir()
}

// resolveInputPath resolves symlinks and validates a required input path.
func resolveInputPath(path string, expectDir bool) (string, error) {
	absolutePath, absErr := filepath.Abs(path)

	if absErr != nil {
		return "", fmt.Errorf("cannot resolve path: %s", path)
	}

	resolvedPath, resolveErr := filepath.EvalSymlinks(absolutePath)

	if resolveErr != nil {
		return "", fmt.Errorf("cannot resolve path: %s", absolutePath)
	}

	fileInfo, statErr := os.Stat(resolvedPath)

	if statErr != nil {
		return "", fmt.Errorf("path not found: %s", resolvedPath)
	}

	if expectDir && !fileInfo.IsDir() {
		return "", fmt.Errorf("directory not found: %s", resolvedPath)
	}

	if !expectDir && fileInfo.IsDir() {
		return "", fmt.Errorf("file not found: %s", resolvedPath)
	}

	return resolvedPath, nil
}

// resolveOptionalPath returns an absolute path and resolves symlinks when it exists.
func resolveOptionalPath(path string) (string, error) {
	absolutePath, absErr := filepath.Abs(path)

	if absErr != nil {
		return "", fmt.Errorf("cannot resolve path: %s", path)
	}

	_, statErr := os.Stat(absolutePath)

	if statErr != nil {
		return absolutePath, nil
	}

	resolvedPath, resolveErr := filepath.EvalSymlinks(absolutePath)

	if resolveErr != nil {
		return absolutePath, nil
	}

	return resolvedPath, nil
}

// normalizeOutputTargets interprets existing directories as target-dir style outputs.
func normalizeOutputTargets(outputFileRaw string, targetDirRaw string, dirMode bool) (string, string, error) {
	outputFile := ""
	targetDir := targetDirRaw

	if outputFileRaw != "" {
		outputPath, outputErr := resolveOptionalPath(outputFileRaw)

		if outputErr != nil {
			return "", "", outputErr
		}

		fileInfo, statErr := os.Stat(outputPath)

		if statErr == nil && fileInfo.IsDir() {
			if targetDir != "" {
				return "", "", fmt.Errorf("output directory passed twice: %s", outputPath)
			}

			targetDir = outputPath
		} else {
			outputFile = outputPath
		}
	}

	if dirMode && outputFile != "" {
		return "", "", fmt.Errorf("option -o/--output-file cannot be used with --dir; use --target-dir")
	}

	if targetDir != "" {
		targetDirPath, targetDirErr := resolveOptionalPath(targetDir)

		if targetDirErr != nil {
			return "", "", targetDirErr
		}

		targetDir = targetDirPath
	}

	return outputFile, targetDir, nil
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

	promptFilePath, resolveErr := resolveInputPath(promptFile, false)

	if resolveErr != nil {
		return "", resolveErr
	}

	promptBytes, readErr := os.ReadFile(promptFilePath)

	if readErr != nil {
		return "", fmt.Errorf("failed to read prompt file: %s", promptFilePath)
	}

	return strings.TrimSpace(string(promptBytes)), nil
}

// normalizeProvider maps aliases to the internal provider names.
func normalizeProvider(value string) string {
	normalizedValue := strings.TrimSpace(strings.ToLower(value))
	normalizedValue = strings.ReplaceAll(normalizedValue, "_", "-")

	switch normalizedValue {
	case "", "local", "whisper", "faster-whisper", "fw":
		return "local"
	case "openai", "api", "oai":
		return "openai"
	default:
		return ""
	}
}

// normalizeLanguage maps common aliases to the language code expected by providers.
func normalizeLanguage(value string) string {
	normalizedValue := strings.TrimSpace(strings.ToLower(value))

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
	normalizedValue := strings.TrimSpace(strings.ToLower(value))

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

// resolveDefaultModel picks a provider-specific default when the user omitted one.
func resolveDefaultModel(provider string, value string) string {
	modelValue := strings.TrimSpace(strings.ToLower(value))

	if modelValue != "" {
		return modelValue
	}

	if provider == "openai" {
		return "whisper-1"
	}

	return "medium"
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
	normalizedValue := strings.TrimSpace(strings.ToLower(value))

	switch normalizedValue {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// mergeMaps copies all extraData fields into baseData.
func mergeMaps(baseData map[string]any, extraData map[string]any) {
	for extraKey, extraValue := range extraData {
		baseData[extraKey] = extraValue
	}
}

// run collects jobs, processes them, and returns the final data payload.
func (a *app) run() (map[string]any, error) {
	prepareErr := a.prepare()

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

// prepare resolves provider-specific runtime dependencies once before processing.
func (a *app) prepare() error {
	if a.config.Provider != "local" {
		return nil
	}

	localCmdPath, lookErr := exec.LookPath(a.config.LocalCmd)

	if lookErr != nil {
		return fmt.Errorf("local transcribe binary not found in PATH: %s", a.config.LocalCmd)
	}

	a.config.LocalCmd = localCmdPath

	return nil
}

// collectJobs builds a unified job list for both single-file and directory mode.
func (a *app) collectJobs() error {
	if a.config.InputFile != "" {
		outputFile := a.config.OutputFile

		if outputFile == "" {
			outputFile = a.buildOutputFile(a.config.InputFile)
		}

		a.jobs = []transcribeJob{
			{
				Index:      0,
				InputFile:  a.config.InputFile,
				OutputFile: outputFile,
			},
		}

		return nil
	}

	collector := &dirCollector{}
	walkErr := filepath.WalkDir(a.config.InputDir, collector.walk)

	if walkErr != nil {
		return walkErr
	}

	if len(collector.files) == 0 {
		return fmt.Errorf("no media files found in directory: %s", a.config.InputDir)
	}

	sort.Strings(collector.files)
	a.jobs = make([]transcribeJob, 0, len(collector.files))

	for fileIndex, inputFile := range collector.files {
		job := transcribeJob{
			Index:      fileIndex,
			InputFile:  inputFile,
			OutputFile: a.buildOutputFile(inputFile),
		}

		a.jobs = append(a.jobs, job)
	}

	return nil
}

// walk handles one filesystem entry during directory scanning.
func (c *dirCollector) walk(path string, dirEntry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}

	if dirEntry.IsDir() {
		return nil
	}

	if !isSupportedMediaFile(path) {
		return nil
	}

	c.files = append(c.files, path)

	return nil
}

// isSupportedMediaFile uses a cheap extension filter during directory scans.
func isSupportedMediaFile(path string) bool {
	fileExt := strings.ToLower(filepath.Ext(path))

	if fileExt == "" {
		return false
	}

	return supportedMediaExtensions[fileExt]
}

// buildOutputFile calculates the output transcript path for one input file.
func (a *app) buildOutputFile(inputFile string) string {
	fileBase := strings.TrimSuffix(filepath.Base(inputFile), filepath.Ext(inputFile))
	outputBase := fileBase + "_transcript." + a.config.OutputFormat
	outputDir := filepath.Dir(inputFile)

	if a.config.TargetDir != "" {
		outputDir = a.config.TargetDir
		relativeDir := buildRelativeDir(a.config.InputDir, inputFile)

		if relativeDir != "" {
			outputDir = filepath.Join(outputDir, relativeDir)
		}
	}

	return filepath.Join(outputDir, outputBase)
}

// buildRelativeDir preserves directory layout under --target-dir.
func buildRelativeDir(rootDir string, inputFile string) string {
	if rootDir == "" {
		return ""
	}

	inputDir := filepath.Dir(inputFile)
	relativeDir, relativeErr := filepath.Rel(rootDir, inputDir)

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
			a.printProgress(jobIndex+1, len(a.jobs), filepath.Base(job.InputFile))
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
	inputChannel := make(chan transcribeJob)
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
func (a *app) runWorker(inputChannel <-chan transcribeJob, resultChannel chan<- workerResult, waitGroup *sync.WaitGroup) {
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
func feedJobs(jobs []transcribeJob, inputChannel chan<- transcribeJob) {
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
func (a *app) processJob(job transcribeJob) (map[string]any, error) {
	outputDir := filepath.Dir(job.OutputFile)
	mkdirErr := os.MkdirAll(outputDir, 0755)

	if mkdirErr != nil {
		jobErr := fmt.Errorf("failed to create output directory: %s", outputDir)

		return a.newErrorResult(job, jobErr), jobErr
	}

	resultData := map[string]any{}
	var jobErr error

	switch a.config.Provider {
	case "openai":
		resultData, jobErr = a.transcribeWithOpenAi(job)
	case "local":
		resultData, jobErr = a.transcribeWithLocal(job)
	default:
		jobErr = fmt.Errorf("unsupported provider: %s", a.config.Provider)
	}

	if jobErr != nil {
		return a.newErrorResult(job, jobErr), jobErr
	}

	return resultData, nil
}

// transcribeWithOpenAi sends one audio file to OpenAI and writes the response.
func (a *app) transcribeWithOpenAi(job transcribeJob) (map[string]any, error) {
	fileHandle, openErr := os.Open(job.InputFile)

	if openErr != nil {
		return nil, fmt.Errorf("failed to open file: %s", job.InputFile)
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

	writeErr := os.WriteFile(job.OutputFile, responseBody, 0644)

	if writeErr != nil {
		return nil, fmt.Errorf("failed to write transcript output: %s", job.OutputFile)
	}

	transcriptText := strings.TrimSpace(string(responseBody))
	resultData := a.newSuccessResult(job, transcriptText, len(responseBody))

	if a.config.Debug && httpResponse != nil {
		resultData["api_url"] = "https://api.openai.com/v1/audio/transcriptions"
		resultData["http_status"] = httpResponse.StatusCode
	}

	return resultData, nil
}

// transcribeWithLocal shells out to the local qs_transcribe-compatible binary.
func (a *app) transcribeWithLocal(job transcribeJob) (map[string]any, error) {
	localCmdArgs := []string{
		"--file", job.InputFile,
		"--lang", a.config.Language,
		"--model", a.config.Model,
		"--output-file", job.OutputFile,
	}

	command := exec.Command(a.config.LocalCmd, localCmdArgs...)

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
			errorDetail := strings.TrimSpace(string(combinedOutput))

			if errorDetail == "" {
				errorDetail = runErr.Error()
			}

			return nil, fmt.Errorf("local transcription failed: %s", errorDetail)
		}
	}

	transcriptBytes, readErr := os.ReadFile(job.OutputFile)

	if readErr != nil {
		return nil, fmt.Errorf("failed to read transcript output: %s", job.OutputFile)
	}

	transcriptText := strings.TrimSpace(string(transcriptBytes))
	resultData := a.newSuccessResult(job, transcriptText, len(transcriptBytes))

	if a.config.Debug {
		resultData["local_cmd"] = a.config.LocalCmd
		resultData["local_cmd_args"] = localCmdArgs
	}

	if a.config.SystemPrompt != "" {
		resultData["prompt_ignored"] = true
	}

	return resultData, nil
}

// shouldStreamLocalProgress lets a single local job show provider-native progress.
func (a *app) shouldStreamLocalProgress() bool {
	return a.config.Progress && len(a.jobs) == 1
}

// newSuccessResult builds the per-file JSON result on success.
func (a *app) newSuccessResult(job transcribeJob, transcriptText string, outputSize int) map[string]any {
	resultData := map[string]any{
		"file":          job.InputFile,
		"output_file":   job.OutputFile,
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

	return resultData
}

// newErrorResult builds the per-file JSON result on failure.
func (a *app) newErrorResult(job transcribeJob, jobErr error) map[string]any {
	resultData := map[string]any{
		"file":          job.InputFile,
		"output_file":   job.OutputFile,
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
		"source_dir": a.config.InputDir,
		"target_dir": a.config.TargetDir,
		"provider":   a.config.Provider,
		"results":    results,
		"total":      len(results),
		"processed":  processedCount,
		"errors":     errorCount,
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
