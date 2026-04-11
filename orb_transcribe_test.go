package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNormalizeCliArgs keeps underscore and hyphen long flags equivalent.
func TestNormalizeCliArgs(t *testing.T) {
	inputArgs := []string{
		"--openai_api_key=test-key",
		"--target_dir",
		"/tmp/out",
		"--system_prompt_file",
		"prompt.txt",
	}

	normalizedArgs := normalizeCliArgs(inputArgs)

	if normalizedArgs[0] != "--openai-api-key=test-key" {
		t.Fatalf("normalizeCliArgs() first arg = %q", normalizedArgs[0])
	}

	if normalizedArgs[1] != "--target-dir" {
		t.Fatalf("normalizeCliArgs() second arg = %q", normalizedArgs[1])
	}

	if normalizedArgs[3] != "--system-prompt-file" {
		t.Fatalf("normalizeCliArgs() fourth arg = %q", normalizedArgs[3])
	}
}

// TestNormalizeProvider covers the supported provider aliases.
func TestNormalizeProvider(t *testing.T) {
	testCases := map[string]string{
		"":               "",
		"local":          "local",
		"whisper":        "local",
		"faster-whisper": "local",
		"faster_whisper": "local",
		"fw":             "local",
		"openai":         "openai",
		"api":            "openai",
		"oai":            "openai",
		"something-else": "",
	}

	for inputValue, expectedValue := range testCases {
		actualValue := normalizeProvider(inputValue)

		if actualValue != expectedValue {
			t.Fatalf("normalizeProvider(%q) = %q, want %q", inputValue, actualValue, expectedValue)
		}
	}
}

// TestResolveProvider applies the default provider rule around API keys.
func TestResolveProvider(t *testing.T) {
	testCases := []struct {
		ProviderValue string
		ApiKey        string
		ExpectedValue string
	}{
		{"", "", "local"},
		{"", "test-key", "openai"},
		{"local", "test-key", "local"},
		{"openai", "", "openai"},
		{"bad-provider", "", ""},
	}

	for _, testCase := range testCases {
		actualValue := resolveProvider(testCase.ProviderValue, testCase.ApiKey)

		if actualValue != testCase.ExpectedValue {
			t.Fatalf("resolveProvider(%q, %q) = %q, want %q", testCase.ProviderValue, testCase.ApiKey, actualValue, testCase.ExpectedValue)
		}
	}
}

// TestNormalizeLocalBackend covers the supported local backend aliases.
func TestNormalizeLocalBackend(t *testing.T) {
	testCases := map[string]string{
		"":            "cmd",
		"cmd":         "cmd",
		"shell":       "cmd",
		"whispercpp":  "whispercpp",
		"whisper-cpp": "whispercpp",
		"whisper_cpp": "whispercpp",
		"whisper.cpp": "whispercpp",
		"something":   "",
	}

	for inputValue, expectedValue := range testCases {
		actualValue := normalizeLocalBackend(inputValue)

		if actualValue != expectedValue {
			t.Fatalf("normalizeLocalBackend(%q) = %q, want %q", inputValue, actualValue, expectedValue)
		}
	}
}

// TestNormalizeLanguage maps common language aliases.
func TestNormalizeLanguage(t *testing.T) {
	testCases := map[string]string{
		"":          "auto",
		"auto":      "auto",
		"EN":        "en",
		"english":   "en",
		"Bulgarian": "bg",
		"bulgaria":  "bg",
		"something": "something",
	}

	for inputValue, expectedValue := range testCases {
		actualValue := normalizeLanguage(inputValue)

		if actualValue != expectedValue {
			t.Fatalf("normalizeLanguage(%q) = %q, want %q", inputValue, actualValue, expectedValue)
		}
	}
}

// TestNormalizeOutputFormat keeps output aliases stable.
func TestNormalizeOutputFormat(t *testing.T) {
	testCases := map[string]string{
		"":      "txt",
		"txt":   "txt",
		"text":  "txt",
		"JSON":  "json",
		"srt":   "srt",
		"vtt":   "vtt",
		"other": "other",
	}

	for inputValue, expectedValue := range testCases {
		actualValue := normalizeOutputFormat(inputValue)

		if actualValue != expectedValue {
			t.Fatalf("normalizeOutputFormat(%q) = %q, want %q", inputValue, actualValue, expectedValue)
		}
	}
}

// TestResolveModel keeps unknown values on provider defaults.
func TestResolveModel(t *testing.T) {
	testCases := []struct {
		Provider      string
		ModelValue    string
		ModelFile     string
		ExpectedValue string
	}{
		{"local", "", "", "medium"},
		{"local", "medium", "", "medium"},
		{"local", "AAAAA", "", "medium"},
		{"local", "large_v3", "", "large-v3"},
		{"local", "large-v3", "", "large-v3"},
		{"local", "", "/tmp/ggml-large-v3.bin", "large-v3"},
		{"local", "AAAAA", "/tmp/ggml-large-v3-turbo.bin", "turbo"},
		{"openai", "", "", "whisper-1"},
		{"openai", "whisper1", "", "whisper-1"},
		{"openai", "AAAAA", "", "whisper-1"},
	}

	for _, testCase := range testCases {
		modelRequest := modelResolveRequest{
			Provider:  testCase.Provider,
			Value:     testCase.ModelValue,
			ModelFile: testCase.ModelFile,
		}
		actualValue := resolveModel(modelRequest)

		if actualValue != testCase.ExpectedValue {
			t.Fatalf("resolveModel(%q, %q, %q) = %q, want %q", testCase.Provider, testCase.ModelValue, testCase.ModelFile, actualValue, testCase.ExpectedValue)
		}
	}
}

// TestMapApiResponseFormat converts txt to the OpenAI text response name.
func TestMapApiResponseFormat(t *testing.T) {
	textFormat := mapApiResponseFormat("txt")

	if textFormat != "text" {
		t.Fatalf("mapApiResponseFormat(%q) = %q, want %q", "txt", textFormat, "text")
	}

	jsonFormat := mapApiResponseFormat("json")

	if jsonFormat != "json" {
		t.Fatalf("mapApiResponseFormat(%q) = %q, want %q", "json", jsonFormat, "json")
	}
}

// TestBuildOutputFile preserves directory layout under --target-dir.
func TestBuildOutputFile(t *testing.T) {
	application := app{
		config: cliConfig{
			Files: fileRefs{
				InputDir:  "/tmp/source",
				OutputDir: "/tmp/out",
			},
			OutputFormat: "txt",
		},
	}

	outputFile := application.buildOutputFile("/tmp/source/nested/clip.mp3")
	expectedFile := filepath.Join("/tmp/out", "nested", "clip_transcript.txt")

	if outputFile != expectedFile {
		t.Fatalf("buildOutputFile() = %q, want %q", outputFile, expectedFile)
	}
}

// TestResolveSystemPromptFile reads prompt content from a prompt file.
func TestResolveSystemPromptFile(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "prompt.txt")
	writeErr := os.WriteFile(promptFile, []byte("  extra context  \n"), 0644)

	if writeErr != nil {
		t.Fatalf("os.WriteFile() error = %v", writeErr)
	}

	promptValue, promptErr := resolveSystemPrompt("", promptFile)

	if promptErr != nil {
		t.Fatalf("resolveSystemPrompt() error = %v", promptErr)
	}

	if promptValue != "extra context" {
		t.Fatalf("resolveSystemPrompt() = %q, want %q", promptValue, "extra context")
	}
}

// TestResolveInputRef guesses one media file when the extension was omitted.
func TestResolveInputRef(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "clip.mp3")
	writeErr := os.WriteFile(inputFile, []byte("test"), 0644)

	if writeErr != nil {
		t.Fatalf("os.WriteFile() error = %v", writeErr)
	}

	inputInfo, resolveErr := resolveInputRef(newInputRefRequest(filepath.Join(tmpDir, "clip"), "file"))

	if resolveErr != nil {
		t.Fatalf("resolveInputRef() error = %v", resolveErr)
	}

	expectedFile, expectedErr := filepath.EvalSymlinks(inputFile)

	if expectedErr != nil {
		t.Fatalf("filepath.EvalSymlinks() error = %v", expectedErr)
	}

	if inputInfo.ResolvedFile != expectedFile {
		t.Fatalf("resolveInputRef() = %q, want %q", inputInfo.ResolvedFile, expectedFile)
	}
}

// TestNewCliConfig keeps the default config values explicit.
func TestNewCliConfig(t *testing.T) {
	config := newCliConfig()

	if config.Provider != "local" {
		t.Fatalf("newCliConfig().Provider = %q", config.Provider)
	}

	if config.Backend.Name != "cmd" {
		t.Fatalf("newCliConfig().Backend.Name = %q", config.Backend.Name)
	}

	if config.Model != "medium" {
		t.Fatalf("newCliConfig().Model = %q", config.Model)
	}

	if config.Backend.Binary != "" {
		t.Fatalf("newCliConfig().Backend.Binary = %q", config.Backend.Binary)
	}

	if config.Backend.DecodeBinary != "" {
		t.Fatalf("newCliConfig().Backend.DecodeBinary = %q", config.Backend.DecodeBinary)
	}

	if config.Workers < 1 {
		t.Fatalf("newCliConfig().Workers = %d", config.Workers)
	}
}
