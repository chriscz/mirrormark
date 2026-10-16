package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/firefox"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Person 1": "person-1", "  Work / Home ": "work-home", "Ünï": "n", "": ""} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChoose(t *testing.T) {
	all := []chrome.Profile{{Dir: "Default", Name: "Personal"}, {Dir: "Profile 1", Name: "Work"}, {Dir: "Profile 2", Name: "Work"}}
	for _, c := range []struct {
		picks []string
		want  []string // dirs, or nil for an error
		err   string
	}{
		{[]string{"Profile 1"}, []string{"Profile 1"}, ""},
		{[]string{"2", "0", "Profile 2"}, []string{"Profile 2", "Default"}, ""},
		{[]string{"Work"}, nil, `no profile "Work"; run mirrormark install to list them`},
		{[]string{"3"}, nil, `no profile number 3; run mirrormark install to list them`},
		{[]string{"99999999999999999999"}, nil, `no profile number 99999999999999999999; run mirrormark install to list them`},
		{[]string{"+1"}, nil, `no profile "+1"; run mirrormark install to list them`},
		{[]string{"-1"}, nil, `no profile "-1"; run mirrormark install to list them`},
		{[]string{"1", "Profile 1"}, []string{"Profile 1"}, ""},
	} {
		got, err := choose(all, c.picks, "mirrormark install")
		var dirs []string
		for _, p := range got {
			dirs = append(dirs, p.Dir)
		}
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%q: err %v, want %q", c.picks, err, c.err)
			}
			continue
		}
		if err != nil || !slices.Equal(dirs, c.want) {
			t.Errorf("%q: %q, %v", c.picks, dirs, err)
		}
	}
	// An exact ID wins over a number: a Gecko profile may live in a
	// directory named "1".
	gecko := []chrome.Profile{{Dir: "/p/0"}, {Dir: "/p/1"}, {Dir: "/p/x"}}
	if got, err := choose(gecko[1:], []string{"1"}, "mirrormark install --browser firefox"); err != nil || len(got) != 1 || got[0].Dir != "/p/1" {
		t.Errorf("ID 1 against number 1: %+v, %v", got, err)
	}
	dup := []chrome.Profile{{Dir: "/a/x.default"}, {Dir: "/b/x.default"}}
	if _, err := choose(dup, []string{"x.default"}, "mirrormark install --browser firefox"); err == nil || err.Error() != `2 profiles have the ID "x.default" (numbers 0, 1); name one by its number` {
		t.Errorf("duplicate ID: %v", err)
	}
}

func TestEnsureDBMakesUniqueIDs(t *testing.T) {
	p := db.Paths{Data: t.TempDir()}
	a, err := ensureDB(p, "chrome", "/u/a/Default", chrome.Profile{Dir: "Default", Name: "Person 1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ensureDB(p, "chrome", "/u/b/Default", chrome.Profile{Dir: "Default", Name: "Person 1"})
	again, _ := ensureDB(p, "chrome", "/u/a/Default", chrome.Profile{Dir: "Default", Name: "Person 1"})
	if a != "chrome-person-1" || b != "chrome-person-1-2" || again != a {
		t.Fatalf("ids %s %s %s", a, b, again)
	}
	d, _ := db.LoadDB(p.ProfileFile(b))
	if d.Profile.Label != "chrome/person-1-2" || d.Profile.Path != "/u/b/Default" || d.Profile.Browser != "chrome" {
		t.Fatalf("profile %+v", d.Profile)
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 2 {
		t.Fatalf("files %v", dbs)
	}
	// A name with no letters or digits falls back to the directory.
	if c, _ := ensureDB(p, "chrome", "/u/a/Profile 3", chrome.Profile{Dir: "Profile 3", Name: "☺"}); c != "chrome-profile-3" {
		t.Fatalf("id %s", c)
	}
}

// A database file mirrormark cannot read may be this profile's; making
// another would bind the profile to a new member behind the user's back.
func TestEnsureDBStopsAtAnUnreadableDatabase(t *testing.T) {
	p := db.Paths{Data: t.TempDir()}
	os.MkdirAll(p.ProfilesDir(), 0o700)
	os.WriteFile(p.ProfileFile("chrome-x"), []byte("not: [yaml"), 0o600)
	if _, err := ensureDB(p, "chrome", "/u/a/Default", chrome.Profile{Dir: "Default", Name: "X"}); err == nil {
		t.Fatal("no error")
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 0 {
		t.Fatalf("created %v", dbs)
	}
}

// A file mirrormark cannot check for is not a taken id: counting it as one
// would try the next id for ever.
func TestEnsureDBStopsWhenItCannotCheckAnID(t *testing.T) {
	skipIfRoot(t)
	p := db.Paths{Data: t.TempDir()}
	os.MkdirAll(p.ProfilesDir(), 0o700)
	// Listable but not searchable: the folder reads as empty and every
	// stat in it fails with a permission error.
	os.Chmod(p.ProfilesDir(), 0o400)
	t.Cleanup(func() { os.Chmod(p.ProfilesDir(), 0o700) })
	done := make(chan error, 1)
	go func() {
		_, err := ensureDB(p, "chrome", "/u/a/Default", chrome.Profile{Dir: "Default", Name: "X"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("err %v, want a permission error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ensureDB did not return")
	}
}

func TestConfigExampleIsAValidEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(configExample), 0o600)
	c, err := db.LoadConfig(path)
	if err != nil || len(c.Unions) != 0 {
		t.Fatalf("%+v, %v", c, err)
	}
}

// testHome gives the test its own home and mirrormark paths, and a
// google-chrome and a brave-browser first on PATH that fail, so no unit test
// starts a real browser; fakeChromeOnPath goes before them.
func testHome(t *testing.T) db.Paths {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "bin")
	os.Mkdir(dir, 0o755)
	for _, n := range []string{"google-chrome", "brave-browser"} {
		os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\necho 'unit tests must not start a browser' >&2\nexit 1\n"), 0o755)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, "")
	}
	p, _ := db.DefaultPaths()
	return p
}

func TestUnpackWritesTheKeyedExtension(t *testing.T) {
	p := testHome(t)
	var out, errb bytes.Buffer
	if code := run([]string{"unpack"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	manifest, err := os.ReadFile(filepath.Join(p.Data, "extension", "manifest.json"))
	if err != nil || !bytes.Contains(manifest, []byte(`"key"`)) {
		t.Fatalf("manifest %s, %v", manifest, err)
	}
	if !strings.Contains(out.String(), "Extension ID: ") {
		t.Fatalf("output %q", out.String())
	}
}

func TestUnpackIntoAFolderKeepsTheID(t *testing.T) {
	p := testHome(t)
	dir := filepath.Join(t.TempDir(), "ext")
	var out1, out2, errb bytes.Buffer
	if code := run([]string{"unpack", dir}, strings.NewReader(""), &out1, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	run([]string{"unpack"}, strings.NewReader(""), &out2, &errb)
	id := func(s string) string { return strings.Fields(strings.SplitN(s, "Extension ID: ", 2)[1])[0] }
	if id(out1.String()) != id(out2.String()) {
		t.Fatalf("ids differ:\n%s\n%s", out1.String(), out2.String())
	}
	if _, err := os.Stat(filepath.Join(p.Data, "extension.pem")); err != nil {
		t.Fatal("the key was not kept:", err)
	}
	if code := run([]string{"unpack", "a", "b"}, strings.NewReader(""), &out1, &errb); code != 2 {
		t.Fatalf("two folders: exit %d", code)
	}
}

// fakeSystem points the system files into a temporary root whose
// directories the test user cannot write, like the real ones, and replaces
// sudo with a fake that records each command and carries out install, which
// must read the file from stdin, and rm.
func fakeSystem(t *testing.T) (root string, calls *[][]string) {
	root = t.TempDir()
	for _, d := range []string{"usr", "etc", "opt"} {
		os.Mkdir(filepath.Join(root, d), 0o555)
	}
	lock := func(mode fs.FileMode) {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && p != root {
				os.Chmod(p, mode)
			}
			return nil
		})
	}
	t.Cleanup(func() { lock(0o755) })
	oldRoot, oldSudo := sysroot, runSudo
	t.Cleanup(func() { sysroot, runSudo = oldRoot, oldSudo })
	sysroot = root
	calls = &[][]string{}
	runSudo = func(stdin io.Reader, _, _ io.Writer, args ...string) error {
		*calls = append(*calls, args)
		lock(0o755)
		defer lock(0o555)
		switch args[0] {
		case "install": // install -D -m 0644 /dev/stdin DST
			if args[4] != "/dev/stdin" {
				return errors.New("install not from stdin")
			}
			data, err := io.ReadAll(stdin)
			if err != nil {
				return err
			}
			os.MkdirAll(filepath.Dir(args[5]), 0o755)
			return os.WriteFile(args[5], data, 0o644)
		case "rm": // rm -f -- PATH...
			for _, p := range args[3:] {
				os.Remove(p)
			}
		}
		return nil
	}
	return root, calls
}

// fakeServers holds, by path, how to stop each fake server, so a test with
// two fake browsers starts one and stopFakeServer can stop it.
var fakeServers = map[string]func(){}

// fakeServer holds the serve lock as a running server would.
func fakeServer(t *testing.T, p db.Paths) {
	if _, ok := fakeServers[p.ServeLock()]; ok {
		return
	}
	_, err := os.Stat(p.Data)
	made := errors.Is(err, fs.ErrNotExist)
	unlock, err := db.TryLock(p.ServeLock())
	if err != nil {
		t.Fatal(err)
	}
	fakeServers[p.ServeLock()] = func() {
		unlock()
		os.Remove(p.ServeLock())
		if made {
			os.Remove(p.Data) // only if nothing else is in it
		}
	}
	t.Cleanup(func() { stopFakeServer(p) })
}

// stopFakeServer stops the fake server and removes the files it made.
func stopFakeServer(p db.Paths) {
	if stop, ok := fakeServers[p.ServeLock()]; ok {
		stop()
		delete(fakeServers, p.ServeLock())
	}
}

// noServerWait makes install's check for a running server answer at once.
func noServerWait(t *testing.T) {
	old := serverWait
	serverWait = 0
	t.Cleanup(func() { serverWait = old })
}

// fakeChromeOnPath puts a google-chrome and a brave-browser on PATH that does what the
// extension's hello does: touch the lock file of the database the bind URL
// names, unless $FAKE_NO_HELLO names that database. It logs each call. The
// hello needs a server, so it holds the serve lock as one would.
// It sets the time from the clock rather than letting touch take the
// kernel's file time, which lags the clock by up to a tick and so can fall
// before the moment Bind started waiting.
func fakeChromeOnPath(t *testing.T, p db.Paths) (log string) {
	fakeServer(t, p)
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\nfor a; do url=$a; done\necho \"$@\" >> " + log + "\n" +
		"frag=${url##*#}; [ \"${frag%%.*}\" = \"$FAKE_NO_HELLO\" ] || touch -d \"@$(date +%s.%N)\" \"" + p.ProfilesDir() + "/${frag%%.*}.lock\"\n"
	for _, n := range []string{"google-chrome", "brave-browser"} {
		os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	old := chrome.BindWait
	chrome.BindWait = 500 * time.Millisecond
	t.Cleanup(func() { chrome.BindWait = old })
	return log
}

// tokenOf is the token install wrote for database id.
func tokenOf(t *testing.T, p db.Paths, id string) string {
	t.Helper()
	d, err := db.LoadDB(p.ProfileFile(id))
	if err != nil || d.Profile.Token == "" {
		t.Fatalf("no token for %s: %+v, %v", id, d, err)
	}
	return d.Profile.Token
}

// chromiumReady writes the Preferences a Chromium browser has once it has
// installed and enabled the extension in the profile.
func chromiumReady(t *testing.T, profileDir, id string) {
	t.Helper()
	v, err := extension.Version()
	must(t, err)
	must(t, os.MkdirAll(profileDir, 0o700))
	must(t, os.WriteFile(filepath.Join(profileDir, "Preferences"), []byte(`{"extensions":{"settings":{"`+id+`":{"path":"`+id+`/`+v+`_0","location":2,"disable_reasons":[],"manifest":{"version":"`+v+`"}}}}}`), 0o600))
}

// geckoReady writes the extensions.json and prefs.js a Gecko browser has
// once it has installed the add-on in the profile.
func geckoReady(t *testing.T, profileDir, uuid string) {
	t.Helper()
	v, err := extension.Version()
	must(t, err)
	must(t, os.WriteFile(filepath.Join(profileDir, "extensions.json"), []byte(`{"addons":[{"id":"`+extension.GeckoID+`","active":true,"userDisabled":false,"appDisabled":false,"version":"`+v+`","location":"app-profile"}]}`), 0o600))
	must(t, os.WriteFile(filepath.Join(profileDir, "prefs.js"), []byte(`user_pref("extensions.webextensions.uuids", "{\"mirrormark@chriscz.github.io\":\"`+uuid+`\"}");`+"\n"), 0o600))
}

// braveData is chromeData with four profiles, each of which Brave has run.
func braveData(t *testing.T) string {
	udd := t.TempDir()
	os.WriteFile(filepath.Join(udd, "Local State"), []byte(`{"profile":{"info_cache":{
		"Default":{"name":"Personal"},"Profile 1":{"name":"Work"},"Profile 2":{"name":"Old"},"Profile 3":{"name":"Off"}}}}`), 0o600)
	for _, d := range []string{"Default", "Profile 1", "Profile 2", "Profile 3"} {
		must(t, os.MkdirAll(filepath.Join(udd, d), 0o700))
		must(t, os.WriteFile(filepath.Join(udd, d, "Preferences"), []byte(`{}`), 0o600))
	}
	return udd
}

func chromeData(t *testing.T) string {
	udd := t.TempDir()
	os.WriteFile(filepath.Join(udd, "Local State"), []byte(`{"profile":{"info_cache":{
		"Profile 1":{"name":"Work"},"Default":{"name":"Personal"}}}}`), 0o600)
	return udd
}

func skipIfRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may write anywhere and install refuses to run as root")
	}
}

func TestInstallWritesSystemFilesWithSudoThenBinds(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, sudoCalls := fakeSystem(t)
	chromeLog := fakeChromeOnPath(t, p)
	udd := chromeData(t)

	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Extension ID: ") || !strings.Contains(out.String(), "Restart Chrome") || errb.Len() != 0 {
		t.Fatalf("output:\n%s%s", out.String(), errb.String())
	}
	key, _ := chrome.LoadKey(filepath.Join(p.Data, "extension.pem"))
	id := chrome.ExtensionID(chrome.PublicKeyDER(key))
	paths := []string{chrome.Chrome.ExternalFile(id), chrome.HostManifestPath()}
	if len(*sudoCalls) != 2 {
		t.Fatalf("sudo calls %v", *sudoCalls)
	}
	for i, c := range *sudoCalls {
		if !slices.Equal(c, []string{"install", "-D", "-m", "0644", "/dev/stdin", filepath.Join(root, paths[i])}) {
			t.Fatalf("sudo call %v", c)
		}
		if !strings.Contains(out.String(), c[5]) {
			t.Fatalf("output does not say what sudo writes:\n%s", out.String())
		}
	}
	// The data came through sudo's stdin.
	ext, _ := os.ReadFile(filepath.Join(root, paths[0]))
	if !bytes.Contains(ext, []byte(filepath.Join(p.Data, "mirrormark.crx"))) {
		t.Fatalf("external file %s", ext)
	}
	if host, _ := os.ReadFile(filepath.Join(root, paths[1])); !bytes.Contains(host, []byte(`"chrome-extension://`+id+`/"`)) {
		t.Fatalf("host manifest %s", host)
	}
	for dir, id := range map[string]string{"Default": "chrome-personal", "Profile 1": "chrome-work"} {
		d, err := db.LoadDB(p.ProfileFile(id))
		if err != nil || d.Profile.Path != filepath.Join(udd, dir) || d.Profile.Label != "chrome/"+strings.TrimPrefix(id, "chrome-") {
			t.Fatalf("%s: %+v, %v", id, d, err)
		}
	}
	if data, _ := os.ReadFile(p.ConfigFile()); string(data) != configExample {
		t.Fatalf("config %q", data)
	}
	if _, err := os.Stat(chromeLog); err == nil {
		t.Fatal("bound before Chrome restarted")
	}

	// After the restart: the files are current, so no sudo, and each
	// profile, which Chrome has installed the extension in, is bound with
	// one open.
	chromiumReady(t, filepath.Join(udd, "Default"), id)
	chromiumReady(t, filepath.Join(udd, "Profile 1"), id)
	out.Reset()
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if len(*sudoCalls) != 2 || strings.Contains(out.String(), "Restart Chrome") {
		t.Fatalf("second run: sudo %v\n%s", *sudoCalls, out.String())
	}
	for _, line := range []string{"bound: Default -> chrome-personal\n", "bound: Profile 1 -> chrome-work\n"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("no %q in:\n%s", line, out.String())
		}
	}
	calls, _ := os.ReadFile(chromeLog)
	if want := "--user-data-dir=" + udd + " --profile-directory=Default chrome-extension://" + id + "/bind.html#chrome-personal." + tokenOf(t, p, "chrome-personal") + "\n" +
		"--user-data-dir=" + udd + " --profile-directory=Profile 1 chrome-extension://" + id + "/bind.html#chrome-work." + tokenOf(t, p, "chrome-work") + "\n"; string(calls) != want {
		t.Fatalf("chrome calls:\n%s", calls)
	}
	if d, err := db.LoadDB(p.ProfileFile("chrome-personal")); err != nil || d.Profile.Origin != "chrome-extension://"+id {
		t.Fatalf("origin %+v, %v", d, err)
	}

	// One --profile binds that profile alone.
	out.Reset()
	os.Remove(chromeLog)
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Profile 1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if calls, _ := os.ReadFile(chromeLog); bytes.Count(calls, []byte("\n")) != 1 || strings.Contains(out.String(), "Default") {
		t.Fatalf("chrome calls:\n%s\noutput:\n%s", calls, out.String())
	}
}

func TestInstallReportsAProfileThatDoesNotConfirm(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	log := fakeChromeOnPath(t, p)
	chrome.BindWait = 300 * time.Millisecond
	udd := chromeData(t)
	var out, errb bytes.Buffer
	run([]string{"install", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb)
	id := installedID(t, p)
	chromiumReady(t, filepath.Join(udd, "Default"), id)
	chromiumReady(t, filepath.Join(udd, "Profile 1"), id)
	// Ready by its files, but the extension never reports in.
	t.Setenv("FAKE_NO_HELLO", "chrome-personal")
	out.Reset()
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if want := "not bound: Default: mirrormark did not report in within 300ms of opening the page; look at the tab Chrome opened, then run mirrormark install --user-data-dir " + udd + " --profile Default.\n"; !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "bound: Profile 1 -> chrome-work") {
		t.Fatalf("output:\n%s", out.String())
	}
	if calls, _ := os.ReadFile(log); bytes.Count(calls, []byte("#chrome-personal.")) != 1 {
		t.Fatalf("chrome calls:\n%s", calls)
	}
}

// TestInstallOpensNothingWhereMirrormarkIsNotReady: each state the
// profile's files can be in gets its own line, and only a ready profile
// gets the bind page.
func TestInstallOpensNothingWhereMirrormarkIsNotReady(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	log := fakeChromeOnPath(t, p)
	udd := braveData(t) // Default, Profile 1, Profile 2, Profile 3
	var out, errb bytes.Buffer
	all := []string{"--profile", "0", "--profile", "1", "--profile", "2", "--profile", "3"}
	run(append([]string{"install", "--browser", "brave", "--user-data-dir", udd}, all...), strings.NewReader(""), &out, &errb)
	id := installedID(t, p)
	chromiumReady(t, filepath.Join(udd, "Default"), id)
	// Profile 1's Preferences, from braveData, has no entry for mirrormark.
	removedAndTurnedOff(t, udd, id)
	os.Remove(log)
	out.Reset()
	if code := run(append([]string{"install", "--browser", "brave", "--user-data-dir", udd}, all...), strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	for _, line := range []string{
		"bound: Default -> brave-",
		"not ready: Profile 1: Brave has not installed mirrormark in this profile yet. Quit Brave completely (close every window), start it again and open this profile, wait half a minute, then run mirrormark install --browser brave --user-data-dir " + udd + ` --profile "Profile 1".`,
		"not ready: Profile 2: you removed mirrormark from this profile, and Brave will not install it there again. Run mirrormark unpack, turn on Developer mode in brave://extensions,",
		"not ready: Profile 3: mirrormark is turned off in this profile. Turn it on in brave://extensions, wait half a minute,",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("no %q in:\n%s", line, out.String())
		}
	}
	if calls, _ := os.ReadFile(log); bytes.Count(calls, []byte("\n")) != 1 || !bytes.Contains(calls, []byte("--profile-directory=Default ")) {
		t.Fatalf("brave calls:\n%s", calls)
	}
}

// TestInstallBindsNothingWithoutAServer: no profile can report in while
// no server runs, so install opens no bind page and says how to start one.
func TestInstallBindsNothingWithoutAServer(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	log := fakeChromeOnPath(t, p)
	noServerWait(t)
	udd := chromeData(t)
	var out, errb bytes.Buffer
	run([]string{"install", "--user-data-dir", udd, "--profile", "Default"}, strings.NewReader(""), &out, &errb)
	chromiumReady(t, filepath.Join(udd, "Default"), installedID(t, p))
	stopFakeServer(p)
	out.Reset()
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Default"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if want := "mirrormark is not running, so no profile can report in. Start it with systemctl --user start mirrormark, or run mirrormark serve, then run mirrormark install --user-data-dir " + udd + " --profile Default again.\n"; !strings.HasSuffix(out.String(), want) {
		t.Fatalf("output:\n%s", out.String())
	}
	if calls, _ := os.ReadFile(log); len(calls) != 0 {
		t.Fatalf("chrome calls:\n%s", calls)
	}
}

// removedAndTurnedOff makes Profile 2 of udd one the user removed
// mirrormark from, and Profile 3 one where they turned it off.
func removedAndTurnedOff(t *testing.T, udd, id string) {
	t.Helper()
	must(t, os.WriteFile(filepath.Join(udd, "Profile 2", "Preferences"), []byte(`{"extensions":{"settings":{"`+id+`":{}},"external_uninstalls":["`+id+`"]}}`), 0o600))
	chromiumReady(t, filepath.Join(udd, "Profile 3"), id)
	prefs, _ := os.ReadFile(filepath.Join(udd, "Profile 3", "Preferences"))
	must(t, os.WriteFile(filepath.Join(udd, "Profile 3", "Preferences"), bytes.Replace(prefs, []byte(`"disable_reasons":[]`), []byte(`"disable_reasons":[1]`), 1), 0o600))
}

// TestFirefoxInstallOpensNothingWhereTheAddonIsNotReady: a Gecko profile
// whose files do not show the add-on running gets advice, not the page.
func TestFirefoxInstallOpensNothingWhereTheAddonIsNotReady(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	// alpha has the XPI, but Firefox has not started there since.
	beta := filepath.Join(root, "xbeta.beta")
	geckoReady(t, beta, "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b")
	ext, _ := os.ReadFile(filepath.Join(beta, "extensions.json"))
	ext = bytes.Replace(ext, []byte(`"active":true`), []byte(`"active":false`), 1)
	ext = bytes.Replace(ext, []byte(`"appDisabled":false`), []byte(`"appDisabled":true`), 1)
	must(t, os.WriteFile(filepath.Join(beta, "extensions.json"), ext, 0o600))
	out.Reset()
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	for _, line := range []string{
		"not ready: xalpha.alpha: Firefox Developer Edition has not installed mirrormark in this profile yet. Quit Firefox Developer Edition if it runs in this profile (close every window) and start it there again, then run mirrormark install --browser firefox --profile xalpha.alpha.\n",
		"not ready: xbeta.beta: Firefox Developer Edition refuses mirrormark in this profile because it is not signed. Check that the profile's user.js sets xpinstall.signatures.required to false, restart Firefox Developer Edition there, then run mirrormark install --browser firefox --profile xbeta.beta.\n",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("no %q in:\n%s", line, out.String())
		}
	}
	if c, _ := os.ReadFile(calls); len(c) != 0 {
		t.Fatalf("calls:\n%s", c)
	}
}

func TestFirefoxInstallBindsNothingWithoutAServer(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := firefoxData(t, p, "aurora")
	noServerWait(t)
	var out, errb bytes.Buffer
	run([]string{"install", "--browser", "firefox", "--profile", "xbeta.beta"}, strings.NewReader(""), &out, &errb)
	geckoReady(t, filepath.Join(root, "xbeta.beta"), "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b")
	stopFakeServer(p)
	out.Reset()
	if code := run([]string{"install", "--browser", "firefox", "--profile", "xbeta.beta"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if want := "mirrormark is not running, so no profile can report in. Start it with systemctl --user start mirrormark, or run mirrormark serve, then run mirrormark install --browser firefox --profile xbeta.beta again.\n"; !strings.HasSuffix(out.String(), want) {
		t.Fatalf("output:\n%s", out.String())
	}
	if c, _ := os.ReadFile(calls); len(c) != 0 {
		t.Fatalf("calls:\n%s", c)
	}
}

func TestInstallStopsWhenSudoFails(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	runSudo = func(_ io.Reader, _, _ io.Writer, args ...string) error { return fs.ErrPermission }
	udd := chromeData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Default"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if !strings.Contains(errb.String(), "sudo") {
		t.Fatalf("stderr %q", errb.String())
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 0 {
		t.Fatalf("databases made without the extension: %v", dbs)
	}
}

// A data directory named through a symlink is the same directory, so it
// must map to the same database files.
func TestInstallResolvesASymlinkedDataDir(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	fakeChromeOnPath(t, p)
	udd := chromeData(t)
	link := filepath.Join(t.TempDir(), "chrome")
	if err := os.Symlink(udd, link); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	for i, dir := range []string{link, udd} {
		if i == 1 { // the second run binds, so Chrome has installed it
			chromiumReady(t, filepath.Join(udd, "Profile 1"), installedID(t, p))
		}
		if code := run([]string{"install", "--user-data-dir", dir, "--profile", "Profile 1"}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
		}
	}
	dbs, _ := db.LoadAll(p)
	if len(dbs) != 1 || dbs["chrome-work"] == nil || dbs["chrome-work"].Profile.Path != filepath.Join(udd, "Profile 1") {
		t.Fatalf("databases %v", dbs)
	}
}

// As root, the commands would work in root's home, not the user's.
func TestCommandsRefuseToRunAsRoot(t *testing.T) {
	p := testHome(t)
	_, calls := fakeSystem(t)
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 0 }
	systemctl := fakeSystemctl(t, "")
	for _, args := range [][]string{{"install", "--user-data-dir", chromeData(t)}, {"install", "--user-data-dir", chromeData(t), "--profile", "Default"}, {"uninstall"}, {"unpack"}, {"unpack", t.TempDir()}} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb); code != 1 || !strings.Contains(errb.String(), "not as root") {
			t.Fatalf("%v: exit %d: %s", args, code, errb.String())
		}
	}
	if _, err := os.Stat(p.Data); err == nil || len(*calls) != 0 || len(*systemctl) != 0 {
		t.Fatalf("changed something as root: %v, sudo %v, systemctl %q", err, *calls, *systemctl)
	}
	mustBeGone(t, "as root", unitPath(p))
}

// Two installs with different keys share the host manifest's one name, so
// each replaces the other's. install goes ahead but says so.
func TestInstallWarnsWhenItReplacesAnotherExtensionsHostManifest(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := fakeSystem(t)
	fakeChromeOnPath(t, p)
	other := chrome.Chrome.SystemFiles(chrome.Extension{ID: "pjjjaeommfpeolloibnafkcmkindkagl", Version: "1", CRX: "/x.crx"}, "/bin/x")[1]
	other.Path = filepath.Join(root, other.Path)
	runSudo(bytes.NewReader(other.Data), nil, nil, "install", "-D", "-m", "0644", "/dev/stdin", other.Path)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", chromeData(t), "--profile", "Default"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "warning: "+other.Path+" lets another extension") {
		t.Fatalf("stderr %q", errb.String())
	}
	if chrome.FilesCurrent([]chrome.File{other}) {
		t.Fatal("the host manifest was not replaced")
	}
}

func TestInstallRejectsAnUnknownProfile(t *testing.T) {
	testHome(t)
	_, calls := fakeSystem(t)
	udd := chromeData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Nope"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if want := `mirrormark: no profile "Nope"; run mirrormark install --user-data-dir ` + udd + " to list them\n"; errb.String() != want || len(*calls) != 0 {
		t.Fatalf("stderr %q, sudo %v", errb.String(), *calls)
	}
}

// TestInstallWithoutProfileListsAndWritesNothing: no --profile prints the
// list and touches nothing: no key, no extension, no database, no sudo.
func TestInstallWithoutProfileListsAndWritesNothing(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	_, sudoCalls := fakeSystem(t)
	log := fakeChromeOnPath(t, p)
	stopFakeServer(p) // a listing needs none
	systemctl := fakeSystemctl(t, "")
	udd := braveData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	want := "Brave profiles in " + udd + ":\n" +
		"  #  ID         NAME      STATE          DATABASE\n" +
		"  0  Default    Personal  not installed  -\n" +
		"  1  Profile 1  Work      not installed  -\n" +
		"  2  Profile 2  Old       not installed  -\n" +
		"  3  Profile 3  Off       not installed  -\n" +
		"Nothing was changed. Name the profiles to install into or bind with --profile ID or --profile #, for example:\n" +
		"  mirrormark install --browser brave --user-data-dir " + udd + " --profile Default\n"
	if out.String() != want || errb.Len() != 0 {
		t.Fatalf("output:\n%s\nwant:\n%s\nstderr: %s", out.String(), want, errb.String())
	}
	if _, err := os.Stat(p.Data); !errors.Is(err, fs.ErrNotExist) || len(*sudoCalls) != 0 {
		t.Fatalf("data dir %v, sudo %v", err, *sudoCalls)
	}
	if _, err := os.Stat(p.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config: %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("started Brave")
	}
	if len(*systemctl) != 0 {
		t.Fatalf("systemctl %q", *systemctl)
	}
	mustBeGone(t, "listing", unitPath(p))
}

// snapshot lists every file under dirs with its size and time, so a test
// can tell that nothing was written there.
func snapshot(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, d := range dirs {
		filepath.WalkDir(d, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if fi, err := e.Info(); err == nil {
				got[path] = fmt.Sprint(fi.Size(), fi.ModTime().UnixNano(), fi.Mode())
			}
			return nil
		})
	}
	return got
}

// After an install, the listing shows each profile's state and its
// database, and still writes nothing; its example is the first profile not
// yet bound.
func TestInstallListsStatesAndDatabasesAfterAnInstall(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, sudoCalls := fakeSystem(t)
	log := fakeChromeOnPath(t, p)
	udd := braveData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	id := installedID(t, p)
	chromiumReady(t, filepath.Join(udd, "Default"), id)
	chromiumReady(t, filepath.Join(udd, "Profile 1"), id)
	out.Reset()
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	removedAndTurnedOff(t, udd, id)
	systemctl := fakeSystemctl(t, "")
	before, calls, started := snapshot(t, root, p.Data, p.State, filepath.Dir(p.Config), udd), len(*sudoCalls), snapshot(t, log)
	out.Reset()
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	want := "Brave profiles in " + udd + ":\n" +
		"  #  ID         NAME      STATE       DATABASE\n" +
		"  0  Default    Personal  bound       brave-personal\n" +
		"  1  Profile 1  Work      bound       brave-work\n" +
		"  2  Profile 2  Old       removed     -\n" +
		"  3  Profile 3  Off       turned off  -\n" +
		"Nothing was changed. Name the profiles to install into or bind with --profile ID or --profile #, for example:\n" +
		"  mirrormark install --browser brave --user-data-dir " + udd + ` --profile "Profile 2"` + "\n"
	if out.String() != want || errb.Len() != 0 {
		t.Fatalf("output:\n%s\nwant:\n%s\nstderr: %s", out.String(), want, errb.String())
	}
	if after := snapshot(t, root, p.Data, p.State, filepath.Dir(p.Config), udd); !maps.Equal(before, after) || len(*sudoCalls) != calls || !maps.Equal(started, snapshot(t, log)) || len(*systemctl) != 0 {
		t.Fatalf("the listing changed something:\nbefore %v\nafter %v\nsudo %v\nsystemctl %q", before, after, *sudoCalls, *systemctl)
	}
}

// A Gecko listing says which profiles have never run or belong to another
// browser, and writes into none of them.
func TestInstallListsGeckoProfilesAndWritesNothing(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := firefoxData(t, p, "aurora")
	stopFakeServer(p) // a listing needs none
	neverRun(t, root, "gamma")
	release := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(release, "defaults", "pref"), 0o755))
	must(t, os.WriteFile(filepath.Join(release, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "release");`), 0o644))
	systemctl := fakeSystemctl(t, "")
	delta := filepath.Join(root, "xdelta.delta")
	must(t, os.MkdirAll(delta, 0o700))
	must(t, os.WriteFile(filepath.Join(delta, "prefs.js"), nil, 0o600))
	must(t, os.WriteFile(filepath.Join(delta, "compatibility.ini"), []byte("[Compatibility]\nLastPlatformDir="+release+"\n"), 0o644))
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	want := "Firefox Developer Edition profiles in " + root + ":\n" +
		"  #  ID            NAME   STATE                          DATABASE\n" +
		"  0  xalpha.alpha  alpha  not installed                  -\n" +
		"  1  xbeta.beta    beta   not installed                  -\n" +
		"  2  xdelta.delta  delta  not Firefox Developer Edition  -\n" +
		"  3  xgamma.gamma  gamma  never run                      -\n" +
		"Nothing was changed. Name the profiles to install into or bind with --profile ID or --profile #, for example:\n" +
		"  mirrormark install --browser firefox --profile xalpha.alpha\n"
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
	// The listing says why delta looks like another browser's.
	if w := "mirrormark: warning: xdelta.delta: it was last run by a browser on the release channel"; !strings.HasPrefix(errb.String(), w) || strings.Count(errb.String(), "\n") != 1 {
		t.Fatalf("stderr %q, want one line starting %q", errb.String(), w)
	}
	for _, n := range []string{"alpha", "beta", "delta", "gamma"} {
		mustBeGone(t, "listing", filepath.Join(root, "x"+n+"."+n, "user.js"), filepath.Join(root, "x"+n+"."+n, "extensions"))
	}
	host, _ := firefox.HostManifestPath()
	mustBeGone(t, "listing", host, p.Data, calls, unitPath(p))
	if len(*systemctl) != 0 {
		t.Fatalf("systemctl %q", *systemctl)
	}
}

func TestUninstallRemovesSystemFilesAndKeepsData(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, sudoCalls := fakeSystem(t)
	fakeChromeOnPath(t, p)
	udd := chromeData(t)
	var out, errb bytes.Buffer
	run([]string{"install", "--user-data-dir", udd, "--profile", "Default"}, strings.NewReader(""), &out, &errb)
	key, _ := chrome.LoadKey(filepath.Join(p.Data, "extension.pem"))
	id := chrome.ExtensionID(chrome.PublicKeyDER(key))
	paths := []string{chrome.Chrome.ExternalFile(id), chrome.HostManifestPath()}

	out.Reset()
	if code := run([]string{"uninstall"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	last := (*sudoCalls)[len(*sudoCalls)-1]
	if !slices.Equal(last, []string{"rm", "-f", "--", filepath.Join(root, paths[0]), filepath.Join(root, paths[1])}) {
		t.Fatalf("sudo %v", last)
	}
	for _, f := range paths {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Fatalf("%s left", f)
		}
	}
	for _, gone := range []string{"extension", "mirrormark.crx"} {
		if _, err := os.Stat(filepath.Join(p.Data, gone)); err == nil {
			t.Fatalf("%s left", gone)
		}
	}
	for _, kept := range []string{p.ConfigFile(), p.ProfileFile("chrome-personal"), filepath.Join(p.Data, "extension.pem")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s removed", kept)
		}
	}
	if !strings.Contains(out.String(), "Kept") || !strings.Contains(out.String(), "--purge") {
		t.Fatalf("output does not say what it kept:\n%s", out.String())
	}

	// A server that has run leaves its lock file behind.
	must(t, os.WriteFile(p.ServeLock(), nil, 0o600))
	out.Reset()
	if code := run([]string{"uninstall", "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, gone := range []string{p.ConfigFile(), p.Data, p.State} {
		if _, err := os.Stat(gone); err == nil {
			t.Fatalf("%s left after --purge", gone)
		}
	}
}

// The host manifest's name is fixed; another install's manifest (another
// user's key, say) is not this one's to remove.
func TestUninstallLeavesAnotherExtensionsHostManifest(t *testing.T) {
	skipIfRoot(t)
	testHome(t)
	root, sudoCalls := fakeSystem(t)
	var out, errb bytes.Buffer
	run([]string{"unpack"}, strings.NewReader(""), &out, &errb)
	other := chrome.Chrome.SystemFiles(chrome.Extension{ID: "pjjjaeommfpeolloibnafkcmkindkagl", Version: "1", CRX: "/x.crx"}, "/bin/x")[1]
	other.Path = filepath.Join(root, other.Path)
	runSudo(bytes.NewReader(other.Data), nil, nil, "install", "-D", "-m", "0644", "/dev/stdin", other.Path)
	if code := run([]string{"uninstall"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !chrome.FilesCurrent([]chrome.File{other}) {
		t.Fatal("another extension's host manifest was removed")
	}
	if len(*sudoCalls) != 1 {
		t.Fatalf("sudo for nothing: %v", *sudoCalls)
	}
}

func TestEnsureDBNamesFirefoxProfiles(t *testing.T) {
	p := db.Paths{Data: t.TempDir()}
	id, err := ensureDB(p, "firefox", "/h/.config/mozilla/firefox/x.dev-edition-default", chrome.Profile{Dir: "x.dev-edition-default", Name: "dev-edition-default"})
	d, _ := db.LoadDB(p.ProfileFile(id))
	if err != nil || id != "firefox-dev-edition-default" || d.Profile.Label != "firefox/dev-edition-default" || d.Profile.Browser != "firefox" {
		t.Fatalf("%s %+v %v", id, d, err)
	}
}

// geckoData makes a profiles root with two profiles, alpha and beta, last
// run by a fake browser in a directory of its own: its binary is called
// bin, its application.ini names app and its channel-prefs.js says
// channel. The fake logs its calls to calls and, given a bind URL, touches
// the lock file of the database it names.
func geckoData(t *testing.T, p db.Paths, root, bin, app, channel string) (calls string) {
	fakeServer(t, p)
	ff := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(ff, "defaults", "pref"), 0o755))
	must(t, os.WriteFile(filepath.Join(ff, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "`+channel+`");`), 0o644))
	must(t, os.WriteFile(filepath.Join(ff, "application.ini"), []byte("[App]\nName="+app+"\n"), 0o644))
	calls = filepath.Join(ff, "calls")
	must(t, os.WriteFile(filepath.Join(ff, bin), []byte("#!/bin/sh\nfor a; do url=$a; done\necho \"$@\" >> "+calls+"\n"+
		"case \"$url\" in *bind.html*) frag=${url##*#}; touch -d \"@$(date +%s.%N)\" \""+p.ProfilesDir()+"/${frag%%.*}.lock\";; esac\n"), 0o755))
	ini := "[General]\nVersion=2\n"
	for i, n := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "x"+n+"."+n)
		must(t, os.MkdirAll(dir, 0o700))
		must(t, os.WriteFile(filepath.Join(dir, "prefs.js"), nil, 0o600))
		must(t, os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte("[Compatibility]\nLastPlatformDir="+ff+"\n"), 0o644))
		ini += fmt.Sprintf("[Profile%d]\nName=%s\nIsRelative=1\nPath=x%s.%s\n", i, n, n, n)
	}
	must(t, os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644))
	old := chrome.BindWait
	chrome.BindWait = 500 * time.Millisecond
	t.Cleanup(func() { chrome.BindWait = old })
	return calls
}

// geckoBoth names both of geckoData's profiles, by ID.
var geckoBoth = []string{"--profile", "xalpha.alpha", "--profile", "xbeta.beta"}

// firefoxData makes Developer Edition's (or, with another channel, another
// Firefox's) profiles root with alpha and beta.
func firefoxData(t *testing.T, p db.Paths, channel string) (root, calls string) {
	root = filepath.Join(os.Getenv("HOME"), ".config", "mozilla", "firefox")
	return root, geckoData(t, p, root, "firefox", "Firefox", channel)
}

// librewolfData makes LibreWolf's profiles root with alpha and beta.
func librewolfData(t *testing.T, p db.Paths) (root, calls string) {
	root = filepath.Join(os.Getenv("HOME"), ".config", "librewolf", "librewolf")
	return root, geckoData(t, p, root, "librewolf", "LibreWolf", "release")
}

func TestInstallIntoFirefoxThenBind(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Restart Firefox") || !strings.Contains(out.String(), "unsigned") {
		t.Fatalf("output:\n%s", out.String())
	}
	for _, n := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "x"+n+"."+n)
		if _, err := os.Stat(firefox.XPIPath(dir)); err != nil {
			t.Fatal(err)
		}
		if user, _ := os.ReadFile(filepath.Join(dir, "user.js")); string(user) != strings.Join(firefox.Prefs, "\n")+"\n" {
			t.Fatalf("user.js %q", user)
		}
		d, err := db.LoadDB(p.ProfileFile("firefox-" + n))
		if err != nil || d.Profile.Path != dir || d.Profile.Browser != "firefox" {
			t.Fatalf("%s: %+v %v", n, d, err)
		}
	}
	host, _ := firefox.HostManifestPath()
	if data, err := os.ReadFile(host); err != nil || !bytes.Contains(data, []byte(extension.GeckoID)) {
		t.Fatalf("host manifest %s %v", data, err)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("bound before Firefox restarted")
	}

	// Firefox has installed the add-on and recorded its UUID.
	for _, n := range []string{"alpha", "beta"} {
		geckoReady(t, filepath.Join(root, "x"+n+"."+n), "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4"+n[:1])
	}
	out.Reset()
	if code := run([]string{"install", "--browser", "firefox", "--profile", "xbeta.beta"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "bound: xbeta.beta -> firefox-beta\n") || strings.Contains(out.String(), "alpha") {
		t.Fatalf("output:\n%s", out.String())
	}
	if c, _ := os.ReadFile(calls); !bytes.Contains(c, []byte("about:blank moz-extension://5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b/bind.html#firefox-beta."+tokenOf(t, p, "firefox-beta"))) {
		t.Fatalf("calls:\n%s", c)
	}
	if d, err := db.LoadDB(p.ProfileFile("firefox-beta")); err != nil || d.Profile.Origin != "moz-extension://5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b" {
		t.Fatalf("origin %+v, %v", d, err)
	}
}

func TestInstallRefusesReleaseFirefox(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "release")
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "Developer Edition") || !strings.Contains(errb.String(), "release") {
		t.Fatalf("stderr %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "xalpha.alpha", "user.js")); err == nil {
		t.Fatal("wrote into a release profile")
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 0 {
		t.Fatalf("made databases %v", dbs)
	}
}

// One release profile among Developer Edition ones stops the whole install
// before it writes anything, so no profile is left half set up.
func TestInstallWithOneReleaseProfileWritesNothing(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	release := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(release, "defaults", "pref"), 0o755))
	must(t, os.WriteFile(filepath.Join(release, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "release");`), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "xbeta.beta", "compatibility.ini"), []byte("[Compatibility]\nLastPlatformDir="+release+"\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "xalpha.alpha", "user.js"), []byte("user_pref(\"browser.startup.page\", 3);\n"), 0o644))
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "cannot install into these profiles; leave them out:\n  xbeta.beta: it was last run by a browser on the release channel") || strings.Contains(errb.String(), "alpha") {
		t.Fatalf("stderr %q", errb.String())
	}
	for _, n := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(root, "x"+n+"."+n, "extensions")); err == nil {
			t.Errorf("%s: wrote the XPI", n)
		}
	}
	if user, _ := os.ReadFile(filepath.Join(root, "xalpha.alpha", "user.js")); string(user) != "user_pref(\"browser.startup.page\", 3);\n" {
		t.Errorf("alpha's user.js changed: %q", user)
	}
	if _, err := os.Stat(filepath.Join(root, "xbeta.beta", "user.js")); err == nil {
		t.Error("wrote beta's user.js")
	}
	host, _ := firefox.HostManifestPath()
	if _, err := os.Stat(host); err == nil {
		t.Error("wrote the host manifest")
	}
	if _, err := os.Stat(p.ProfilesDir()); err == nil {
		t.Error("made the databases directory")
	}
	if _, err := os.Stat(p.ConfigFile()); err == nil {
		t.Error("wrote the config")
	}
}

func TestUninstallFromFirefoxRestoresItsChecks(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb)
	dir := filepath.Join(root, "xalpha.alpha")
	user, _ := os.ReadFile(filepath.Join(dir, "user.js"))
	must(t, os.WriteFile(filepath.Join(dir, "user.js"), append([]byte("user_pref(\"browser.startup.page\", 3);\n"), user...), 0o644))

	out.Reset()
	if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if _, err := os.Stat(firefox.XPIPath(dir)); err == nil {
		t.Fatal("XPI left")
	}
	want := "user_pref(\"browser.startup.page\", 3);\n" + firefox.ResetPrefs[firefox.Prefs[0]] + "\n" + firefox.ResetPrefs[firefox.Prefs[1]] + "\n" + firefox.Prefs[2] + "\n"
	if got, _ := os.ReadFile(filepath.Join(dir, "user.js")); string(got) != want {
		t.Fatalf("user.js %q", got)
	}
	host, _ := firefox.HostManifestPath()
	if _, err := os.Stat(host); err == nil {
		t.Fatal("host manifest left")
	}
	if _, err := os.Stat(p.ProfileFile("firefox-alpha")); err != nil {
		t.Fatal("database removed without --purge")
	}
	if !strings.Contains(out.String(), "checks add-on signatures there again") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// With no Firefox database, uninstall cannot tell which profiles to put
// back, and says so rather than claiming it did.
func TestFirefoxUninstallWithNoProfileSaysSo(t *testing.T) {
	skipIfRoot(t)
	testHome(t)
	var out, errb bytes.Buffer
	if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "No Firefox Developer Edition profile") || !strings.Contains(out.String(), "xpinstall.signatures.required") ||
		strings.Contains(out.String(), "checks add-on signatures there again") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// Firefox has no key, so its uninstall does not say it kept one.
func TestFirefoxUninstallKeepsNoKey(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	for _, args := range [][]string{append([]string{"install", "--browser", "firefox"}, geckoBoth...), {"uninstall", "--browser", "firefox"}} {
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%s: exit %d: %s%s", args[0], code, out.String(), errb.String())
		}
	}
	if !strings.Contains(out.String(), "Kept your config") || strings.Contains(out.String(), "key") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// A user.js uninstall has nothing to change in is not rewritten.
func TestFirefoxUninstallLeavesAnUnchangedUserJSAlone(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	for _, args := range [][]string{append([]string{"install", "--browser", "firefox"}, geckoBoth...), {"uninstall", "--browser", "firefox"}} {
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%s: exit %d: %s%s", args[0], code, out.String(), errb.String())
		}
	}
	user := filepath.Join(root, "xalpha.alpha", "user.js")
	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(user, old, old); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if fi, err := os.Stat(user); err != nil || !fi.ModTime().Equal(old) {
		t.Fatalf("user.js rewritten: %v %v", fi.ModTime(), err)
	}
}

// Uninstall puts user.js back before it removes the add-on, so if a write
// fails, the add-on and host manifest are still there and running
// uninstall again finishes the job.
func TestFirefoxUninstallWritesUserJSFirst(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	locked := t.TempDir()
	link := filepath.Join(root, "xbeta.beta", "user.js")
	data, err := os.ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "user.js"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(locked, "user.js"), link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	host, _ := firefox.HostManifestPath()
	mustExist(t, "failed uninstall", firefox.XPIPath(filepath.Join(root, "xalpha.alpha")), firefox.XPIPath(filepath.Join(root, "xbeta.beta")), host)
}

// Installing again after an uninstall turns the reset lines back into the
// prefs where they are, rather than adding a pair each time.
func TestFirefoxReinstallKeepsOneLinePerPref(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"install"}, {"uninstall"}, {"install"}, {"uninstall"}, {"install"}} {
		if args[0] == "install" {
			args = append(args, geckoBoth...)
		}
		if code := run(append(args, "--browser", "firefox"), strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%s: exit %d: %s%s", args[0], code, out.String(), errb.String())
		}
	}
	for _, n := range []string{"alpha", "beta"} {
		if user, _ := os.ReadFile(filepath.Join(root, "x"+n+"."+n, "user.js")); string(user) != strings.Join(firefox.Prefs, "\n")+"\n" {
			t.Fatalf("%s: user.js %q", n, user)
		}
	}
}

// A user.js linked in from the user's dotfiles stays a link, and a private
// one stays private, through install and uninstall.
func TestFirefoxKeepsALinkedPrivateUserJS(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	target, link := filepath.Join(t.TempDir(), "user.js"), filepath.Join(root, "xalpha.alpha", "user.js")
	must(t, os.WriteFile(target, []byte("user_pref(\"browser.startup.page\", 3);\n"), 0o600))
	must(t, os.Symlink(target, link))
	var out, errb bytes.Buffer
	for _, args := range [][]string{append([]string{"install", "--browser", "firefox"}, geckoBoth...), {"uninstall", "--browser", "firefox"}} {
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%s: exit %d: %s%s", args[0], code, out.String(), errb.String())
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s replaced the link: %v", args[0], err)
		}
		fi, _ := os.Stat(target)
		data, _ := os.ReadFile(target)
		if fi.Mode().Perm() != 0o600 || !bytes.Contains(data, []byte("browser.startup.page")) || !bytes.Contains(data, []byte("signatures.required")) {
			t.Fatalf("%s: %v %q", args[0], fi.Mode().Perm(), data)
		}
	}
}

// installBoth installs into Chrome's two profiles and Firefox's two, gives
// one profile of each a backup, and writes a log, as a few days of use
// would.
func installBoth(t *testing.T) (p db.Paths, root string) {
	t.Helper()
	p = testHome(t)
	fakeSystem(t)
	root, _ = firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"install", "--user-data-dir", chromeData(t), "--profile", "Default", "--profile", "1"}, append([]string{"install", "--browser", "firefox"}, geckoBoth...)} {
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%v: exit %d: %s%s", args, code, out.String(), errb.String())
		}
	}
	for _, id := range []string{"chrome-personal", "firefox-alpha"} {
		if err := os.MkdirAll(p.BackupDir(id), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.BackupDir(id), "2026-01-01.yaml"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(p.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.LogFile(), []byte("log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, root
}

func mustExist(t *testing.T, when string, paths ...string) {
	t.Helper()
	for _, f := range paths {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s: %s missing", when, f)
		}
	}
}

func mustBeGone(t *testing.T, when string, paths ...string) {
	t.Helper()
	for _, f := range paths {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s: %s left", when, f)
		}
	}
}

// Purging Firefox deletes only Firefox's databases: Chrome's install and
// the files both share stay until Chrome goes too.
func TestFirefoxPurgeKeepsChromesFiles(t *testing.T) {
	skipIfRoot(t)
	p, _ := installBoth(t)
	var out, errb bytes.Buffer
	if code := run([]string{"uninstall", "--browser", "firefox", "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "firefox purge", p.ProfileFile("firefox-alpha"), p.ProfileFile("firefox-beta"), p.BackupDir("firefox-alpha"))
	mustExist(t, "firefox purge", p.ProfileFile("chrome-personal"), p.ProfileFile("chrome-work"), p.BackupDir("chrome-personal"),
		filepath.Join(p.Data, "extension.pem"), p.ConfigFile(), p.LogFile())
	if !strings.Contains(out.String(), "Kept") || !strings.Contains(out.String(), "chrome-personal") {
		t.Fatalf("output does not say what it kept:\n%s", out.String())
	}

	out.Reset()
	if code := run([]string{"uninstall", "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "last purge", p.ConfigFile(), p.Data, p.State)
}

// Purging Chrome keeps the Firefox databases, which Firefox's uninstall
// needs to find the profiles whose user.js it puts back.
func TestChromePurgeKeepsFirefoxDatabases(t *testing.T) {
	skipIfRoot(t)
	p, root := installBoth(t)
	var out, errb bytes.Buffer
	if code := run([]string{"uninstall", "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "chrome purge", p.ProfileFile("chrome-personal"), p.ProfileFile("chrome-work"), p.BackupDir("chrome-personal"),
		filepath.Join(p.Data, "extension.pem"))
	mustExist(t, "chrome purge", p.ProfileFile("firefox-alpha"), p.ProfileFile("firefox-beta"), p.BackupDir("firefox-alpha"),
		p.ConfigFile(), p.LogFile())
	if !strings.Contains(out.String(), "Kept") || !strings.Contains(out.String(), "firefox-alpha") {
		t.Fatalf("output does not say what it kept:\n%s", out.String())
	}

	out.Reset()
	if code := run([]string{"uninstall", "--browser", "firefox", "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	for _, n := range []string{"alpha", "beta"} {
		if user, _ := os.ReadFile(filepath.Join(root, "x"+n+"."+n, "user.js")); !bytes.Contains(user, []byte(firefox.ResetPrefs[firefox.Prefs[0]])) {
			t.Errorf("%s: user.js not put back: %q", n, user)
		}
	}
	mustBeGone(t, "last purge", p.ConfigFile(), p.Data, p.State)
}

// Install makes every profile's database before it writes into any
// profile, so a write that fails part way never leaves a profile with its
// add-on checks off and no database for uninstall to find it by.
func TestFirefoxInstallFailingPartWayCanBeUninstalled(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(root, "xbeta.beta", "user.js")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	alpha := filepath.Join(root, "xalpha.alpha", "user.js")
	if user, _ := os.ReadFile(alpha); !bytes.Contains(user, []byte(firefox.Prefs[0])) {
		t.Fatalf("alpha was not written first: %q", user)
	}
	mustExist(t, "failed install", p.ProfileFile("firefox-alpha"), p.ProfileFile("firefox-beta"), p.ConfigFile())
	if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("uninstall: exit %d: %s%s", code, out.String(), errb.String())
	}
	if user, _ := os.ReadFile(alpha); !bytes.Contains(user, []byte(firefox.ResetPrefs[firefox.Prefs[0]])) {
		t.Fatalf("alpha's user.js not put back: %q", user)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// neverRun lists profile name in root's profiles.ini with a directory but
// no compatibility.ini, like the second profile LibreWolf makes.
func neverRun(t *testing.T, root, name string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(root, "x"+name+"."+name), 0o700))
	f, err := os.OpenFile(filepath.Join(root, "profiles.ini"), os.O_APPEND|os.O_WRONLY, 0)
	must(t, err)
	_, err = fmt.Fprintf(f, "[Profile9]\nName=%s\nIsRelative=1\nPath=x%s.%s\n", name, name, name)
	must(t, err)
	must(t, f.Close())
}

func TestInstallRefusesANamedProfileThatHasNeverRun(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	neverRun(t, root, "gamma")
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "firefox", "--profile", "xalpha.alpha", "--profile", "xgamma.gamma"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "xgamma.gamma: it has never run") {
		t.Fatalf("stderr %q", errb.String())
	}
	mustBeGone(t, "refused install", filepath.Join(root, "xalpha.alpha", "user.js"), p.ProfileFile("firefox-alpha"))
}

// installedID is the extension id of the key install made.
func installedID(t *testing.T, p db.Paths) string {
	t.Helper()
	key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem"))
	must(t, err)
	return chrome.ExtensionID(chrome.PublicKeyDER(key))
}

// installChromium runs install's first pass for each browser, each in a
// data directory of its own with profiles Default ("Personal") and
// Profile 1 ("Work").
func installChromium(t *testing.T, browsers ...string) {
	t.Helper()
	for _, b := range browsers {
		var out, errb bytes.Buffer
		if code := run([]string{"install", "--browser", b, "--user-data-dir", chromeData(t), "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("install %s: exit %d: %s%s", b, code, out.String(), errb.String())
		}
	}
}

func TestInstallIntoBraveThenBind(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := fakeSystem(t)
	calls := fakeChromeOnPath(t, p)
	udd := chromeData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Restart Brave") || !strings.Contains(out.String(), "mirrormark install --browser brave again") {
		t.Fatalf("output:\n%s", out.String())
	}
	id := installedID(t, p)
	mustExist(t, "install", filepath.Join(root, chrome.Brave.ExternalFile(id)), filepath.Join(root, chrome.HostManifestPath()))
	mustBeGone(t, "install", filepath.Join(root, chrome.Chrome.ExternalFile(id)))
	for dir, id := range map[string]string{"Default": "brave-personal", "Profile 1": "brave-work"} {
		d, err := db.LoadDB(p.ProfileFile(id))
		if err != nil || d.Profile.Browser != "brave" || d.Profile.Label != "brave/"+strings.TrimPrefix(id, "brave-") || d.Profile.Path != filepath.Join(udd, dir) {
			t.Fatalf("%s: %+v, %v", id, d, err)
		}
	}

	chromiumReady(t, filepath.Join(udd, "Default"), id)
	chromiumReady(t, filepath.Join(udd, "Profile 1"), id)
	out.Reset()
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd, "--profile", "Default", "--profile", "1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "bound: Profile 1 -> brave-work\n") {
		t.Fatalf("output:\n%s", out.String())
	}
	if c, _ := os.ReadFile(calls); !bytes.Contains(c, []byte("--user-data-dir="+udd+" --profile-directory=Profile 1 chrome-extension://"+id+"/bind.html#brave-work."+tokenOf(t, p, "brave-work")+"\n")) {
		t.Fatalf("calls:\n%s", c)
	}
}

// Install for a browser that has never run here (no Local State) stops
// before it writes anything, so it creates nothing under that browser's
// system directory.
func TestInstallIntoAbsentBraveWritesNothing(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := fakeSystem(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "brave", "--profile", "Default"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "Brave") || len(*calls) != 0 {
		t.Fatalf("stderr %q, sudo calls %q", errb.String(), *calls)
	}
	mustBeGone(t, "absent brave", filepath.Join(root, "opt", "brave.com"), filepath.Join(root, chrome.HostManifestPath()), p.Data)
}

// The CRX and the host manifest serve every Chromium browser: they go with
// the last one's external file, whichever goes first.
func TestUninstallingOneChromiumBrowserKeepsTheSharedFiles(t *testing.T) {
	for _, c := range []struct{ gone, stays chrome.Browser }{{chrome.Brave, chrome.Chrome}, {chrome.Chrome, chrome.Brave}} {
		t.Run(c.gone.Config, func(t *testing.T) {
			skipIfRoot(t)
			p := testHome(t)
			root, _ := fakeSystem(t)
			fakeChromeOnPath(t, p)
			installChromium(t, "chrome", "brave")
			id := installedID(t, p)
			host, crx := filepath.Join(root, chrome.HostManifestPath()), filepath.Join(p.Data, "mirrormark.crx")
			var out, errb bytes.Buffer
			if code := run([]string{"uninstall", "--browser", c.gone.Config}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, "first uninstall", filepath.Join(root, c.gone.ExternalFile(id)))
			mustExist(t, "first uninstall", filepath.Join(root, c.stays.ExternalFile(id)), host, crx, filepath.Join(p.Data, "extension"), unitPath(p))
			if !strings.Contains(out.String(), "Kept the mirrormark service, which "+c.stays.Name+" still uses.") {
				t.Fatalf("output:\n%s", out.String())
			}
			if code := run([]string{"uninstall", "--browser", c.stays.Config}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, "second uninstall", filepath.Join(root, c.stays.ExternalFile(id)), host, crx, filepath.Join(p.Data, "extension"), unitPath(p))
		})
	}
}

// The key fixes the id both browsers use; purging either keeps it while
// the other is installed.
func TestPurgingOneChromiumBrowserKeepsTheKey(t *testing.T) {
	for _, c := range []struct {
		gone, stays chrome.Browser
		goneIDs     []string
		staysID     string
	}{
		{chrome.Brave, chrome.Chrome, []string{"brave-personal", "brave-work"}, "chrome-personal"},
		{chrome.Chrome, chrome.Brave, []string{"chrome-personal", "chrome-work"}, "brave-personal"},
	} {
		t.Run(c.gone.Config, func(t *testing.T) {
			skipIfRoot(t)
			p := testHome(t)
			fakeSystem(t)
			fakeChromeOnPath(t, p)
			installChromium(t, "chrome", "brave")
			var out, errb bytes.Buffer
			if code := run([]string{"uninstall", "--browser", c.gone.Config, "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, c.gone.Config+" purge", p.ProfileFile(c.goneIDs[0]), p.ProfileFile(c.goneIDs[1]))
			mustExist(t, c.gone.Config+" purge", filepath.Join(p.Data, "extension.pem"), p.ProfileFile(c.staysID), p.ConfigFile())
			if code := run([]string{"uninstall", "--browser", c.stays.Config, "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, "last purge", p.ConfigFile(), p.Data, p.State)
		})
	}
}

// A Chromium browser's database left after its plain uninstall still
// keeps the key: installing that browser again must give the same id.
func TestBravePurgeKeepsTheKeyWhileChromesDatabasesAreLeft(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	fakeChromeOnPath(t, p)
	installChromium(t, "chrome", "brave")
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"uninstall"}, {"uninstall", "--browser", "brave", "--purge"}} {
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%q: exit %d: %s%s", args, code, out.String(), errb.String())
		}
	}
	mustExist(t, "brave purge", filepath.Join(p.Data, "extension.pem"), p.ProfileFile("chrome-personal"))
}

// A data directory left behind (say by removing Brave's package) is not an
// installed Brave: install writes nothing into a browser it cannot start.
func TestInstallRefusesABrowserThatIsNotInstalled(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := fakeSystem(t)
	udd := chromeData(t)
	t.Setenv("PATH", t.TempDir()) // no brave-browser, not even testHome's guard
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd, "--profile", "Default"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "Brave is not installed (no brave-browser on PATH)") || len(*calls) != 0 {
		t.Fatalf("stderr %q, sudo calls %q", errb.String(), *calls)
	}
	mustBeGone(t, "brave not installed", filepath.Join(root, "opt", "brave.com"), filepath.Join(root, chrome.HostManifestPath()), p.Data)
}

// When uninstall cannot tell whether another browser's external file is
// there, it keeps the files that browser may still need.
func TestUninstallKeepsTheSharedFilesWhenItCannotTell(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := fakeSystem(t)
	fakeChromeOnPath(t, p)
	installChromium(t, "chrome", "brave")
	id := installedID(t, p)
	must(t, os.Chmod(filepath.Dir(filepath.Join(root, chrome.Chrome.ExternalFile(id))), 0))
	var out, errb bytes.Buffer
	if code := run([]string{"uninstall", "--browser", "brave"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "uninstall", filepath.Join(root, chrome.Brave.ExternalFile(id)))
	mustExist(t, "uninstall", filepath.Join(root, chrome.HostManifestPath()), filepath.Join(p.Data, "mirrormark.crx"),
		filepath.Join(p.Data, "extension"), filepath.Join(root, chrome.Chrome.ExternalFile(id)))
	if strings.Count(errb.String(), "cannot tell whether Chrome still uses mirrormark") != 1 {
		t.Fatalf("stderr:\n%s", errb.String())
	}
}

// Uninstalling a browser mirrormark was never installed in says so, and
// leaves the other browser's install alone.
func TestUninstallFromABrowserNeverInstalledSaysSo(t *testing.T) {
	skipIfRoot(t)
	for _, chromeInstalled := range []bool{false, true} {
		t.Run(fmt.Sprint("chrome installed ", chromeInstalled), func(t *testing.T) {
			p := testHome(t)
			root, _ := fakeSystem(t)
			fakeChromeOnPath(t, p)
			if chromeInstalled {
				installChromium(t, "chrome")
			}
			var out, errb bytes.Buffer
			if code := run([]string{"uninstall", "--browser", "brave"}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			if strings.Contains(out.String(), "Brave removes mirrormark") || !strings.Contains(out.String(), "mirrormark is not installed in Brave") {
				t.Fatalf("output:\n%s", out.String())
			}
			if chromeInstalled {
				id := installedID(t, p)
				mustExist(t, "uninstall", filepath.Join(root, chrome.Chrome.ExternalFile(id)), filepath.Join(root, chrome.HostManifestPath()),
					filepath.Join(p.Data, "mirrormark.crx"), filepath.Join(p.Data, "extension"))
			}
		})
	}
}

func TestInstallIntoLibreWolfThenBind(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := librewolfData(t, p)
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "librewolf"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Restart LibreWolf") || !strings.Contains(out.String(), "unsigned") {
		t.Fatalf("output:\n%s", out.String())
	}
	for _, n := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "x"+n+"."+n)
		mustExist(t, "install", firefox.XPIPath(dir))
		if user, _ := os.ReadFile(filepath.Join(dir, "user.js")); string(user) != strings.Join(firefox.Prefs, "\n")+"\n" {
			t.Fatalf("user.js %q", user)
		}
		d, err := db.LoadDB(p.ProfileFile("librewolf-" + n))
		if err != nil || d.Profile.Path != dir || d.Profile.Browser != "librewolf" || d.Profile.Label != "librewolf/"+n {
			t.Fatalf("%s: %+v %v", n, d, err)
		}
	}
	host, _ := firefox.HostManifestPath()
	mustExist(t, "install", host)

	for _, n := range []string{"alpha", "beta"} {
		geckoReady(t, filepath.Join(root, "x"+n+"."+n), "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4"+n[:1])
	}
	out.Reset()
	if code := run([]string{"install", "--browser", "librewolf", "--profile", "xbeta.beta"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "bound: xbeta.beta -> librewolf-beta\n") {
		t.Fatalf("output:\n%s", out.String())
	}
	if c, _ := os.ReadFile(calls); !bytes.Contains(c, []byte("about:blank moz-extension://5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b/bind.html#librewolf-beta."+tokenOf(t, p, "librewolf-beta"))) {
		t.Fatalf("calls:\n%s", c)
	}
}

// Each Gecko browser takes only its own profiles, and writes nothing when
// it refuses.
func TestEachGeckoBrowserTakesOnlyItsOwnProfiles(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	lwRoot, _ := librewolfData(t, p)
	ffRoot, _ := firefoxData(t, p, "aurora")
	for _, c := range []struct{ browser, root, want string }{
		{"librewolf", ffRoot, "not LibreWolf"},
		{"firefox", lwRoot, "this profile belongs to LibreWolf; use --browser librewolf"},
	} {
		var out, errb bytes.Buffer
		if code := run(append([]string{"install", "--browser", c.browser, "--user-data-dir", c.root}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 1 {
			t.Fatalf("%s: exit %d: %s%s", c.browser, code, out.String(), errb.String())
		}
		if !strings.Contains(errb.String(), c.want) {
			t.Fatalf("%s: stderr %q", c.browser, errb.String())
		}
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 0 {
		t.Fatalf("made databases %v", dbs)
	}
	host, _ := firefox.HostManifestPath()
	mustBeGone(t, "refused install", host, filepath.Join(ffRoot, "xalpha.alpha", "user.js"), filepath.Join(lwRoot, "xalpha.alpha", "user.js"))
}

// installGecko installs mirrormark into Developer Edition's and LibreWolf's
// profiles.
func installGecko(t *testing.T) (p db.Paths, ffRoot, lwRoot string) {
	skipIfRoot(t)
	p = testHome(t)
	lwRoot, _ = librewolfData(t, p)
	ffRoot, _ = firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	for _, b := range []string{"firefox", "librewolf"} {
		if code := run(append([]string{"install", "--browser", b}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("install %s: exit %d: %s%s", b, code, out.String(), errb.String())
		}
	}
	return p, ffRoot, lwRoot
}

// Developer Edition and LibreWolf read one host manifest: it goes with the
// last browser whose profiles still hold the add-on.
func TestUninstallingOneGeckoBrowserKeepsTheSharedHostManifest(t *testing.T) {
	for _, c := range []struct{ first, firstName, last string }{
		{"librewolf", "Firefox Developer Edition", "firefox"},
		{"firefox", "LibreWolf", "librewolf"},
	} {
		t.Run(c.first+" first", func(t *testing.T) {
			p, ffRoot, lwRoot := installGecko(t)
			roots := map[string]string{"firefox": ffRoot, "librewolf": lwRoot}
			host, _ := firefox.HostManifestPath()
			var out, errb bytes.Buffer
			if code := run([]string{"uninstall", "--browser", c.first}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, c.first+" uninstall", firefox.XPIPath(filepath.Join(roots[c.first], "xalpha.alpha")))
			mustExist(t, c.first+" uninstall", host, firefox.XPIPath(filepath.Join(roots[c.last], "xalpha.alpha")), unitPath(p))
			if user, _ := os.ReadFile(filepath.Join(roots[c.last], "xalpha.alpha", "user.js")); !bytes.Contains(user, []byte(firefox.Prefs[0])) {
				t.Fatalf("%s's user.js was reset: %q", c.last, user)
			}
			if !strings.Contains(out.String(), c.firstName+"'s profiles still use") || !strings.Contains(out.String(), "Kept the mirrormark service, which "+c.firstName+" still uses.") {
				t.Fatalf("output:\n%s", out.String())
			}
			out.Reset()
			if code := run([]string{"uninstall", "--browser", c.last}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, c.last+" uninstall", host, unitPath(p))
			if strings.Contains(out.String(), "still use") {
				t.Fatalf("output:\n%s", out.String())
			}
		})
	}
}

// When uninstall cannot tell whether another Gecko browser's profile still
// holds the add-on, it keeps the host manifest and says why. No Developer
// Edition profile can be checked, so only that keeps it.
func TestGeckoUninstallKeepsTheHostManifestWhenItCannotCheck(t *testing.T) {
	_, ffRoot, _ := installGecko(t)
	for _, n := range []string{"alpha", "beta"} {
		ext := filepath.Join(ffRoot, "x"+n+"."+n, "extensions")
		must(t, os.RemoveAll(ext))
		must(t, os.WriteFile(ext, nil, 0o600)) // the XPI's path now cannot be checked
	}
	var out, errb bytes.Buffer
	if code := run([]string{"uninstall", "--browser", "librewolf"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	host, _ := firefox.HostManifestPath()
	mustExist(t, "uninstall", host)
	if strings.Count(errb.String(), "cannot tell whether Firefox Developer Edition still uses mirrormark") != 1 {
		t.Fatalf("stderr %q", errb.String())
	}
}

// installChromeAndFirefox installs Chrome and a Developer Edition
// profile, so the one server serves both families.
func installChromeAndFirefox(t *testing.T) (p db.Paths, calls *[]string) {
	t.Helper()
	skipIfRoot(t)
	p = testHome(t)
	fakeSystem(t)
	fakeChromeOnPath(t, p)
	firefoxData(t, p, "aurora")
	calls = fakeSystemctl(t, "")
	installChromium(t, "chrome")
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustExist(t, "install", unitPath(p))
	if want := []string{"daemon-reload", "enable mirrormark.service", "restart mirrormark.service"}; !slices.Equal((*calls)[len(*calls)-3:], want) {
		t.Fatalf("calls %q", *calls)
	}
	*calls = nil
	return p, calls
}

// Uninstalling one family keeps the server while the other still has
// mirrormark, whichever goes first.
func TestUninstallKeepsTheServiceWhileTheOtherFamilyUsesIt(t *testing.T) {
	for _, c := range []struct{ first, firstName, last, lastName string }{
		{"chrome", "Chrome", "firefox", "Firefox Developer Edition"},
		{"firefox", "Firefox Developer Edition", "chrome", "Chrome"},
	} {
		t.Run(c.first+" first", func(t *testing.T) {
			p, calls := installChromeAndFirefox(t)
			var out, errb bytes.Buffer
			if code := run([]string{"uninstall", "--browser", c.first}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustExist(t, c.first+" uninstall", unitPath(p))
			if len(*calls) != 0 || !strings.Contains(out.String(), "Kept the mirrormark service, which "+c.lastName+" still uses.") {
				t.Fatalf("calls %q, output:\n%s", *calls, out.String())
			}
			out.Reset()
			if code := run([]string{"uninstall", "--browser", c.last}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, c.last+" uninstall", unitPath(p))
			if want := []string{"disable --now mirrormark.service", "daemon-reload"}; !slices.Equal(*calls, want) || !strings.Contains(out.String(), "Stopped and removed the mirrormark service.") {
				t.Fatalf("calls %q, output:\n%s", *calls, out.String())
			}
		})
	}
}

// Without the key, uninstall cannot find Chrome's external file by its id.
// Chrome then still counts while it has a database or an external file
// that names mirrormark's CRX, and an unreadable key counts as a user.
func TestGeckoUninstallKeepsTheServiceWhenTheKeyIsGone(t *testing.T) {
	for _, c := range []struct {
		name    string
		breakIt func(t *testing.T, p db.Paths)
		kept    bool
		stderr  string
	}{
		{"database left", func(t *testing.T, p db.Paths) {
			must(t, os.Remove(filepath.Join(p.Data, "extension.pem")))
		}, true, ""},
		{"external file left", func(t *testing.T, p db.Paths) {
			must(t, os.Remove(filepath.Join(p.Data, "extension.pem")))
			for _, d := range []string{"chrome-personal", "chrome-work"} {
				must(t, os.Remove(p.ProfileFile(d)))
			}
		}, true, ""},
		{"key unreadable", func(t *testing.T, p db.Paths) {
			must(t, os.WriteFile(filepath.Join(p.Data, "extension.pem"), []byte("not a key"), 0o600))
		}, true, "cannot tell whether a Chromium browser still uses mirrormark"},
		{"nothing left", func(t *testing.T, p db.Paths) {
			id := installedID(t, p)
			must(t, os.Remove(filepath.Join(p.Data, "extension.pem")))
			for _, d := range []string{"chrome-personal", "chrome-work"} {
				must(t, os.Remove(p.ProfileFile(d)))
			}
			must(t, runSudo(nil, nil, nil, "rm", "-f", "--", filepath.Join(sysroot, chrome.Chrome.ExternalFile(id))))
		}, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _ := installChromeAndFirefox(t)
			c.breakIt(t, p)
			var out, errb bytes.Buffer
			if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			_, err := os.Stat(unitPath(p))
			if kept := err == nil; kept != c.kept {
				t.Fatalf("unit kept %v, want %v; output:\n%s%s", kept, c.kept, out.String(), errb.String())
			}
			if c.kept && c.stderr == "" && !strings.Contains(out.String(), "Kept the mirrormark service, which Chrome still uses.") {
				t.Fatalf("output:\n%s", out.String())
			}
			if !strings.Contains(errb.String(), c.stderr) {
				t.Fatalf("stderr %q, want %q", errb.String(), c.stderr)
			}
		})
	}
}

// A Chromium uninstall without the key cannot tell who still uses the
// service, so it leaves it and says how to remove it by hand.
func TestChromiumUninstallWithoutTheKeyNamesTheService(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	firefoxData(t, p, "aurora")
	calls := fakeSystemctl(t, "")
	var out, errb bytes.Buffer
	if code := run(append([]string{"install", "--browser", "firefox"}, geckoBoth...), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	*calls = nil
	out.Reset()
	if code := run([]string{"uninstall"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustExist(t, "uninstall", unitPath(p))
	if len(*calls) != 0 || !strings.Contains(out.String(), "systemctl --user disable --now mirrormark.service") || !strings.Contains(out.String(), unitPath(p)) {
		t.Fatalf("calls %q, output:\n%s", *calls, out.String())
	}
}

// Without systemd, install still writes the unit and says how to run the
// server by hand; uninstall removes the unit and says to stop that server.
func TestServiceWithoutSystemd(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	fakeChromeOnPath(t, p)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", chromeData(t), "--profile", "Default"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Keep this running instead") || !strings.Contains(out.String(), " serve\n") {
		t.Fatalf("output:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"uninstall"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "uninstall", unitPath(p))
	if !strings.Contains(out.String(), "Removed "+unitPath(p)+". If you run mirrormark serve yourself, stop it.") {
		t.Fatalf("output:\n%s", out.String())
	}
}
