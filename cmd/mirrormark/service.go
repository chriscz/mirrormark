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
	"slices"
	"strings"
	"time"

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
// reconnect at their next alarm. It says so, and how to look at the
// service and stop it. Without systemd it says how to run the server by
// hand.
func startServer(p db.Paths, self string, stdout io.Writer) error {
	if err := chrome.WriteFiles([]chrome.File{{Path: unitPath(p), Data: unit(self)}}); err != nil {
		return err
	}
	running := false
	if unlock, err := db.TryLock(p.ServeLock()); errors.Is(err, db.ErrLocked) {
		running = true
	} else if err == nil {
		unlock()
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := runSystemctl(args...); err != nil {
			fmt.Fprintf(stdout, "Could not run mirrormark as a systemd user service (%v).\nKeep this running instead, for example from your desktop's autostart:\n  %s serve\n", err, quoteArg(self))
			return nil
		}
	}
	started := "Started it now."
	if running {
		started = "It was running, so it was restarted now to run this binary; connected profiles reconnect within a minute."
	}
	fmt.Fprintf(stdout, "Set up mirrormark serve as a systemd user service, %s. It runs as you and starts when you log in. %s\n"+
		"  status: systemctl --user status mirrormark\n  log:    %s\n  stop:   systemctl --user disable --now mirrormark\n", unitPath(p), started, p.LogFile())
	return nil
}

// serverWait is how long install waits for a server systemd is starting.
var serverWait = 5 * time.Second

// serverRunning reports whether a server holds the serve lock. A server
// that starts during the brief test finds the lock taken, exits, and
// systemd starts it again.
func serverRunning(p db.Paths) bool {
	deadline := time.Now().Add(serverWait)
	for {
		unlock, err := db.TryLock(p.ServeLock())
		if errors.Is(err, db.ErrLocked) {
			return true
		}
		if err == nil {
			unlock()
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
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

// browserUsers names, by family, the browsers other than the one being
// uninstalled that still have mirrormark in place.
type browserUsers struct{ chromium, gecko []string }

// all is every browser that still needs the server.
func (u browserUsers) all() []string { return slices.Concat(u.chromium, u.gecko) }

// otherUsers names the browsers other than browser that still have
// mirrormark in place, so still need the shared files and the server:
// another Chromium browser whose external file is there, or a Gecko
// browser with the XPI in a profile a database names. One that cannot be
// checked counts. Uninstall calls it once, so each warning prints once.
func otherUsers(p db.Paths, browser string, stderr io.Writer) browserUsers {
	var u browserUsers
	dbs, errs := db.LoadAll(p)
	switch key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem")); {
	case err == nil:
		u.chromium = chromiumUsers(browser, chrome.ExtensionID(chrome.PublicKeyDER(key)), stderr)
	case errors.Is(err, fs.ErrNotExist):
		u.chromium = keylessChromiumUsers(p, dbs, browser, stderr)
	default:
		fmt.Fprintf(stderr, "mirrormark: warning: cannot tell whether a Chromium browser still uses mirrormark, so keeping the files it shares: %v\n", err)
		u.chromium = []string{"a Chromium browser it cannot check"}
	}
	u.gecko = geckoUsers(dbs, browser, stderr)
	if len(errs) > 0 {
		// Gecko uninstall stops at an unreadable database before it uses
		// this list; for the server, that database's browser counts.
		u.gecko = append(u.gecko, "a browser whose database cannot be read")
	}
	return u
}

// keylessChromiumUsers names the Chromium browsers other than except that
// may still use mirrormark when the key, and so the extension id, is gone:
// those with a database, or with an external file that names mirrormark's
// CRX. One whose files it cannot read counts.
func keylessChromiumUsers(p db.Paths, dbs map[string]*db.DB, except string, stderr io.Writer) []string {
	crx := filepath.Join(p.Data, "mirrormark.crx")
	var out []string
	for _, o := range chrome.Browsers {
		if o.Config == except {
			continue
		}
		used := false
		for _, d := range dbs {
			used = used || d.Profile.Browser == o.Config
		}
		if used {
			out = append(out, o.Name)
			continue
		}
		used, err := namesCRX(filepath.Join(sysroot, o.ExternalDir), crx)
		if err != nil {
			fmt.Fprintf(stderr, "mirrormark: warning: cannot tell whether %s still uses mirrormark, so keeping the files it shares: %v\n", o.Name, err)
		}
		if used || err != nil {
			out = append(out, o.Name)
		}
	}
	return out
}

// namesCRX reports whether an external extension file in dir points at crx.
func namesCRX(dir, crx string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		got, err := chrome.ExternalCRX(filepath.Join(dir, e.Name()))
		if err != nil {
			return false, err
		}
		if got == crx {
			return true, nil
		}
	}
	return false, nil
}
