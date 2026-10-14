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

// A log over 1 MiB moves to log.1, replacing the one before, when a host
// starts.
func TestHostRotatesALargeLog(t *testing.T) {
	d := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, d+"/"+v)
	}
	log := d + "/XDG_STATE_HOME/mirrormark/log"
	if err := os.MkdirAll(d+"/XDG_STATE_HOME/mirrormark", 0o700); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), 1<<20+1)
	if err := os.WriteFile(log, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log+".1", []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"host"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errb.String())
	}
	if old, _ := os.ReadFile(log + ".1"); !bytes.Equal(old, big) {
		t.Fatalf("log.1 holds %d bytes, want the old log", len(old))
	}
	if info, err := os.Stat(log); err != nil || info.Size() >= 1<<20 {
		t.Fatalf("log not started afresh: %v %v", info, err)
	}

	// A small log stays.
	if code := run([]string{"host"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if old, _ := os.ReadFile(log + ".1"); !bytes.Equal(old, big) {
		t.Fatal("a small log was rotated")
	}
}
