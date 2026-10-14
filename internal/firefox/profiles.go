// Package firefox installs mirrormark into Firefox Developer Edition on
// Linux, one profile at a time and without root: it lists the profiles,
// writes the XPI and three user.js prefs into each, writes the per-user
// native host manifest, and binds each profile by opening bind.html in it.
//
// Release Firefox installs only add-ons Mozilla signed, so only Developer
// Edition, which honours xpinstall.signatures.required, is supported. That
// pref lets the profile take any unsigned add-on; uninstall restores it.
//
// Firefox picks the extension's moz-extension:// UUID per profile and keeps
// it in prefs.js. Setting it there would replace every add-on's UUID and
// lose their storage, so it is only read.
package firefox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chriscz/mirrormark/internal/chrome"
)

// DefaultRoot is the directory holding profiles.ini: the XDG location
// Firefox uses for new installs since 147, if it holds one, else the
// legacy ~/.mozilla/firefox.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cfg := filepath.Join(home, ".config")
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		cfg = v
	}
	if r := filepath.Join(cfg, "mozilla", "firefox"); fileExists(filepath.Join(r, "profiles.ini")) {
		return r, nil
	}
	return filepath.Join(home, ".mozilla", "firefox"), nil
}

// Profiles lists root's profiles, sorted by Dir: those in profiles.ini,
// with Dir as the file gives it (relative to root, or absolute), and every
// other directory in root that holds a prefs.js, named by what follows its
// first ".". Firefox's new profile switcher lists its profiles only in an
// SQLite file, which this does not read, but each one has a prefs.js.
func Profiles(root string) ([]chrome.Profile, error) {
	var out []chrome.Profile
	seen := map[string]bool{}
	if data, err := os.ReadFile(filepath.Join(root, "profiles.ini")); err == nil {
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
	}
	entries, _ := os.ReadDir(root)
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
		return nil, fmt.Errorf("no Firefox profiles in %s; start Firefox Developer Edition once and close it", root)
	}
	slices.SortFunc(out, func(a, b chrome.Profile) int { return strings.Compare(a.Dir, b.Dir) })
	return out, nil
}

// Dir is profile p's directory.
func Dir(root string, p chrome.Profile) string {
	if filepath.IsAbs(p.Dir) {
		return p.Dir
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
