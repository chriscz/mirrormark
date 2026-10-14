package firefox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
