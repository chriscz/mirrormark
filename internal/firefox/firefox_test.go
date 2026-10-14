package firefox

import (
	"archive/zip"
	"bytes"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
)

func TestProfilesFromIniAndScan(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(t.TempDir(), "elsewhere")
	os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(
		"[General]\nStartWithLastProfile=1\nVersion=2\n\n"+
			"[Profile1]\nName=work\nIsRelative=0\nPath="+abs+"\n\n"+
			"[Profile0]\nName=dev-edition-default\nIsRelative=1\nPath=ab12.dev-edition-default\nDefault=1\n\n"+
			"[Install4F96D1932A9F858E]\nDefault=ab12.dev-edition-default\n"), 0o644)
	for _, d := range []string{"ab12.dev-edition-default", "cd34.Original profile", "no-prefs"} {
		os.MkdirAll(filepath.Join(root, d), 0o700)
	}
	os.WriteFile(filepath.Join(root, "ab12.dev-edition-default", "prefs.js"), nil, 0o600)
	os.WriteFile(filepath.Join(root, "cd34.Original profile", "prefs.js"), nil, 0o600)
	got, err := Profiles(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []chrome.Profile{
		{Dir: abs, Name: "work"},
		{Dir: "ab12.dev-edition-default", Name: "dev-edition-default"},
		{Dir: "cd34.Original profile", Name: "Original profile"},
	}
	slices.SortFunc(want, func(a, b chrome.Profile) int { return strings.Compare(a.Dir, b.Dir) })
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if d := Dir(root, chrome.Profile{Dir: abs}); d != abs {
		t.Fatalf("absolute Dir %s", d)
	}
	if d := Dir(root, chrome.Profile{Dir: "ab12.dev-edition-default"}); d != filepath.Join(root, "ab12.dev-edition-default") {
		t.Fatalf("relative Dir %s", d)
	}
}

func TestProfilesWithNoneFails(t *testing.T) {
	if _, err := Profiles(t.TempDir()); err == nil || !strings.Contains(err.Error(), "start Firefox") {
		t.Fatalf("err %v", err)
	}
}

func TestDefaultRootPrefersTheNewLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if r, _ := DefaultRoot(); r != filepath.Join(home, ".mozilla", "firefox") {
		t.Fatalf("without profiles.ini: %s", r)
	}
	newRoot := filepath.Join(home, ".config", "mozilla", "firefox")
	os.MkdirAll(newRoot, 0o700)
	os.WriteFile(filepath.Join(newRoot, "profiles.ini"), nil, 0o644)
	if r, _ := DefaultRoot(); r != newRoot {
		t.Fatalf("with profiles.ini: %s", r)
	}
}

func TestXPIHoldsTheExtensionWithoutAKey(t *testing.T) {
	data, err := XPI()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := XPI()
	if !bytes.Equal(data, again) {
		t.Fatal("two builds differ, so install would always ask for a restart")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"background.js", "bind.html", "bind.js", "manifest.json", "popup.html", "popup.js"}) {
		t.Fatalf("files %v", names)
	}
	m, _ := z.Open("manifest.json")
	raw, _ := io.ReadAll(m)
	if bytes.Contains(raw, []byte(`"key"`)) || !bytes.Contains(raw, []byte(extension.GeckoID)) {
		t.Fatalf("manifest %s", raw)
	}
}

func TestFilesAppendOnlyMissingPrefs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "user.js"), []byte("user_pref(\"browser.startup.page\", 3);\n"+Prefs[1]+"\n"), 0o644)
	files, err := Files([]string{dir}, "/usr/local/bin/mirrormark")
	if err != nil {
		t.Fatal(err)
	}
	host, _ := HostManifestPath()
	if len(files) != 3 || files[0].Path != host || files[1].Path != XPIPath(dir) || files[2].Path != filepath.Join(dir, "user.js") {
		t.Fatalf("paths %v", files)
	}
	if !bytes.Contains(files[0].Data, []byte(`"allowed_extensions": [`+"\n    \""+extension.GeckoID+`"`)) ||
		!bytes.Contains(files[0].Data, []byte(`"path": "/usr/local/bin/mirrormark"`)) {
		t.Fatalf("host manifest %s", files[0].Data)
	}
	want := "user_pref(\"browser.startup.page\", 3);\n" + Prefs[1] + "\n" + Prefs[0] + "\n" + Prefs[2] + "\n"
	if string(files[2].Data) != want {
		t.Fatalf("user.js %q", files[2].Data)
	}
	if !strings.HasSuffix(host, "/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json") {
		t.Fatal(host)
	}
}

func TestPrefsAndTheirResets(t *testing.T) {
	want := []string{
		`user_pref("xpinstall.signatures.required", false);`,
		`user_pref("extensions.autoDisableScopes", 14);`,
		`user_pref("extensions.startupScanScopes", 1);`,
	}
	if !slices.Equal(Prefs, want) {
		t.Fatalf("Prefs %q", Prefs)
	}
	resets := map[string]string{
		`user_pref("xpinstall.signatures.required", false);`: `user_pref("xpinstall.signatures.required", true);`,
		`user_pref("extensions.autoDisableScopes", 14);`:     `user_pref("extensions.autoDisableScopes", 15);`,
	}
	if !maps.Equal(ResetPrefs, resets) {
		t.Fatalf("ResetPrefs %q", ResetPrefs)
	}
}

func TestChannelFromTheProfilesFirefox(t *testing.T) {
	ff := t.TempDir()
	os.MkdirAll(filepath.Join(ff, "defaults", "pref"), 0o755)
	os.WriteFile(filepath.Join(ff, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "aurora");`+"\n"), 0o644)
	dir := t.TempDir()
	if _, _, err := Channel(dir); err == nil || !strings.Contains(err.Error(), "start Firefox") {
		t.Fatalf("no compatibility.ini: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte("[Compatibility]\nLastVersion=159.0_20261001000000/20261001000000\nLastPlatformDir="+ff+"\nLastAppDir="+ff+"/browser\n"), 0o644)
	bin, ch, err := Channel(dir)
	if err != nil || bin != filepath.Join(ff, "firefox") || ch != "aurora" {
		t.Fatalf("%s %s %v", bin, ch, err)
	}
}

func TestUUIDFromPrefs(t *testing.T) {
	dir := t.TempDir()
	if u, err := UUID(dir); u != "" || err != nil {
		t.Fatalf("no prefs.js: %q %v", u, err)
	}
	os.WriteFile(filepath.Join(dir, "prefs.js"), []byte(
		"user_pref(\"browser.startup.page\", 3);\n"+
			`user_pref("extensions.webextensions.uuids", "{\"other@example.com\":\"0f0f0f0f-1111-4222-8333-444444444444\",\"mirrormark@chriscz.github.io\":\"5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f40\"}");`+"\n"), 0o600)
	if u, err := UUID(dir); u != "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f40" || err != nil {
		t.Fatalf("%q %v", u, err)
	}
}
