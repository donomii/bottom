package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFishCompletionIncludesCommandsAndOptions(t *testing.T) {
	var output bytes.Buffer
	if err := runCompletion(nil, &output); err != nil {
		t.Fatalf("write fish completion: %v", err)
	}
	for _, expected := range []string{"-a 'record'", "-a 'trace'", "-a 'completion'", "-l backend", "-l input", "-l perfetto", "-l before", "-l help"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected fish completion to contain %q", expected)
		}
	}
}

func TestCompletionHelpAndArguments(t *testing.T) {
	var output bytes.Buffer
	if err := runCompletion([]string{"-h"}, &output); err != nil {
		t.Fatalf("write completion help: %v", err)
	}
	if !strings.Contains(output.String(), "~/.config/fish/completions/bottom.fish") {
		t.Fatalf("expected completion help to explain installation, received %q", output.String())
	}
	if err := runCompletion([]string{"unexpected"}, &output); err == nil {
		t.Fatal("expected positional completion arguments to be rejected")
	}
}
