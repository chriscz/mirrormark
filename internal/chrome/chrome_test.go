package chrome

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestProfilesFromLocalState(t *testing.T) {
	udd := t.TempDir()
	os.WriteFile(filepath.Join(udd, "Local State"), []byte(`{"profile":{"info_cache":{
		"Profile 1":{"name":"Work"},"Default":{"name":"Personal"}}}}`), 0o600)
	got, err := Chrome.Profiles(udd)
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{{Dir: "Default", Name: "Personal"}, {Dir: "Profile 1", Name: "Work"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestProfilesSortByIDWithNumbersAsNumbers(t *testing.T) {
	udd := t.TempDir()
	os.WriteFile(filepath.Join(udd, "Local State"), []byte(`{"profile":{"info_cache":{
		"Profile 10":{"name":"j"},"Profile 2":{"name":"b"},"Default":{"name":"d"},"Profile 1":{"name":"a"}}}}`), 0o600)
	ps, err := Chrome.Profiles(udd)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.ID())
	}
	if want := []string{"Default", "Profile 1", "Profile 2", "Profile 10"}; !slices.Equal(ids, want) {
		t.Fatalf("order %q, want %q", ids, want)
	}
}

func TestCompareIDs(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"Profile 2", "Profile 10", -1}, {"Profile 10", "Profile 9", 1}, {"Default", "Profile 1", -1},
		{"abc.default", "abc.default", 0}, {"a01", "a1", 1}, {"x9.y", "x10.a", -1},
	} {
		if got := CompareIDs(c.a, c.b); got != c.want {
			t.Errorf("CompareIDs(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestProfilesWithoutLocalStateFails(t *testing.T) {
	if _, err := Chrome.Profiles(t.TempDir()); err == nil {
		t.Fatal("no error for a data directory Chrome never used")
	}
}

func TestProfilesRejectsEmptyOrBrokenLocalState(t *testing.T) {
	for _, data := range []string{`{}`, `{"profile":`} {
		udd := t.TempDir()
		os.WriteFile(filepath.Join(udd, "Local State"), []byte(data), 0o600)
		if _, err := Chrome.Profiles(udd); err == nil {
			t.Errorf("no error for Local State %s", data)
		}
	}
}

func TestDefaultUserDataDirFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if got, _ := Chrome.DefaultUserDataDir(); got != "/x/config/google-chrome" {
		t.Fatalf("got %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	t.Setenv("HOME", "/h")
	if got, _ := Chrome.DefaultUserDataDir(); got != "/h/.config/google-chrome" {
		t.Fatalf("got %s", got)
	}
}

// The public key and id of the throwaway key used in the install spike;
// Chrome itself reported this id for it.
const spikeKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA1qIVj5Wd5Sc4m9P0srXXXzh/A14mHIWQ+sWk/QuMCVCO0OM9Jym1a+KhhYEdUugY6YjaT505BgTt32PhPtnZOCAIDW3aihojoFCdidE7iHOKYMRu911k7bBOpaq/oXDdKoZO8xtg42OXaewkyHjS5KByoaIZSrWxjDT1NgCmwVi4f/Kk5lu4t4EYtD0mc+rK6288HE0tM1jIjIvW01/bAuDa5vcEwj2hVf/33yyHrm2XKVqbChnOkp4FM381bFQzYtOeNq8ec8TZTBPJEMc36OvEjurGiPCxxTOskc8bJ/4R+phVR2ADOLYu3VoiTyoFX/aYS2KJKQdJu2CX2/v98wIDAQAB"

func TestExtensionIDMatchesChrome(t *testing.T) {
	der, _ := base64.StdEncoding.DecodeString(spikeKey)
	if got := ExtensionID(der); got != "pjjjaeommfpeolloibnafkcmkindkagl" {
		t.Fatalf("id %s", got)
	}
}

func TestKeyIsCreatedOnceWithMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension.pem")
	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateKey(path)
	if err != nil || !k1.Equal(k2) {
		t.Fatalf("second load gave another key: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestLoadOrCreateKeyKeepsABrokenKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension.pem")
	os.WriteFile(path, []byte("not a key"), 0o600)
	if _, err := LoadOrCreateKey(path); err == nil {
		t.Fatal("a broken key file was accepted")
	}
	if data, _ := os.ReadFile(path); string(data) != "not a key" {
		t.Fatal("a broken key file was replaced, which would change the extension id")
	}
}

// field reads one protobuf length-delimited field.
func field(b []byte) (num int, val, rest []byte) {
	tag, n := binary.Uvarint(b)
	l, m := binary.Uvarint(b[n:])
	start := n + m
	return int(tag >> 3), b[start : start+int(l)], b[start+int(l):]
}

func TestCRXIsSignedAndHoldsTheExtension(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"x"}`), 0o644)
	key, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "k.pem"))
	out := filepath.Join(t.TempDir(), "x.crx")
	if err := WriteCRX(dir, key, out); err != nil {
		t.Fatal(err)
	}
	crx, _ := os.ReadFile(out)
	if string(crx[:4]) != "Cr24" || binary.LittleEndian.Uint32(crx[4:]) != 3 {
		t.Fatal("bad magic or version")
	}
	hlen := binary.LittleEndian.Uint32(crx[8:])
	header, archive := crx[12:12+hlen], crx[12+hlen:]

	num, proof, rest := field(header)
	if num != 2 {
		t.Fatalf("first header field %d, want 2 (sha256_with_rsa)", num)
	}
	_, pub, p2 := field(proof)
	_, sig, _ := field(p2)
	num, signed, _ := field(rest)
	if num != 10000 {
		t.Fatalf("second header field %d, want 10000 (signed_header_data)", num)
	}
	if !bytes.Equal(pub, PublicKeyDER(key)) {
		t.Fatal("header carries another public key")
	}
	// The signed data holds the first 16 bytes of the key's SHA-256: the
	// extension id before it is written as letters.
	_, crxID, _ := field(signed)
	sum := sha256.Sum256(pub)
	if !bytes.Equal(crxID, sum[:16]) {
		t.Fatal("signed crx_id is not the key's id")
	}
	msg := append([]byte("CRX3 SignedData\x00"), binary.LittleEndian.AppendUint32(nil, uint32(len(signed)))...)
	msg = append(append(msg, signed...), archive...)
	h := sha256.Sum256(msg)
	pk, _ := x509.ParsePKIXPublicKey(pub)
	rsaPub, ok := pk.(*rsa.PublicKey)
	if !ok {
		t.Fatal("not an RSA key")
	}
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, h[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "manifest.json" {
		t.Fatalf("archive: %v", err)
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0o644 {
		t.Fatalf("Chrome must be able to read the CRX: %v", info.Mode().Perm())
	}
}

func TestPrepareKeepsTheIDAndMatchesTheManifestKey(t *testing.T) {
	data := t.TempDir()
	e1, err := Prepare(data)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(e1.Dir, "stale.js"), nil, 0o644)
	e2, err := Prepare(data)
	if err != nil {
		t.Fatal(err)
	}
	if e1 != e2 || e1.Version == "" || e1.Dir != filepath.Join(data, "extension") || e1.CRX != filepath.Join(data, "mirrormark.crx") {
		t.Fatalf("%+v then %+v", e1, e2)
	}
	if _, err := os.Stat(filepath.Join(e2.Dir, "stale.js")); err == nil {
		t.Fatal("a file from an earlier unpack survived")
	}
	if _, err := os.Stat(e2.CRX); err != nil {
		t.Fatal(err)
	}
	// Chrome derives the id of a Load unpacked copy from the manifest key.
	var m struct{ Key string }
	raw, _ := os.ReadFile(filepath.Join(e2.Dir, "manifest.json"))
	json.Unmarshal(raw, &m)
	der, _ := base64.StdEncoding.DecodeString(m.Key)
	if ExtensionID(der) != e2.ID {
		t.Fatalf("manifest key gives %s, not %s", ExtensionID(der), e2.ID)
	}
}

func TestUnpackIntoAnotherFolderKeepsTheID(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(t.TempDir(), "ext")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("keep"), 0o644)
	e, err := Unpack(data, dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := Prepare(data)
	if e.ID != p.ID || e.Dir != dir {
		t.Fatalf("%+v vs %+v", e, p)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "mine.txt")); string(data) != "keep" {
		t.Fatal("unpack deleted a file it did not write in a folder the user chose")
	}
}

func TestFilesCurrentAndWrite(t *testing.T) {
	dir := t.TempDir()
	fs := []File{{Path: filepath.Join(dir, "a", "x.json"), Data: []byte("1")}}
	if FilesCurrent(fs) {
		t.Fatal("missing file counted as current")
	}
	if err := WriteFiles(fs); err != nil {
		t.Fatal(err)
	}
	if !FilesCurrent(fs) {
		t.Fatal("written file not current")
	}
	if info, _ := os.Stat(filepath.Join(dir, "a")); info.Mode().Perm() != 0o755 {
		t.Fatalf("Chrome must be able to read the directory: %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(fs[0].Path); info.Mode().Perm() != 0o644 {
		t.Fatalf("Chrome must be able to read the file: %v", info.Mode().Perm())
	}
	if FilesCurrent([]File{{Path: fs[0].Path, Data: []byte("2")}}) {
		t.Fatal("changed file counted as current")
	}
	if err := RemoveFiles([]string{fs[0].Path, fs[0].Path + ".missing"}); err != nil {
		t.Fatal(err)
	}
	if FilesCurrent(fs) {
		t.Fatal("removed file counted as current")
	}
}

// Rewriting a file the user keeps elsewhere and links in, as dotfiles often
// are, writes through the link; a file the user made private stays private.
func TestWriteFilesKeepsALinkAndTheMode(t *testing.T) {
	dir, dotfiles := t.TempDir(), t.TempDir()
	target, link := filepath.Join(dotfiles, "user.js"), filepath.Join(dir, "user.js")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private.js")
	if err := os.WriteFile(private, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles([]File{{link, []byte("new")}, {private, []byte("new")}}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v %v", fi, err)
	}
	for _, f := range []string{target, private} {
		data, _ := os.ReadFile(f)
		fi, _ := os.Stat(f)
		if string(data) != "new" || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %q %v", f, data, fi.Mode().Perm())
		}
	}
	// A link to nothing is left alone: writing it would replace the link.
	dangling := filepath.Join(dir, "dangling.js")
	if err := os.Symlink(filepath.Join(dotfiles, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles([]File{{dangling, []byte("new")}}); err == nil {
		t.Fatal("wrote over a dangling link")
	}
	if fi, err := os.Lstat(dangling); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dangling link replaced: %v %v", fi, err)
	}
}

func TestSystemFiles(t *testing.T) {
	e := Extension{ID: "pjjjaeommfpeolloibnafkcmkindkagl", Version: "0.1.0", CRX: "/h/.local/share/mirrormark/mirrormark.crx"}
	fs := Chrome.SystemFiles(e, "/usr/local/bin/mirrormark")
	if fs[0].Path != "/usr/share/google-chrome/extensions/pjjjaeommfpeolloibnafkcmkindkagl.json" ||
		!bytes.Contains(fs[0].Data, []byte(`"external_version": "0.1.0"`)) {
		t.Fatalf("external file %s %s", fs[0].Path, fs[0].Data)
	}
	var ext map[string]string
	if err := json.Unmarshal(fs[0].Data, &ext); err != nil || len(ext) != 2 || ext["external_crx"] != e.CRX {
		t.Fatalf("external file %s: %v", fs[0].Data, err)
	}
	if fs[1].Path != "/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json" ||
		!bytes.Contains(fs[1].Data, []byte(`"chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/"`)) {
		t.Fatalf("host manifest %s %s", fs[1].Path, fs[1].Data)
	}
	var host hostManifestJSON
	if err := json.Unmarshal(fs[1].Data, &host); err != nil || host.Name != "com.github.chriscz.mirrormark" ||
		host.Path != "/usr/local/bin/mirrormark" || host.Type != "stdio" || len(host.AllowedOrigins) != 1 {
		t.Fatalf("host manifest %s: %v", fs[1].Data, err)
	}
	if fs[0].Path != Chrome.ExternalFile(e.ID) || fs[1].Path != HostManifestPath() || len(fs) != 2 {
		t.Fatalf("paths %s %s", fs[0].Path, fs[1].Path)
	}
}

func TestLookup(t *testing.T) {
	if b, ok := Lookup("chrome"); !ok || b != Chrome {
		t.Fatalf("chrome: %+v %v", b, ok)
	}
	if _, ok := Lookup("firefox"); ok {
		t.Fatal("firefox is not a Chromium browser")
	}
	if Chrome.InstallCommand() != "mirrormark install" {
		t.Fatalf("Chrome's command %q", Chrome.InstallCommand())
	}
	if Chrome.ExtensionsPage != "chrome://extensions" {
		t.Fatalf("Chrome's extensions page %q", Chrome.ExtensionsPage)
	}
}

func TestBraveDescription(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if got, _ := Brave.DefaultUserDataDir(); got != "/x/config/BraveSoftware/Brave-Browser" {
		t.Fatalf("data dir %s", got)
	}
	if got := Brave.ExternalFile("pjjjaeommfpeolloibnafkcmkindkagl"); got != "/opt/brave.com/brave/extensions/pjjjaeommfpeolloibnafkcmkindkagl.json" {
		t.Fatalf("external file %s", got)
	}
	if b, ok := Lookup("brave"); !ok || b != Brave || Brave.InstallCommand() != "mirrormark install --browser brave" {
		t.Fatalf("lookup %+v %v", b, ok)
	}
	if Brave.ExtensionsPage != "brave://extensions" {
		t.Fatalf("extensions page %q", Brave.ExtensionsPage)
	}
}

// fakeChrome writes a script that logs its arguments, one call per line.
func fakeChrome(t *testing.T) (bin, log string) {
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "google-chrome")
	os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\n"), 0o755)
	return bin, log
}

func shortBindWait(t *testing.T) {
	old := BindWait
	BindWait = 200 * time.Millisecond
	t.Cleanup(func() { BindWait = old })
}

func TestOpenStartsOnceAndWaits(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	start := time.Now()
	ok, err := Open(fake, []string{"x", "y"}, func(time.Time) bool { return false })
	if err != nil || ok || time.Since(start) < BindWait {
		t.Fatalf("ok %v, err %v, after %v", ok, err, time.Since(start))
	}
	if data, _ := os.ReadFile(log); string(data) != "x y\n" {
		t.Fatalf("calls:\n%s", data)
	}
}

func TestBindOpensThePageOnce(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	udd := filepath.Join(t.TempDir(), "udd")
	err := Chrome.Bind(fake, udd, "Profile 1", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-work", func(time.Time) bool {
		data, _ := os.ReadFile(log)
		return len(data) > 0
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if want := "--user-data-dir=" + udd + " --profile-directory=Profile 1 chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/bind.html#chrome-work\n"; string(data) != want {
		t.Fatalf("calls:\n%s", data)
	}
}

func TestBindSaysWhenTheExtensionDoesNotReportIn(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	udd, _ := Brave.DefaultUserDataDir()
	err := Brave.Bind(fake, udd, "Default", "pjjjaeommfpeolloibnafkcmkindkagl", "brave-a", func(time.Time) bool { return false })
	if err == nil || err.Error() != "mirrormark did not report in within 200ms of opening the page; look at the tab Brave opened" {
		t.Fatalf("err %v", err)
	}
	// The default data directory is left implicit, as a user would type it.
	if data, _ := os.ReadFile(log); string(data) != "--profile-directory=Default chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/bind.html#brave-a\n" {
		t.Fatalf("calls:\n%s", data)
	}
}

func TestBindPassesTheAttemptStart(t *testing.T) {
	shortBindWait(t)
	fake, _ := fakeChrome(t)
	before := time.Now()
	var seen time.Time
	Chrome.Bind(fake, t.TempDir(), "Default", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-a", func(since time.Time) bool {
		seen = since
		return true
	})
	if seen.Before(before) || time.Since(seen) > time.Second {
		t.Fatalf("since %v, test started %v", seen, before)
	}
}

func TestBindReportsAChromeThatWillNotStart(t *testing.T) {
	err := Chrome.Bind(filepath.Join(t.TempDir(), "missing"), t.TempDir(), "Default", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-a", func(time.Time) bool { return true })
	if err == nil {
		t.Fatal("no error")
	}
}

func TestReadStateFromPreferences(t *testing.T) {
	const id = "pjjjaeommfpeolloibnafkcmkindkagl"
	entry := func(extra string) string {
		return `{"extensions":{"settings":{"` + id + `":{"path":"` + id + `/0.1.0_0","location":2,"manifest":{"version":"0.1.0"}` + extra + `}}}}`
	}
	for _, c := range []struct {
		name, prefs string // prefs "" means no Preferences file
		placed      bool
		want        ProfileState
	}{
		{"never run", "", true, ProfileState{State: NeverRun}},
		{"no entry, no file", `{"extensions":{"settings":{}}}`, false, ProfileState{State: NotInstalled}},
		{"no entry yet", `{}`, true, ProfileState{State: NotInstalledYet}},
		{"enabled", entry(`,"disable_reasons":[]`), true, ProfileState{State: Ready}},
		{"enabled again", entry(``), true, ProfileState{State: Ready}},
		{"turned off", entry(`,"disable_reasons":[1]`), true, ProfileState{State: TurnedOff}},
		{"turned off, old format", entry(`,"disable_reasons":1`), true, ProfileState{State: TurnedOff}},
		{"removed", `{"extensions":{"settings":{"` + id + `":{}},"external_uninstalls":["` + id + `"]}}`, true, ProfileState{State: Removed}},
		{"old build", entry(``), true, ProfileState{State: OldBuild, Version: "0.1.0"}},
		// Load unpacked after a Remove: the record of the Remove may stay.
		{"loaded again", `{"extensions":{"settings":{"` + id + `":{"path":"/home/u/mirrormark-extension","location":4,"manifest":{"version":"0.1.0"}}},"external_uninstalls":["` + id + `"]}}`, true, ProfileState{State: Ready}},
	} {
		dir := t.TempDir()
		if c.prefs != "" {
			os.WriteFile(filepath.Join(dir, "Preferences"), []byte(c.prefs), 0o600)
		}
		want := "0.1.0"
		if c.name == "old build" {
			want = "0.2.0"
		}
		got, err := ReadState(dir, id, want, c.placed)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestReadStateReportsABrokenPreferences(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Preferences"), []byte("{"), 0o600)
	if _, err := ReadState(dir, "pjjjaeommfpeolloibnafkcmkindkagl", "0.1.0", true); err == nil || !strings.Contains(err.Error(), "Preferences") {
		t.Fatalf("err %v", err)
	}
}

func TestProfileStateWords(t *testing.T) {
	for s, w := range map[ProfileState]string{
		{State: NeverRun}: "never run", {State: NotInstalled}: "not installed", {State: NotInstalledYet}: "not installed yet",
		{State: Removed}: "removed", {State: TurnedOff}: "turned off", {State: Refused}: "refused",
		{State: OldBuild, Version: "0.1.0"}: "old build 0.1.0", {State: Ready}: "ready",
	} {
		if s.String() != w {
			t.Errorf("%+v: %q, want %q", s, s.String(), w)
		}
	}
}
