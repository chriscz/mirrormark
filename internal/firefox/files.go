package firefox

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
)

// Prefs are the user.js lines that let Firefox Developer Edition install
// the unsigned XPI from the profile's extensions folder, enabled and
// without a prompt, at its next start. autoDisableScopes 14 clears only
// the profile bit of the default 15, so add-ons sideloaded into the user,
// application or system scopes still start disabled. The last makes a
// profile that has run before look in that folder at all.
var Prefs = []string{
	`user_pref("xpinstall.signatures.required", false);`,
	`user_pref("extensions.autoDisableScopes", 14);`,
	`user_pref("extensions.startupScanScopes", 1);`,
}

// ResetPrefs maps the two lines that weaken the profile's add-on checks to
// lines that set their defaults again, for uninstall. A line in user.js
// alone would not do: Firefox keeps the value in prefs.js once set.
var ResetPrefs = map[string]string{
	Prefs[0]: `user_pref("xpinstall.signatures.required", true);`,
	Prefs[1]: `user_pref("extensions.autoDisableScopes", 15);`,
}

// zipTime is every XPI entry's time, so the same files give the same bytes
// and install can tell an XPI is current.
var zipTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// XPI zips the embedded extension as it is: Firefox ignores Chrome's
// service_worker, and the XPI needs no key.
func XPI() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := fs.WalkDir(extension.Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := extension.Files.ReadFile(name)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	err = zw.Close()
	return buf.Bytes(), err
}

// XPIPath is where Firefox looks for the add-on in one profile.
func XPIPath(profileDir string) string {
	return filepath.Join(profileDir, "extensions", extension.GeckoID+".xpi")
}

// HostManifestPath is Firefox's per-user native host manifest. Firefox
// reads only ~/.mozilla/native-messaging-hosts, even when its profiles live
// under ~/.config/mozilla.
func HostManifestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mozilla", "native-messaging-hosts", extension.HostName+".json"), nil
}

type hostManifestJSON struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Path              string   `json:"path"`
	Type              string   `json:"type"`
	AllowedExtensions []string `json:"allowed_extensions"`
}

// Files are the files install writes: the host manifest letting the add-on
// start bin, then for each profile the XPI and its user.js with every line
// of Prefs it lacks appended. The user's own lines stay as they are.
func Files(profileDirs []string, bin string) ([]chrome.File, error) {
	host, err := HostManifestPath()
	if err != nil {
		return nil, err
	}
	m, _ := json.MarshalIndent(hostManifestJSON{Name: extension.HostName, Description: "mirrormark bookmark sync",
		Path: bin, Type: "stdio", AllowedExtensions: []string{extension.GeckoID}}, "", "  ") // cannot fail
	files := []chrome.File{{Path: host, Data: append(m, '\n')}}
	xpi, err := XPI()
	if err != nil {
		return nil, err
	}
	for _, dir := range profileDirs {
		user, err := os.ReadFile(filepath.Join(dir, "user.js"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if len(user) > 0 && user[len(user)-1] != '\n' {
			user = append(user, '\n')
		}
		for _, p := range Prefs {
			if !hasLine(user, p) {
				user = append(user, p+"\n"...)
			}
		}
		files = append(files, chrome.File{Path: XPIPath(dir), Data: xpi}, chrome.File{Path: filepath.Join(dir, "user.js"), Data: user})
	}
	return files, nil
}

func hasLine(data []byte, line string) bool {
	for l := range strings.Lines(string(data)) {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

var channelRe = regexp.MustCompile(`pref\("app\.update\.channel",\s*"([^"]+)"\)`)

// Channel returns the Firefox binary that last ran the profile, from its
// compatibility.ini, and that Firefox's update channel: "aurora" for
// Developer Edition, "release" for release.
func Channel(profileDir string) (bin, channel string, err error) {
	data, err := os.ReadFile(filepath.Join(profileDir, "compatibility.ini"))
	if err != nil {
		return "", "", fmt.Errorf("cannot tell which Firefox uses %s; start Firefox in that profile once and close it: %w", profileDir, err)
	}
	var dir string
	for _, s := range iniSections(string(data)) {
		if s["[]"] == "Compatibility" {
			dir = s["LastPlatformDir"]
		}
	}
	if dir == "" {
		return "", "", fmt.Errorf("%s names no Firefox (LastPlatformDir)", filepath.Join(profileDir, "compatibility.ini"))
	}
	prefs, err := os.ReadFile(filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
	if err != nil {
		return "", "", fmt.Errorf("cannot read the update channel of the Firefox in %s: %w", dir, err)
	}
	m := channelRe.FindSubmatch(prefs)
	if m == nil {
		return "", "", fmt.Errorf("no update channel in %s", filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
	}
	return filepath.Join(dir, "firefox"), string(m[1]), nil
}

var uuidsRe = regexp.MustCompile(`user_pref\("extensions\.webextensions\.uuids",\s*("(?:[^"\\]|\\.)*")\);`)

// UUID returns the add-on's moz-extension:// host in the profile, or ""
// if Firefox has not installed it yet. Firefox writes prefs.js a few
// seconds after the start that installs the add-on.
func UUID(profileDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, "prefs.js"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	m := uuidsRe.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	// The pref is a JavaScript string holding JSON; for what Firefox
	// escapes in it, a JSON string literal reads the same.
	var s string
	var ids map[string]string
	if err := json.Unmarshal(m[1], &s); err != nil {
		return "", fmt.Errorf("prefs.js: extensions.webextensions.uuids: %w", err)
	}
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return "", fmt.Errorf("prefs.js: extensions.webextensions.uuids: %w", err)
	}
	return ids[extension.GeckoID], nil
}
