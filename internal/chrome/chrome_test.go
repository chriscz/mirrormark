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
	got, err := Profiles(udd)
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{{Dir: "Default", Name: "Personal"}, {Dir: "Profile 1", Name: "Work"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestProfilesWithoutLocalStateFails(t *testing.T) {
	if _, err := Profiles(t.TempDir()); err == nil {
		t.Fatal("no error for a data directory Chrome never used")
	}
}

func TestProfilesRejectsEmptyOrBrokenLocalState(t *testing.T) {
	for _, data := range []string{`{}`, `{"profile":`} {
		udd := t.TempDir()
		os.WriteFile(filepath.Join(udd, "Local State"), []byte(data), 0o600)
		if _, err := Profiles(udd); err == nil {
			t.Errorf("no error for Local State %s", data)
		}
	}
}

func TestDefaultUserDataDirFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if got, _ := DefaultUserDataDir(); got != "/x/config/google-chrome" {
		t.Fatalf("got %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	t.Setenv("HOME", "/h")
	if got, _ := DefaultUserDataDir(); got != "/h/.config/google-chrome" {
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

func TestSystemFiles(t *testing.T) {
	e := Extension{ID: "pjjjaeommfpeolloibnafkcmkindkagl", Version: "0.1.0", CRX: "/h/.local/share/mirrormark/mirrormark.crx"}
	fs := SystemFiles(e, "/usr/local/bin/mirrormark")
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
	if paths := SystemFilePaths(e.ID); paths[0] != fs[0].Path || paths[1] != fs[1].Path {
		t.Fatalf("paths %v", paths)
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

func TestBindRetriesUntilConfirmed(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	udd := filepath.Join(t.TempDir(), "udd")
	err := Bind(fake, udd, "Profile 1", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-work", func(time.Time) bool {
		data, _ := os.ReadFile(log)
		return bytes.Count(data, []byte("\n")) >= 2
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	want := "--user-data-dir=" + udd + " --profile-directory=Profile 1 chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/bind.html#chrome-work\n"
	if string(data) != want+want {
		t.Fatalf("calls:\n%s", data)
	}
}

func TestBindGivesUpAfterThreeTries(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	udd, _ := DefaultUserDataDir()
	err := Bind(fake, udd, "Default", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-a", func(time.Time) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "chrome://extensions") {
		t.Fatalf("err %v", err)
	}
	data, _ := os.ReadFile(log)
	// The default data directory is left implicit, as a user would type it.
	want := "--profile-directory=Default chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/bind.html#chrome-a\n"
	if string(data) != want+want+want {
		t.Fatalf("calls:\n%s", data)
	}
}

func TestBindPassesTheAttemptStart(t *testing.T) {
	shortBindWait(t)
	fake, _ := fakeChrome(t)
	before := time.Now()
	var seen time.Time
	Bind(fake, t.TempDir(), "Default", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-a", func(since time.Time) bool {
		seen = since
		return true
	})
	if seen.Before(before) || time.Since(seen) > time.Second {
		t.Fatalf("since %v, test started %v", seen, before)
	}
}

func TestBindReportsAChromeThatWillNotStart(t *testing.T) {
	err := Bind(filepath.Join(t.TempDir(), "missing"), t.TempDir(), "Default", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-a", func(time.Time) bool { return true })
	if err == nil {
		t.Fatal("no error")
	}
}
