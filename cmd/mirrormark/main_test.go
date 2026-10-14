package main

import (
	"bytes"
	"os"
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

// Chrome's extension-origin argument selects host mode, which logs to the
// log file and exits cleanly when the browser closes the port.
func TestExtensionOriginRunsTheHost(t *testing.T) {
	d := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, d+"/"+v)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"chrome-extension://abc/"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("host wrote %q to stdout", out.String())
	}
	if _, err := os.Stat(d + "/XDG_STATE_HOME/mirrormark/log"); err != nil {
		t.Fatal(err)
	}
}
