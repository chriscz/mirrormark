// Package chrome installs mirrormark into Google Chrome on Linux: it lists
// Chrome's profiles, keeps the key that fixes the extension's id, packs the
// embedded extension as a signed CRX, writes the two system files Chrome
// reads, and binds profiles.
//
// Branded Chrome on Linux installs a CRX named by a JSON file in its
// external extensions directory, without a prompt, in every profile, at its
// next start. It ignores --load-extension, and no per-user location works,
// so the two system files need root. The host manifest in the system
// directory reaches every Chrome data directory.
//
// Neither the extension nor the host can tell which profile they run in, so
// install binds each profile by opening bind.html#<database id> in it from
// the command line, and confirms the binding through the host's hello.
package chrome

import (
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

// DefaultUserDataDir is Chrome's default data directory on Linux.
func DefaultUserDataDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		return filepath.Join(v, "google-chrome"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "google-chrome"), nil
}

// Profiles lists the profiles in Chrome's "Local State" file, sorted by
// directory. The file is not a documented interface; it has held
// profile.info_cache for years.
func Profiles(userDataDir string) ([]Profile, error) {
	data, err := os.ReadFile(filepath.Join(userDataDir, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("cannot read Chrome's profile list (has Chrome run with %s?): %w", userDataDir, err)
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
		return nil, fmt.Errorf("Chrome lists no profiles in %s; start Chrome once and close it", userDataDir)
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(a.Dir, b.Dir) })
	return out, nil
}
