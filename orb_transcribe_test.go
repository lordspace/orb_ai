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
		"":               "local",
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
			InputDir:     "/tmp/source",
			TargetDir:    "/tmp/out",
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
