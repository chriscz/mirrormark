package db

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSplitPath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
		ok   bool
	}{
		{"toolbar/Work", []string{"toolbar", "Work"}, true},
		{`other/A\/B/C`, []string{"other", "A/B", "C"}, true},
		{`Bookmarks bar/x\\y`, []string{"Bookmarks bar", `x\y`}, true},
		{"toolbar//Work", nil, false},
		{"", nil, false},
		{`toolbar/x\`, nil, false},
	} {
		got, err := SplitPath(c.in)
		if (err == nil) != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("SplitPath(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestLoadConfigMissingIsEmpty(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil || c.Format != 1 || len(c.Unions) != 0 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestConfigValidate(t *testing.T) {
	for name, text := range map[string]string{
		"bad format":  "format: 2\n",
		"bad id":      "format: 1\nunions:\n  work:\n    Chrome_A: toolbar/Work\n",
		"bad path":    "format: 1\nunions:\n  work:\n    chrome-a: toolbar//Work\n",
		"nested":      "format: 1\nunions:\n  a:\n    chrome-a: toolbar/Work\n  b:\n    chrome-a: toolbar/Work/Sub\n",
		"same folder": "format: 1\nunions:\n  a:\n    chrome-a: toolbar/Work\n  b:\n    chrome-a: toolbar/Work\n",
		"unknown key": "format: 1\nunion: {}\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte(text), 0o600)
		if _, err := LoadConfig(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("format: 1\nunions:\n  work:\n    chrome-a: toolbar/Work\n    chrome-b: other/Work\n"), 0o600)
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestLoadConfigEmptyFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, nil, 0o600)
	c, err := LoadConfig(path)
	if err != nil || c.Format != 1 || len(c.Unions) != 0 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestConfigNestingRespectsEscapedSlash(t *testing.T) {
	load := func(text string) error {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte(text), 0o600)
		_, err := LoadConfig(path)
		return err
	}
	// "A/B" is one folder title, not folder B inside A.
	if err := load("format: 1\nunions:\n  a:\n    chrome-a: toolbar/A\n  b:\n    chrome-a: 'toolbar/A\\/B'\n"); err != nil {
		t.Fatalf("sibling folders rejected: %v", err)
	}
	if err := load("format: 1\nunions:\n  a:\n    chrome-a: 'toolbar/A\\/B'\n  b:\n    chrome-a: 'toolbar/A\\/B/C'\n"); err == nil {
		t.Fatal("nested folders accepted")
	}
}
