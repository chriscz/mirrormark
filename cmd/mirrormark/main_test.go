package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
)

func TestMain(m *testing.M) {
	// The tests' default: no systemd, as in a container. Tests of the
	// service install their own recorder.
	runSystemctl = func(args ...string) error { return errors.New("systemctl is not available in tests") }
	os.Exit(m.Run())
}

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

// The arguments browsers started the native host with are unknown
// commands now: nothing runs the host that way any more.
func TestUnknownCommand(t *testing.T) {
	for _, arg := range []string{"frobnicate", "host", "chrome-extension://abc/", "/home/u/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json"} {
		var out, errb bytes.Buffer
		if code := run([]string{arg}, strings.NewReader(""), &out, &errb); code != 2 {
			t.Fatalf("%s: exit code %d, want 2", arg, code)
		}
	}
}

// A log over 1 MiB moves to log.1, replacing the one before, when the
// server opens it.
func TestOpenLogRotatesALargeLog(t *testing.T) {
	d := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, d+"/"+v)
	}
	p, err := db.DefaultPaths()
	if err != nil {
		t.Fatal(err)
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
	open := func() {
		t.Helper()
		_, f, err := openLog(p)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	open()
	if old, _ := os.ReadFile(log + ".1"); !bytes.Equal(old, big) {
		t.Fatalf("log.1 holds %d bytes, want the old log", len(old))
	}
	if info, err := os.Stat(log); err != nil || info.Size() >= 1<<20 {
		t.Fatalf("log not started afresh: %v %v", info, err)
	}

	// A small log stays.
	open()
	if old, _ := os.ReadFile(log + ".1"); !bytes.Equal(old, big) {
		t.Fatal("a small log was rotated")
	}
}

// A second server for the same user stops at once, before it listens.
func TestServeRunsOncePerUser(t *testing.T) {
	d := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, d+"/"+v)
	}
	p, _ := db.DefaultPaths()
	unlock, err := db.TryLock(p.ServeLock())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	var out, errb bytes.Buffer
	if code := run([]string{"serve"}, strings.NewReader(""), &out, &errb); code != 1 || !strings.Contains(errb.String(), "already running") {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
}
