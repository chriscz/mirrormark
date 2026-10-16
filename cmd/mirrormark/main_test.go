package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/extension"
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

// A build without a release version prints the embedded extension's.
func TestVersion(t *testing.T) {
	want, err := extension.Version()
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"version", "--version"} {
		var out, errb bytes.Buffer
		if code := run([]string{arg}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%s: exit code %d, want 0; stderr %q", arg, code, errb.String())
		}
		if got := out.String(); got != "mirrormark "+want+"\n" {
			t.Fatalf("%s: stdout %q, want %q", arg, got, "mirrormark "+want+"\n")
		}
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

// help, and the usage printed on an error, say how to share folders and
// name each file where it is for this user, as the XDG variables place it.
func TestHelpNamesTheResolvedFiles(t *testing.T) {
	root := t.TempDir()
	cfg, data, state := filepath.Join(root, "cfg"), filepath.Join(root, "data"), filepath.Join(root, "state")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_STATE_HOME", state)
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	help := out.String()
	for _, want := range []string{
		"\nSharing folders:\n",
		"\n    format: 1\n    unions:\n      work:\n        chrome-personal: toolbar/Work\n        firefox-work: toolbar/Work\n",
		"\nFiles:\n",
		"  config.yaml      " + filepath.Join(cfg, "mirrormark", "config.yaml") + "\n",
		"  databases        " + filepath.Join(data, "mirrormark", "profiles") + "/<id>.yaml\n",
		"  backups          " + filepath.Join(data, "mirrormark", "backups") + "/<id>/\n",
		"  key, extension   " + filepath.Join(data, "mirrormark") + "/ (extension.pem, mirrormark.crx, extension/)\n",
		"  log              " + filepath.Join(state, "mirrormark", "log") + "\n",
		"  serve.lock       " + filepath.Join(data, "mirrormark", "serve.lock") + "\n",
		"  service          " + filepath.Join(cfg, "systemd", "user", "mirrormark.service") + "\n",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help has no %q:\n%s", want, help)
		}
	}
	// The prose, however it is wrapped.
	flat := strings.Join(strings.Fields(help), " ")
	for _, want := range []string{
		"DATABASE column of mirrormark install --browser B",
		"<id> in the line bound: <profile> -> <id>",
		"starts with toolbar, other or mobile, or menu in Firefox Developer Edition and LibreWolf",
		"or the title the browser shows for that folder, such as Bookmarks bar,",
		"as the bookmark manager shows them",
		`write \/ for a slash`,
		"One profile's folders cannot be inside each other",
		"within seconds, with no restart, and the log records each one",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("help has no %q:\n%s", want, help)
		}
	}
	// The example is a config mirrormark takes.
	example := help[strings.Index(help, "    format: 1"):]
	example = example[:strings.Index(example, "\n\n")+1]
	c := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(c, []byte(example), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := db.LoadConfig(c); err != nil || len(got.Unions["work"]) != 2 {
		t.Fatalf("the example %q: %+v, %v", example, got, err)
	}
	errb.Reset()
	run(nil, strings.NewReader(""), &out, &errb)
	if errb.String() != help {
		t.Fatalf("the usage on an error differs from help:\n%s", errb.String())
	}
}
