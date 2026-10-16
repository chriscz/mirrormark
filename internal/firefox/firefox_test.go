package firefox

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	got, err := Firefox.Profiles(root)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by ID, the directory's base name, wherever the directory is.
	want := []chrome.Profile{
		{Dir: "ab12.dev-edition-default", Name: "dev-edition-default"},
		{Dir: "cd34.Original profile", Name: "Original profile"},
		{Dir: abs, Name: "work"},
	}
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
	if _, err := Firefox.Profiles(t.TempDir()); err == nil || !strings.Contains(err.Error(), "start Firefox") {
		t.Fatalf("err %v", err)
	}
}

func TestProfilesListsAnAbsoluteIniPathOnce(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ab12.default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prefs.js"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ini := "[Profile0]\nName=default\nIsRelative=0\nPath=" + root + "/./ab12.default/\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Firefox.Profiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "default" || Dir(root, got[0]) != dir {
		t.Fatalf("got %+v", got)
	}
	if id := got[0].ID(); id != "ab12.default" {
		t.Fatalf("ID %q of an absolute Path, want its base name", id)
	}
}

func TestProfilesReportsAnUnreadableIni(t *testing.T) {
	root := t.TempDir()
	ini := filepath.Join(root, "profiles.ini")
	if err := os.WriteFile(ini, []byte("[Profile0]\nName=a\nPath=a\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := Firefox.Profiles(root); err == nil || !strings.Contains(err.Error(), ini) {
		t.Fatalf("err %v", err)
	}
}

func TestProfilesReportsAnUnreadableRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte("[Profile0]\nName=a\nPath=a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Searchable but not listable: profiles.ini reads, the scan does not.
	if err := os.Chmod(root, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o700) })
	if _, err := Firefox.Profiles(root); err == nil || !strings.Contains(err.Error(), root) {
		t.Fatalf("err %v", err)
	}
}

func TestDefaultRootPrefersTheNewLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if r, _ := Firefox.DefaultRoot(); r != filepath.Join(home, ".mozilla", "firefox") {
		t.Fatalf("without profiles.ini: %s", r)
	}
	newRoot := filepath.Join(home, ".config", "mozilla", "firefox")
	os.MkdirAll(newRoot, 0o700)
	os.WriteFile(filepath.Join(newRoot, "profiles.ini"), nil, 0o644)
	if r, _ := Firefox.DefaultRoot(); r != newRoot {
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
	if !slices.Equal(names, []string{"background.js", "bind.html", "bind.js", "manifest.json", "popup.html", "popup.js", "ports.json"}) {
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
	files, err := Files([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != XPIPath(dir) || files[1].Path != filepath.Join(dir, "user.js") {
		t.Fatalf("paths %v", files)
	}
	want := "user_pref(\"browser.startup.page\", 3);\n" + Prefs[1] + "\n" + Prefs[0] + "\n" + Prefs[2] + "\n"
	if string(files[1].Data) != want {
		t.Fatalf("user.js %q", files[1].Data)
	}
	host, err := LegacyHostManifest()
	if err != nil || host != filepath.Join(os.Getenv("HOME"), ".mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json") {
		t.Fatal(host, err)
	}
}

// After an uninstall, user.js holds the reset lines; install puts each pref
// back where its reset line is, so reinstalling adds no lines.
func TestFilesReplaceResetLinesInPlace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte(ResetPrefs[Prefs[0]]+"\nuser_pref(\"browser.startup.page\", 3);\n"+ResetPrefs[Prefs[1]]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := Files([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	want := Prefs[0] + "\nuser_pref(\"browser.startup.page\", 3);\n" + Prefs[1] + "\n" + Prefs[2] + "\n"
	if string(files[1].Data) != want {
		t.Fatalf("user.js %q", files[1].Data)
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

func TestCheckTakesOnlyDeveloperEdition(t *testing.T) {
	ff := t.TempDir()
	os.MkdirAll(filepath.Join(ff, "defaults", "pref"), 0o755)
	prefs := filepath.Join(ff, "defaults", "pref", "channel-prefs.js")
	os.WriteFile(prefs, []byte(`pref("app.update.channel", "aurora");`+"\n"), 0o644)
	dir := t.TempDir()
	if _, err := Firefox.Check(dir); err == nil || !strings.Contains(err.Error(), "start Firefox") {
		t.Fatalf("no compatibility.ini: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte("[Compatibility]\nLastVersion=159.0_20261001000000/20261001000000\nLastPlatformDir="+ff+"\nLastAppDir="+ff+"/browser\n"), 0o644)
	if bin, err := Firefox.Check(dir); err != nil || bin != filepath.Join(ff, "firefox") {
		t.Fatalf("%s %v", bin, err)
	}
	os.WriteFile(prefs, []byte(`pref("app.update.channel", "release");`+"\n"), 0o644)
	if _, err := Firefox.Check(dir); err == nil || !strings.Contains(err.Error(), "release") {
		t.Fatalf("release: %v", err)
	}
}

// Each file Check reads that is missing, unreadable or says nothing makes
// it refuse, never take the profile: only a profile known to be Developer
// Edition's may have its add-on checks turned off.
func TestCheckRefusesWhatItCannotRead(t *testing.T) {
	compat := func(body string) func(t *testing.T, ff, dir string) {
		return func(t *testing.T, ff, dir string) {
			must(t, os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte(body), 0o644))
		}
	}
	unreadable := func(path func(ff, dir string) string) func(t *testing.T, ff, dir string) {
		return func(t *testing.T, ff, dir string) {
			p := path(ff, dir)
			must(t, os.Remove(p))
			must(t, os.Mkdir(p, 0o755)) // reading it fails, even as root
		}
	}
	prefsOf := func(ff, _ string) string { return filepath.Join(ff, "defaults", "pref", "channel-prefs.js") }
	for _, c := range []struct {
		name  string
		spoil func(t *testing.T, ff, dir string)
		want  string
	}{
		{"compatibility.ini without LastPlatformDir", compat("[Compatibility]\nLastVersion=159.0\n"), "names no browser (LastPlatformDir)"},
		{"LastPlatformDir outside [Compatibility]", compat("[Other]\nLastPlatformDir=/x\n"), "names no browser (LastPlatformDir)"},
		{"unreadable compatibility.ini", unreadable(func(_, dir string) string { return filepath.Join(dir, "compatibility.ini") }), "cannot tell which browser uses"},
		{"missing channel-prefs.js", func(t *testing.T, ff, dir string) { must(t, os.Remove(prefsOf(ff, dir))) }, "cannot read the update channel"},
		{"unreadable channel-prefs.js", unreadable(prefsOf), "cannot read the update channel"},
		{"channel-prefs.js without a channel", func(t *testing.T, ff, dir string) {
			must(t, os.WriteFile(prefsOf(ff, dir), []byte("// nothing\n"), 0o644))
		}, "no update channel"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ff, dir := geckoInstall(t, "Firefox", "aurora")
			if _, err := Firefox.Check(dir); err != nil {
				t.Fatalf("before: %v", err)
			}
			c.spoil(t, ff, dir)
			if bin, err := Firefox.Check(dir); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %q, %v; want a refusal saying %q", bin, err, c.want)
			}
		})
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

const testUUID = "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f40"

// fakeFirefox logs each call, and every bind URL touches $dir/bound.
func fakeFirefox(t *testing.T, profileDir string) (bin, log string) {
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "firefox"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"case \"$*\" in *bind.html*) touch \"" + dir + "/bound\";; esac\n"
	os.WriteFile(bin, []byte(script), 0o755)
	old := chrome.BindWait
	chrome.BindWait = 300 * time.Millisecond
	t.Cleanup(func() { chrome.BindWait = old })
	return bin, log
}

func boundFile(bin string) func(time.Time) bool {
	return func(time.Time) bool { _, err := os.Stat(filepath.Join(filepath.Dir(bin), "bound")); return err == nil }
}

// tok is a bind token for the tests.
const tok = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestBindOpensThePageOnceWithTheUUID(t *testing.T) {
	dir := t.TempDir()
	bin, log := fakeFirefox(t, dir)
	if err := Firefox.Bind(bin, dir, testUUID, "firefox-work", tok, boundFile(bin)); err != nil {
		t.Fatal(err)
	}
	if calls, _ := os.ReadFile(log); string(calls) != "--profile "+dir+" about:blank moz-extension://"+testUUID+"/bind.html#firefox-work."+tok+"\n" {
		t.Fatalf("calls:\n%s", calls)
	}
}

func TestBindSaysWhenTheAddonDoesNotReportIn(t *testing.T) {
	dir := t.TempDir()
	bin, log := fakeFirefox(t, dir)
	err := LibreWolf.Bind(bin, dir, testUUID, "librewolf-work", tok, func(time.Time) bool { return false })
	calls, _ := os.ReadFile(log)
	if err == nil || !strings.Contains(err.Error(), "look at the tab LibreWolf opened") || bytes.Count(calls, []byte("bind.html")) != 1 {
		t.Fatalf("err %v, calls:\n%s", err, calls)
	}
}

func TestCheckSaysWhenAProfileHasNeverRun(t *testing.T) {
	_, err := Firefox.Check(t.TempDir())
	if !errors.Is(err, ErrNeverRun) || !strings.Contains(err.Error(), "start Firefox Developer Edition") {
		t.Fatalf("err %v", err)
	}
}

func TestLibreWolfDefaultRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if r, _ := LibreWolf.DefaultRoot(); r != filepath.Join(home, ".librewolf") {
		t.Fatalf("without profiles.ini: %s", r)
	}
	xdg := filepath.Join(home, "xdg", "librewolf", "librewolf")
	must(t, os.MkdirAll(xdg, 0o700))
	must(t, os.WriteFile(filepath.Join(xdg, "profiles.ini"), nil, 0o644))
	if r, _ := LibreWolf.DefaultRoot(); r != xdg {
		t.Fatalf("with profiles.ini: %s", r)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// geckoInstall makes a browser's install directory whose application.ini
// names app (none if app is "") and whose channel-prefs.js says channel,
// and a profile last run from it.
func geckoInstall(t *testing.T, app, channel string) (dir, profile string) {
	t.Helper()
	dir = t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "defaults", "pref"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "`+channel+`");`+"\n"), 0o644))
	if app != "" {
		must(t, os.WriteFile(filepath.Join(dir, "application.ini"), []byte("[App]\nVendor=x\nName="+app+"\n"), 0o644))
	}
	profile = t.TempDir()
	must(t, os.WriteFile(filepath.Join(profile, "compatibility.ini"), []byte("[Compatibility]\nLastPlatformDir="+dir+"\n"), 0o644))
	return dir, profile
}

// LibreWolf's channel says release, so its application.ini tells it apart.
func TestCheckTellsLibreWolfFromFirefox(t *testing.T) {
	_, lw := geckoInstall(t, "LibreWolf", "release")
	_, release := geckoInstall(t, "Firefox", "release")
	_, dev := geckoInstall(t, "Firefox", "aurora")
	if bin, err := LibreWolf.Check(lw); err != nil || filepath.Base(bin) != "librewolf" {
		t.Fatalf("LibreWolf: %s %v", bin, err)
	}
	for name, profile := range map[string]string{"release Firefox": release, "Developer Edition": dev} {
		if _, err := LibreWolf.Check(profile); err == nil || !strings.Contains(err.Error(), "not LibreWolf") {
			t.Fatalf("%s as LibreWolf: %v", name, err)
		}
	}
	if _, err := Firefox.Check(lw); err == nil || !strings.Contains(err.Error(), "this profile belongs to LibreWolf; use --browser librewolf") || strings.Contains(err.Error(), "Mozilla signed") {
		t.Fatalf("LibreWolf as Firefox: %v", err)
	}
	if _, err := Firefox.Check(release); err == nil || !strings.Contains(err.Error(), "not Firefox Developer Edition") || !strings.Contains(err.Error(), "Mozilla signed") {
		t.Fatalf("release Firefox as Developer Edition: %v", err)
	}
	// Only Firefox on the channels that require signing is said to install
	// only add-ons Mozilla signed; Nightly takes unsigned ones when told to.
	for _, channel := range []string{"beta", "esr"} {
		_, p := geckoInstall(t, "Firefox", channel)
		if _, err := Firefox.Check(p); err == nil || !strings.Contains(err.Error(), "Mozilla signed") {
			t.Fatalf("%s Firefox as Developer Edition: %v", channel, err)
		}
	}
	_, nightly := geckoInstall(t, "Firefox", "nightly")
	if _, err := Firefox.Check(nightly); err == nil || !strings.Contains(err.Error(), "on the nightly channel") || strings.Contains(err.Error(), "Mozilla signed") {
		t.Fatalf("Nightly as Developer Edition: %v", err)
	}
	_, fork := geckoInstall(t, "Waterfox", "release")
	if _, err := Firefox.Check(fork); err == nil || !strings.Contains(err.Error(), "last run by Waterfox") || strings.Contains(err.Error(), "Mozilla signed") {
		t.Fatalf("another Gecko browser as Developer Edition: %v", err)
	}
}

// A refusal names the browser asked for by its name, not by what its
// application.ini says.
func TestCheckRefusalNamesTheBrowserAskedFor(t *testing.T) {
	_, dev := geckoInstall(t, "Firefox", "aurora")
	b := Browser{Config: "test", Name: "Test Browser", Bin: "test", AppName: "TestApp"}
	if _, err := b.Check(dev); err == nil || !strings.Contains(err.Error(), "not Test Browser") {
		t.Fatalf("%v", err)
	}
}

// A browser told by its application.ini is refused when that file is
// missing or cannot be read, not taken for the right one.
func TestCheckRefusesWithoutAReadableApplicationIni(t *testing.T) {
	dir, profile := geckoInstall(t, "", "release")
	if _, err := LibreWolf.Check(profile); err == nil || !strings.Contains(err.Error(), "application.ini") {
		t.Fatalf("no application.ini: %v", err)
	}
	must(t, os.Mkdir(filepath.Join(dir, "application.ini"), 0o755)) // reading it fails, even as root
	if _, err := LibreWolf.Check(profile); err == nil || !strings.Contains(err.Error(), "application.ini") {
		t.Fatalf("unreadable application.ini: %v", err)
	}
}

// A browser that says neither its channel nor its application name could
// not tell its profiles from any other's, so it takes none.
func TestCheckRefusesABrowserItCannotTellApart(t *testing.T) {
	_, profile := geckoInstall(t, "LibreWolf", "release")
	if bin, err := (Browser{}).Check(profile); err == nil {
		t.Fatalf("zero Browser took %s", bin)
	}
}

func TestReadStateFromExtensionsJSON(t *testing.T) {
	uuidPref := `user_pref("extensions.webextensions.uuids", "{\"mirrormark@chriscz.github.io\":\"` + testUUID + `\"}");` + "\n"
	addon := func(fields string) string {
		return `{"addons":[{"id":"other@x"},{"id":"mirrormark@chriscz.github.io","location":"app-profile","version":"0.1.0",` + fields + `}]}`
	}
	for _, c := range []struct {
		name       string
		xpi        bool
		ext, prefs string // "" means no file
		want       chrome.ProfileState
		wantUUID   string
	}{
		{"no XPI", false, "", "", chrome.ProfileState{State: chrome.NotInstalled}, ""},
		{"XPI, not started", true, "", "", chrome.ProfileState{State: chrome.NotInstalledYet}, ""},
		{"no entry", true, `{"addons":[{"id":"other@x"}]}`, "", chrome.ProfileState{State: chrome.NotInstalledYet}, ""},
		{"entry, no UUID yet", true, addon(`"active":true,"userDisabled":false,"appDisabled":false`), "", chrome.ProfileState{State: chrome.NotInstalledYet}, ""},
		{"ready", true, addon(`"active":true,"userDisabled":false,"appDisabled":false`), uuidPref, chrome.ProfileState{State: chrome.Ready}, testUUID},
		{"turned off", true, addon(`"active":false,"userDisabled":true,"appDisabled":false`), uuidPref, chrome.ProfileState{State: chrome.TurnedOff}, testUUID},
		{"refused", true, addon(`"active":false,"userDisabled":false,"appDisabled":true`), uuidPref, chrome.ProfileState{State: chrome.Refused}, testUUID},
	} {
		dir := t.TempDir()
		if c.xpi {
			must(t, os.MkdirAll(filepath.Join(dir, "extensions"), 0o755))
			must(t, os.WriteFile(XPIPath(dir), []byte("xpi"), 0o644))
		}
		if c.ext != "" {
			must(t, os.WriteFile(filepath.Join(dir, "extensions.json"), []byte(c.ext), 0o644))
		}
		if c.prefs != "" {
			must(t, os.WriteFile(filepath.Join(dir, "prefs.js"), []byte(c.prefs), 0o644))
		}
		got, uuid, err := ReadState(dir, "0.1.0")
		if err != nil || got != c.want || uuid != c.wantUUID {
			t.Errorf("%s: %+v %q %v; want %+v %q", c.name, got, uuid, err, c.want, c.wantUUID)
		}
	}
	// Another build.
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "extensions"), 0o755))
	must(t, os.WriteFile(XPIPath(dir), []byte("xpi"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "extensions.json"), []byte(addon(`"active":true`)), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "prefs.js"), []byte(uuidPref), 0o644))
	if got, _, err := ReadState(dir, "0.3.0"); err != nil || got != (chrome.ProfileState{State: chrome.OldBuild, Version: "0.1.0"}) {
		t.Errorf("old build: %+v %v", got, err)
	}
}
