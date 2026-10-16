package main

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// fakeSystemctl records each systemctl call, failing those fail names.
func fakeSystemctl(t *testing.T, fail string) *[]string {
	calls := &[]string{}
	old := runSystemctl
	t.Cleanup(func() { runSystemctl = old })
	runSystemctl = func(args ...string) error {
		*calls = append(*calls, strings.Join(args, " "))
		if fail != "" && strings.HasPrefix(strings.Join(args, " "), fail) {
			return errors.New("Failed to connect to bus")
		}
		return nil
	}
	return calls
}

func TestUnit(t *testing.T) {
	got := string(unit("/opt/mirror mark/100%/mirrormark"))
	want := `# Written by mirrormark install; mirrormark uninstall removes it.
[Unit]
Description=mirrormark bookmark sync

[Service]
ExecStart="/opt/mirror mark/100%%/mirrormark" serve
Restart=on-failure

[Install]
WantedBy=default.target
`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	for self, want := range map[string]string{
		"/opt/a$b/mirrormark":        "ExecStart=/opt/a$$b/mirrormark serve\n",
		`/opt/a\b/mirrormark`:        `ExecStart="/opt/a\\b/mirrormark" serve` + "\n",
		`/opt/a"b/mirrormark`:        `ExecStart="/opt/a\"b/mirrormark" serve` + "\n",
		"/opt/it's/mirrormark":       `ExecStart="/opt/it's/mirrormark" serve` + "\n",
		`/opt/$x "y" \z%/mirrormark`: `ExecStart="/opt/$$x \"y\" \\z%%/mirrormark" serve` + "\n",
	} {
		if got := string(unit(self)); !strings.Contains(got, want) {
			t.Errorf("unit(%q) has no %q:\n%s", self, want, got)
		}
	}
}

// Every run restarts the server, so it runs the binary just installed.
func TestStartServer(t *testing.T) {
	p := testHome(t)
	calls := fakeSystemctl(t, "")
	var out bytes.Buffer
	for range 2 {
		*calls = nil
		if err := startServer(p, "/bin/mirrormark", &out); err != nil {
			t.Fatal(err)
		}
		if want := []string{"daemon-reload", "enable mirrormark.service", "restart mirrormark.service"}; !slices.Equal(*calls, want) {
			t.Fatalf("calls %q", *calls)
		}
	}
	data, err := os.ReadFile(unitPath(p))
	if err != nil || !bytes.Contains(data, []byte("ExecStart=/bin/mirrormark serve\n")) {
		t.Fatalf("unit %q, %v", data, err)
	}
}

// install says what it set up, that it started the server or restarted
// the one running, and how to look at it and stop it.
func TestStartServerSaysWhatItDid(t *testing.T) {
	p := testHome(t)
	fakeSystemctl(t, "")
	how := "  status: systemctl --user status mirrormark\n" +
		"  log:    " + p.LogFile() + "\n" +
		"  stop:   systemctl --user disable --now mirrormark\n"
	var out bytes.Buffer
	if err := startServer(p, "/bin/mirrormark", &out); err != nil {
		t.Fatal(err)
	}
	if want := "Set up mirrormark serve as a systemd user service, " + unitPath(p) + ". It runs as you and starts when you log in. Started it now.\n" + how; out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
	fakeServer(t, p)
	out.Reset()
	if err := startServer(p, "/bin/mirrormark", &out); err != nil {
		t.Fatal(err)
	}
	if want := "Set up mirrormark serve as a systemd user service, " + unitPath(p) + ". It runs as you and starts when you log in. It was running, so it was restarted now to run this binary; connected profiles reconnect within a minute.\n" + how; out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// A server the user started by hand holds the serve lock too. A restart
// would start a unit that cannot take the lock, and the old binary would
// keep running, so install leaves the service to start once that one stops.
func TestStartServerWithAHandStartedServer(t *testing.T) {
	p := testHome(t)
	calls := fakeSystemctl(t, "is-active")
	fakeServer(t, p)
	var out bytes.Buffer
	if err := startServer(p, "/bin/mirrormark", &out); err != nil {
		t.Fatal(err)
	}
	if want := []string{"daemon-reload", "enable mirrormark.service", "is-active --quiet mirrormark.service"}; !slices.Equal(*calls, want) {
		t.Fatalf("calls %q", *calls)
	}
	want := "Set up mirrormark serve as a systemd user service, " + unitPath(p) + ". It runs as you and starts when you log in. " +
		"A mirrormark serve you started yourself is running; stop it so the service can take over, then run:\n" +
		"  systemctl --user start mirrormark\n" +
		"  status: systemctl --user status mirrormark\n" +
		"  log:    " + p.LogFile() + "\n" +
		"  stop:   systemctl --user disable --now mirrormark\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestStartServerWithoutSystemd(t *testing.T) {
	p := testHome(t)
	fakeSystemctl(t, "daemon-reload")
	var out bytes.Buffer
	if err := startServer(p, "/bin/mirrormark", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Keep this running instead") || !strings.Contains(out.String(), "/bin/mirrormark serve") {
		t.Fatalf("output %q", out.String())
	}
}

func TestStopServer(t *testing.T) {
	p := testHome(t)
	calls := fakeSystemctl(t, "")
	var out bytes.Buffer
	startServer(p, "/bin/mirrormark", &out)
	*calls = nil
	if err := stopServer(p, []string{"Firefox Developer Edition"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || !strings.Contains(out.String(), "Kept the mirrormark service, which Firefox Developer Edition still uses.") {
		t.Fatalf("calls %q, output %q", *calls, out.String())
	}
	if err := stopServer(p, nil, &out); err != nil {
		t.Fatal(err)
	}
	if want := []string{"disable --now mirrormark.service", "daemon-reload"}; !slices.Equal(*calls, want) {
		t.Fatalf("calls %q", *calls)
	}
	if _, err := os.Stat(unitPath(p)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unit still there: %v", err)
	}
}

// systemctl can disable a unit without a running user manager, which then
// stopped nothing; uninstall must not say it did.
func TestStopServerWithoutAUserManager(t *testing.T) {
	p := testHome(t)
	var out bytes.Buffer
	fakeSystemctl(t, "")
	startServer(p, "/bin/mirrormark", &out)
	fakeSystemctl(t, "daemon-reload")
	out.Reset()
	if err := stopServer(p, nil, &out); err != nil {
		t.Fatal(err)
	}
	if want := "Removed " + unitPath(p) + ". If you run mirrormark serve yourself, stop it.\n"; out.String() != want {
		t.Fatalf("output %q, want %q", out.String(), want)
	}
}
