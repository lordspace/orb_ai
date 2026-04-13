package main

import "sync"

// cliConfig keeps the full app configuration in one place.
type cliConfig struct {
	Files        fileRefs
	Provider     string
	Backend      backendConfig
	Language     string
	OutputFormat string
	Model        string
	SystemPrompt string
	ApiKey       string
	ApiBaseUrl   string
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
	Name         string
	Binary       string
	DecodeBinary string
	ModelDir     string
	ModelFile    string
}

// binaryConfig describes one executable override plus its smart fallback names.
type binaryConfig struct {
	Label string
	Value string
	Names []string
}

// cmdParams keeps one command binary and its args together.
type cmdParams struct {
	Binary string
	Args   []string
}

// whisperOutputRef keeps the requested output file and the generated whisper-cli file together.
type whisperOutputRef struct {
	BaseFile      string
	GeneratedFile string
	ResultFile    string
}

// transcriptOutput keeps the transcript text and output size together.
type transcriptOutput struct {
	Text string
	Size int
}

// whisperRun keeps one prepared whisper.cpp execution together.
type whisperRun struct {
	WaveFile  string
	OutputRef whisperOutputRef
	Cmd       cmdParams
}

// modelResolveRequest keeps model resolution inputs together.
type modelResolveRequest struct {
	Provider  string
	Value     string
	ModelFile string
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

// app owns the config, job list, and shared progress behavior.
type app struct {
	config     cliConfig
	jobs       []jobInput
	provider   provider
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

// localWhisperCppBackend runs local transcription through whisper-cli.
type localWhisperCppBackend struct{}

// groqProvider sends audio files to the Groq OpenAI-compatible transcription API.
type groqProvider struct{}

// preparedInput holds the audio file ready for whisper-cli plus an optional temp file to clean up.
type preparedInput struct {
	AudioFile string
	TempFile  string
}
