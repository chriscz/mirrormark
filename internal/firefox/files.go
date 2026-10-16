package firefox

import (
	"archive/zip"
	"bytes"
	"cmp"
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

// LegacyHostManifest is where builds before the server put the native host
// manifest Firefox and LibreWolf read; uninstall removes it when it names
// the gecko id.
func LegacyHostManifest() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mozilla", "native-messaging-hosts", "com.github.chriscz.mirrormark.json"), nil
}

// Files are the files install writes: for each profile the XPI and its
// user.js with the prefs set (see withPrefs). The user's own lines stay as
// they are.
func Files(profileDirs []string) ([]chrome.File, error) {
	xpi, err := XPI()
	if err != nil {
		return nil, err
	}
	var files []chrome.File
	for _, dir := range profileDirs {
		user, err := os.ReadFile(filepath.Join(dir, "user.js"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		files = append(files, chrome.File{Path: XPIPath(dir), Data: xpi}, chrome.File{Path: filepath.Join(dir, "user.js"), Data: withPrefs(user)})
	}
	return files, nil
}

// withPrefs returns user.js with each line uninstall wrote (a value of
// ResetPrefs) turned back into its pref where it is, so installing again
// adds no lines, then every pref still missing appended. Changing a line
// in place relies on no later line setting the same pref: Firefox applies
// the last one.
func withPrefs(user []byte) []byte {
	var b strings.Builder
	have := map[string]bool{}
	for line := range strings.Lines(string(user)) {
		l := strings.TrimSpace(line)
		for p, r := range ResetPrefs {
			if l == r {
				l, line = p, p+"\n"
			}
		}
		have[l] = true
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		b.WriteString(line)
	}
	for _, p := range Prefs {
		if !have[p] {
			b.WriteString(p + "\n")
		}
	}
	return []byte(b.String())
}

var channelRe = regexp.MustCompile(`pref\("app\.update\.channel",\s*"([^"]+)"\)`)

// ErrNeverRun means no browser has run the profile yet, so nothing in it
// says which browser it belongs to. LibreWolf makes a second profile at its
// first start that never runs.
var ErrNeverRun = errors.New("it has never run")

// Check returns the binary of the browser that last ran the profile, from
// its compatibility.ini, or why b cannot install into the profile: it was
// last run by another browser, or nothing tells which. b's Channel and
// AppName, whichever are set, must both match; with neither set, nothing
// would tell b's profiles from another browser's, so it refuses them all.
func (b Browser) Check(profileDir string) (bin string, err error) {
	if b.Channel == "" && b.AppName == "" {
		return "", fmt.Errorf("nothing tells %s's profiles from other browsers'", cmp.Or(b.Name, "this browser"))
	}
	data, err := os.ReadFile(filepath.Join(profileDir, "compatibility.ini"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w; start %s in it once and close it", ErrNeverRun, b.Name)
	}
	if err != nil {
		return "", fmt.Errorf("cannot tell which browser uses %s: %w", profileDir, err)
	}
	var dir string
	for _, s := range iniSections(string(data)) {
		if s["[]"] == "Compatibility" {
			dir = s["LastPlatformDir"]
		}
	}
	if dir == "" {
		return "", fmt.Errorf("%s names no browser (LastPlatformDir)", filepath.Join(profileDir, "compatibility.ini"))
	}
	if b.Channel != "" {
		prefs, err := os.ReadFile(filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
		if err != nil {
			return "", fmt.Errorf("cannot read the update channel of the browser in %s: %w", dir, err)
		}
		m := channelRe.FindSubmatch(prefs)
		if m == nil {
			return "", fmt.Errorf("no update channel in %s", filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
		}
		if string(m[1]) != b.Channel {
			return "", b.channelRefusal(dir, string(m[1]))
		}
	}
	if b.AppName != "" {
		name, err := appName(dir)
		if err != nil {
			return "", fmt.Errorf("cannot tell which browser is in %s: %w", dir, err)
		}
		if name != b.AppName {
			return "", fmt.Errorf("it was last run by %s from %s, not %s. mirrormark works in %s", cmp.Or(name, "a browser that does not name itself"), dir, b.Name, supported())
		}
	}
	return filepath.Join(dir, b.Bin), nil
}

// channelRefusal says why b refuses a profile last run from dir, by a
// browser on another update channel: which browser that is, if
// application.ini names one install knows, so the user can ask for it.
// Only Mozilla's Firefox is said to need add-ons Mozilla signed; other
// Gecko browsers decide that for themselves.
func (b Browser) channelRefusal(dir, channel string) error {
	name, _ := appName(dir) // a profile it cannot name is still refused
	for _, o := range Browsers {
		if o.AppName != "" && o.AppName == name && o.Config != b.Config {
			return fmt.Errorf("it was last run by %s from %s, not %s: this profile belongs to %s; use --browser %s", name, dir, b.Name, o.Name, o.Config)
		}
	}
	if name == "Firefox" {
		return fmt.Errorf("it was last run by Firefox on the %s channel from %s, not %s; Firefox on that channel installs only add-ons Mozilla signed. mirrormark works in %s", channel, dir, b.Name, supported())
	}
	return fmt.Errorf("it was last run by %s on the %s channel from %s, not %s. mirrormark works in %s", cmp.Or(name, "a browser"), channel, dir, b.Name, supported())
}

// appName is the Name in the application.ini of the browser in dir.
func appName(dir string) (string, error) {
	ini, err := os.ReadFile(filepath.Join(dir, "application.ini"))
	if err != nil {
		return "", err
	}
	var name string
	for _, s := range iniSections(string(ini)) {
		if s["[]"] == "App" {
			name = s["Name"]
		}
	}
	return name, nil
}

// supported says which browsers install takes, and with which --browser.
func supported() string {
	var s []string
	for _, b := range Browsers {
		s = append(s, b.Name+" with --browser "+b.Config)
	}
	return strings.Join(s, ", and in ")
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
