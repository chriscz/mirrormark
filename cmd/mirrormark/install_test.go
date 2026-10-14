package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
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
	if got, err := choose(all, nil); err != nil || len(got) != 3 {
		t.Fatalf("default should be every profile: %+v, %v", got, err)
	}
	if got, _ := choose(all, []string{"Profile 1"}); len(got) != 1 || got[0].Dir != "Profile 1" {
		t.Fatalf("by directory: %+v", got)
	}
	if got, _ := choose(all, []string{"Personal", "Profile 2"}); len(got) != 2 || got[0].Dir != "Default" || got[1].Dir != "Profile 2" {
		t.Fatalf("by name: %+v", got)
	}
	if got, _ := choose(all, []string{"Default", "Personal"}); len(got) != 1 {
		t.Fatalf("one profile named twice: %+v", got)
	}
	if _, err := choose(all, []string{"Work"}); err == nil {
		t.Fatal("a name two profiles share was accepted")
	}
	if _, err := choose(all, []string{"Nope"}); err == nil {
		t.Fatal("unknown profile accepted")
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
// google-chrome first on PATH that fails, so no unit test starts the real
// Chrome; fakeChromeOnPath goes before it.
func testHome(t *testing.T) db.Paths {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "bin")
	os.Mkdir(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "google-chrome"), []byte("#!/bin/sh\necho 'unit tests must not start Chrome' >&2\nexit 1\n"), 0o755)
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
	for _, d := range []string{"usr", "etc"} {
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

// fakeChromeOnPath puts a google-chrome on PATH that does what the
// extension's hello does: touch the lock file of the database the bind URL
// names, unless $FAKE_NO_HELLO names that database. It logs each call.
// It sets the time from the clock rather than letting touch take the
// kernel's file time, which lags the clock by up to a tick and so can fall
// before the moment Bind started waiting.
func fakeChromeOnPath(t *testing.T, p db.Paths) (log string) {
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\nfor a; do url=$a; done\necho \"$@\" >> " + log + "\n" +
		"[ \"${url##*#}\" = \"$FAKE_NO_HELLO\" ] || touch -d \"@$(date +%s.%N)\" \"" + p.ProfilesDir() + "/${url##*#}.lock\"\n"
	os.WriteFile(filepath.Join(dir, "google-chrome"), []byte(script), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	old := chrome.BindWait
	chrome.BindWait = 500 * time.Millisecond
	t.Cleanup(func() { chrome.BindWait = old })
	return log
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
	if code := run([]string{"install", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Extension ID: ") || !strings.Contains(out.String(), "Restart Chrome") || errb.Len() != 0 {
		t.Fatalf("output:\n%s%s", out.String(), errb.String())
	}
	key, _ := chrome.LoadKey(filepath.Join(p.Data, "extension.pem"))
	id := chrome.ExtensionID(chrome.PublicKeyDER(key))
	paths := chrome.SystemFilePaths(id)
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
	// profile is bound and confirmed.
	out.Reset()
	if code := run([]string{"install", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 0 {
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
	if !bytes.Contains(calls, []byte("--profile-directory=Profile 1 chrome-extension://"+id+"/bind.html#chrome-work\n")) {
		t.Fatalf("chrome calls:\n%s", calls)
	}

	// --profile narrows the binding to one profile, by name or directory.
	out.Reset()
	os.Remove(chromeLog)
	if code := run([]string{"install", "--user-data-dir", udd, "--profile", "Work"}, strings.NewReader(""), &out, &errb); code != 0 {
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
	run([]string{"install", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb)
	// As if the extension were disabled in Default: no hello comes.
	t.Setenv("FAKE_NO_HELLO", "chrome-personal")
	out.Reset()
	if code := run([]string{"install", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "not bound: Default: ") || !strings.Contains(out.String(), "bound: Profile 1 -> chrome-work") {
		t.Fatalf("output:\n%s", out.String())
	}
	if calls, _ := os.ReadFile(log); bytes.Count(calls, []byte("#chrome-personal\n")) != 3 {
		t.Fatalf("chrome calls:\n%s", calls)
	}
}

func TestInstallStopsWhenSudoFails(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	runSudo = func(_ io.Reader, _, _ io.Writer, args ...string) error { return fs.ErrPermission }
	udd := chromeData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 1 {
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
	for _, dir := range []string{link, udd} {
		if code := run([]string{"install", "--user-data-dir", dir, "--profile", "Work"}, strings.NewReader(""), &out, &errb); code != 0 {
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
	for _, args := range [][]string{{"install", "--user-data-dir", chromeData(t)}, {"uninstall"}, {"unpack"}, {"unpack", t.TempDir()}} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb); code != 1 || !strings.Contains(errb.String(), "not as root") {
			t.Fatalf("%v: exit %d: %s", args, code, errb.String())
		}
	}
	if _, err := os.Stat(p.Data); err == nil || len(*calls) != 0 {
		t.Fatalf("changed something as root: %v, sudo %v", err, *calls)
	}
}

// Two installs with different keys share the host manifest's one name, so
// each replaces the other's. install goes ahead but says so.
func TestInstallWarnsWhenItReplacesAnotherExtensionsHostManifest(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := fakeSystem(t)
	fakeChromeOnPath(t, p)
	other := chrome.SystemFiles(chrome.Extension{ID: "pjjjaeommfpeolloibnafkcmkindkagl", Version: "1", CRX: "/x.crx"}, "/bin/x")[1]
	other.Path = filepath.Join(root, other.Path)
	runSudo(bytes.NewReader(other.Data), nil, nil, "install", "-D", "-m", "0644", "/dev/stdin", other.Path)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", chromeData(t)}, strings.NewReader(""), &out, &errb); code != 0 {
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
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--user-data-dir", chromeData(t), "--profile", "Nope"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "Profile 1 (Work)") || len(*calls) != 0 {
		t.Fatalf("stderr %q, sudo %v", errb.String(), *calls)
	}
}

func TestUninstallRemovesSystemFilesAndKeepsData(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, sudoCalls := fakeSystem(t)
	fakeChromeOnPath(t, p)
	udd := chromeData(t)
	var out, errb bytes.Buffer
	run([]string{"install", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb)
	key, _ := chrome.LoadKey(filepath.Join(p.Data, "extension.pem"))
	paths := chrome.SystemFilePaths(chrome.ExtensionID(chrome.PublicKeyDER(key)))

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
	other := chrome.SystemFiles(chrome.Extension{ID: "pjjjaeommfpeolloibnafkcmkindkagl", Version: "1", CRX: "/x.crx"}, "/bin/x")[1]
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
