// Package chrome installs mirrormark into Chromium-based browsers on Linux,
// Google Chrome first: it lists their profiles, keeps the key that fixes the extension's id, packs the
// embedded extension as a signed CRX, writes the system files they read, and binds profiles.
//
// Branded Chrome on Linux installs a CRX named by a JSON file in its
// external extensions directory, without a prompt, in every profile, at its
// next start. It ignores --load-extension, and no per-user location works,
// so the two system files need root. The host manifest in the system
// directory reaches every Chrome data directory.
//
// Neither the extension nor the host can tell which profile they run in, so
// install binds each profile by opening bind.html#<database id>.<token> in it from
// the command line, and waits for the extension to report in.
package chrome

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Profile is one Chrome profile: its directory inside the data directory,
// such as "Profile 1", and the name Chrome shows for it.
type Profile struct{ Dir, Name string }

// ID is how install names a profile: its directory's name, such as
// "Profile 1" or a Gecko profile's "x1y2.default-release".
func (p Profile) ID() string { return filepath.Base(p.Dir) }

// CompareIDs orders profile IDs as people count: runs of digits compare as
// numbers, so "Profile 2" comes before "Profile 10"; everything else
// compares bytewise.
func CompareIDs(a, b string) int {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			// Equal numbers: the one with more leading zeros sorts after,
			// which keeps the order total.
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb), cmp.Compare(da, db)); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

// digits is the length of the run of ASCII digits s starts with.
func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// Browser is one Chromium-based browser mirrormark installs into. They
// share the key, the CRX, the extension id and the system host manifest;
// each has its own data directory, command and external extensions
// directory.
type Browser struct {
	Config         string // the --browser value, and browser: in a database
	Name           string // what messages call it
	DataDir        string // its data directory under $XDG_CONFIG_HOME or ~/.config
	Bin            string // the command that starts it
	ExternalDir    string // where it reads external extension files at start
	ExtensionsPage string // where the user turns extensions on and off
}

// Chrome is Google Chrome. On Linux it installs a CRX named by a file in
// its external extensions directory, without a prompt, in every profile.
var Chrome = Browser{Config: "chrome", Name: "Chrome", DataDir: "google-chrome", Bin: "google-chrome",
	ExternalDir: "/usr/share/google-chrome/extensions", ExtensionsPage: "chrome://extensions"}

// Brave is Brave stable from Brave's package. It reads external extension
// files from its own install directory, and native host manifests from
// Chrome's system directory, so it shares Chrome's host manifest.
var Brave = Browser{Config: "brave", Name: "Brave", DataDir: "BraveSoftware/Brave-Browser", Bin: "brave-browser",
	ExternalDir: "/opt/brave.com/brave/extensions", ExtensionsPage: "brave://extensions"}

// Browsers are the Chromium-based browsers install knows.
var Browsers = []Browser{Chrome, Brave}

// Lookup returns the browser whose Config is config.
func Lookup(config string) (Browser, bool) {
	i := slices.IndexFunc(Browsers, func(b Browser) bool { return b.Config == config })
	if i < 0 {
		return Browser{}, false
	}
	return Browsers[i], true
}

// InstallCommand is the command that installs into b, as messages tell
// the user to run it; Chrome is install's default.
func (b Browser) InstallCommand() string {
	if b.Config == Chrome.Config {
		return "mirrormark install"
	}
	return "mirrormark install --browser " + b.Config
}

// DefaultUserDataDir is b's default data directory on Linux.
func (b Browser) DefaultUserDataDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		return filepath.Join(v, b.DataDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", b.DataDir), nil
}

// Profiles lists the profiles in b's "Local State" file, sorted by ID,
// numbers as numbers. The file is not a documented interface; it has held
// profile.info_cache for years.
func (b Browser) Profiles(userDataDir string) ([]Profile, error) {
	data, err := os.ReadFile(filepath.Join(userDataDir, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s's profile list (has %s run with %s?): %w", b.Name, b.Name, userDataDir, err)
	}
	var ls struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(userDataDir, "Local State"), err)
	}
	var out []Profile
	for dir, info := range ls.Profile.InfoCache {
		out = append(out, Profile{Dir: dir, Name: info.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no profiles in %s; start %s once and close it", b.Name, userDataDir, b.Name)
	}
	slices.SortFunc(out, func(a, b Profile) int { return cmp.Or(CompareIDs(a.ID(), b.ID()), strings.Compare(a.Dir, b.Dir)) })
	return out, nil
}
