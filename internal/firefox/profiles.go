// Package firefox installs mirrormark into Gecko browsers on Linux,
// Firefox Developer Edition and LibreWolf, one profile at a time and
// without root: it lists the profiles, writes the XPI and three user.js
// prefs into each, writes the per-user native host manifest they share,
// and binds each profile by opening bind.html in it.
//
// Release Firefox installs only add-ons Mozilla signed, so only browsers
// that honour xpinstall.signatures.required are supported. That pref lets
// the profile take any unsigned add-on; uninstall restores it.
//
// Firefox picks the extension's moz-extension:// UUID per profile and keeps
// it in prefs.js. Setting it there would replace every add-on's UUID and
// lose their storage, so it is only read.
package firefox

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chriscz/mirrormark/internal/chrome"
)

// Browser is one Gecko browser mirrormark installs into. They share the
// XPI, the user.js prefs and the per-user host manifest; each has its own
// profiles root and binary, and its own way to tell its profiles from
// other Gecko browsers'.
type Browser struct {
	Config   string // the --browser value, and browser: in a database
	Name     string // what messages call it
	XDGRoot  string // the profiles root under $XDG_CONFIG_HOME, if it holds profiles.ini
	HomeRoot string // the profiles root under the home directory otherwise
	Bin      string // its binary, in the directory compatibility.ini names
	Channel  string // the update channel its builds have, if it is told by that
	AppName  string // the Name in its application.ini, if it is told by that
}

// Firefox is Firefox Developer Edition: release Firefox installs only
// add-ons Mozilla signed, Developer Edition honours
// xpinstall.signatures.required.
var Firefox = Browser{Config: "firefox", Name: "Firefox Developer Edition", XDGRoot: "mozilla/firefox",
	HomeRoot: ".mozilla/firefox", Bin: "firefox", Channel: "aurora"}

// LibreWolf is built without MOZ_REQUIRE_SIGNING, so it honours
// xpinstall.signatures.required like Developer Edition. Its channel says
// release, so its application.ini tells it from release Firefox.
var LibreWolf = Browser{Config: "librewolf", Name: "LibreWolf", XDGRoot: "librewolf/librewolf",
	HomeRoot: ".librewolf", Bin: "librewolf", AppName: "LibreWolf"}

// Browsers are the Gecko browsers install knows.
var Browsers = []Browser{Firefox, LibreWolf}

// Lookup returns the browser whose Config is config.
func Lookup(config string) (Browser, bool) {
	i := slices.IndexFunc(Browsers, func(b Browser) bool { return b.Config == config })
	if i < 0 {
		return Browser{}, false
	}
	return Browsers[i], true
}

// DefaultRoot is the directory holding b's profiles.ini: the XDG location,
// if it holds one, else the legacy one in the home directory.
func (b Browser) DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cfg := filepath.Join(home, ".config")
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		cfg = v
	}
	if r := filepath.Join(cfg, b.XDGRoot); fileExists(filepath.Join(r, "profiles.ini")) {
		return r, nil
	}
	return filepath.Join(home, b.HomeRoot), nil
}

// Profiles lists root's profiles, sorted by ID, numbers as numbers: those
// in profiles.ini, with Dir as the file gives it (relative to root, or
// absolute), and every other directory in root that holds a prefs.js,
// named by what follows its first ".". Firefox's new profile switcher lists its profiles only in an
// SQLite file, which this does not read, but each one has a prefs.js.
func (b Browser) Profiles(root string) ([]chrome.Profile, error) {
	var out []chrome.Profile
	seen := map[string]bool{}
	// A missing file or root means no profiles yet; any other error is
	// reported, not taken for that.
	data, err := os.ReadFile(filepath.Join(root, "profiles.ini"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, s := range iniSections(string(data)) {
		if !strings.HasPrefix(s["[]"], "Profile") || s["Path"] == "" {
			continue
		}
		p := chrome.Profile{Dir: s["Path"], Name: s["Name"]}
		if s["IsRelative"] != "0" {
			p.Dir = filepath.FromSlash(p.Dir)
		}
		out = append(out, p)
		seen[Dir(root, p)] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if !e.IsDir() || seen[dir] || !fileExists(filepath.Join(dir, "prefs.js")) {
			continue
		}
		name := e.Name()
		if _, after, ok := strings.Cut(name, "."); ok {
			name = after
		}
		out = append(out, chrome.Profile{Dir: e.Name(), Name: name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no %s profiles in %s; start %s once and close it", b.Name, root, b.Name)
	}
	// Ties (two absolute paths with one base name) go by Dir, so the
	// numbers install prints stay the same from one run to the next.
	slices.SortFunc(out, func(a, b chrome.Profile) int {
		return cmp.Or(chrome.CompareIDs(a.ID(), b.ID()), strings.Compare(a.Dir, b.Dir))
	})
	return out, nil
}

// Dir is profile p's directory, cleaned so that an absolute path in
// profiles.ini matches the same directory found by the scan.
func Dir(root string, p chrome.Profile) string {
	if filepath.IsAbs(p.Dir) {
		return filepath.Clean(p.Dir)
	}
	return filepath.Join(root, p.Dir)
}

// iniSections parses an INI file into one map per section; the section's
// name is under "[]".
func iniSections(data string) []map[string]string {
	var out []map[string]string
	for line := range strings.Lines(data) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			out = append(out, map[string]string{"[]": line[1 : len(line)-1]})
		} else if k, v, ok := strings.Cut(line, "="); ok && len(out) > 0 {
			out[len(out)-1][k] = v
		}
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
