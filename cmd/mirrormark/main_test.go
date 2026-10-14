package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWithoutArgsPrintsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "Usage: mirrormark") {
		t.Fatalf("stderr %q has no usage", errb.String())
	}
}

func TestHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Usage: mirrormark") {
		t.Fatalf("stdout %q has no usage", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}
