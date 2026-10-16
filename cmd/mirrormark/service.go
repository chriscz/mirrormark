package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
)

// runSystemctl runs systemctl --user; tests replace it.
var runSystemctl = func(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

const unitName = "mirrormark.service"

// unitPath is the user unit. systemd reads user units from
// $XDG_CONFIG_HOME/systemd/user, the directory p.Config sits in.
func unitPath(p db.Paths) string {
	return filepath.Join(filepath.Dir(p.Config), "systemd", "user", unitName)
}

// unitQuote writes s for a unit file: % and $ doubled so systemd does not
// expand them, and quoted if it holds a space, a quote or a backslash.
func unitQuote(s string) string {
	s = strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// unit is the user unit that runs self as the server. It sets no
// environment: systemd's user manager supplies it.
func unit(self string) []byte {
	return []byte("# Written by mirrormark install; mirrormark uninstall removes it.\n[Unit]\nDescription=mirrormark bookmark sync\n\n[Service]\n" +
		"ExecStart=" + unitQuote(self) + " serve\nRestart=on-failure\n\n[Install]\nWantedBy=default.target\n")
}

// startServer writes the user unit and has systemd (re)start the server,
// every time, so it runs the binary just installed; connected extensions
// reconnect at their next alarm. Without systemd it says how to run the
// server by hand.
func startServer(p db.Paths, self string, stdout io.Writer) error {
	if err := chrome.WriteFiles([]chrome.File{{Path: unitPath(p), Data: unit(self)}}); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := runSystemctl(args...); err != nil {
			fmt.Fprintf(stdout, "Could not run mirrormark as a systemd user service (%v).\nKeep this running instead, for example from your desktop's autostart:\n  %s serve\n", err, quoteArg(self))
			return nil
		}
	}
	return nil
}

// stopServer stops and removes the user unit unless users, the other
// browsers that still have mirrormark, need the server.
func stopServer(p db.Paths, users []string, stdout io.Writer) error {
	path := unitPath(p)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if len(users) > 0 {
		fmt.Fprintf(stdout, "Kept the mirrormark service, which %s still uses.\n", strings.Join(users, " and "))
		return nil
	}
	// Without a running user manager, systemctl may still disable the unit
	// offline but stops nothing; only a reload says a manager is there.
	stopped := runSystemctl("disable", "--now", unitName) == nil
	if err := chrome.RemoveFiles([]string{path}); err != nil {
		return err
	}
	if runSystemctl("daemon-reload") != nil {
		stopped = false
	}
	if stopped {
		fmt.Fprintln(stdout, "Stopped and removed the mirrormark service.")
	} else {
		fmt.Fprintf(stdout, "Removed %s. If you run mirrormark serve yourself, stop it.\n", path)
	}
	return nil
}

// serviceUsers names the browsers other than browser that still have
// mirrormark in place, so still need the server: another Chromium browser
// whose external file is there, or a Gecko browser with the XPI in a
// profile a database names. One that cannot be checked counts.
func serviceUsers(p db.Paths, browser string, stderr io.Writer) []string {
	var out []string
	if key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem")); err == nil {
		out = chromiumUsers(browser, chrome.ExtensionID(chrome.PublicKeyDER(key)), stderr)
	}
	dbs, errs := db.LoadAll(p)
	if len(errs) > 0 {
		return append(out, "a browser whose database cannot be read")
	}
	return append(out, geckoUsers(dbs, browser, stderr)...)
}
