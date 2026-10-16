package chrome

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUnderFirejail(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	link := func(name, target string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	firejail := write("firejail", "\x7fELF fake firejail")
	plain := write("brave", "\x7fELF a browser that mentions firejail")
	for _, c := range []struct {
		name string
		path string
		want bool
	}{
		{"firecfg's symlink", link("by-firecfg", firejail), true},
		{"a chain of symlinks", link("chain", filepath.Join(dir, "by-firecfg")), true},
		{"a wrapper script", write("wrapper", "#!/bin/sh\nexec firejail --profile=brave /usr/bin/brave-browser \"$@\"\n"), true},
		{"a wrapper with an absolute path", write("wrapper2", "#!/bin/bash\n# start Brave jailed\nexec /usr/bin/firejail brave-browser \"$@\"\n"), true},
		{"a plain binary", plain, false},
		{"a symlink to a plain binary", link("to-plain", plain), false},
		{"a wrapper after a test", write("after", "#!/bin/sh\n[ -x /usr/bin/firejail ] && exec firejail brave-browser \"$@\"\n"), true},
		{"a wrapper in an if", write("if", "#!/bin/sh\nif true; then exec env A=1 firejail brave \"$@\"; fi\n"), true},
		{"a test for firejail", write("test", "#!/bin/sh\n[ -x /usr/bin/firejail ] || exec brave \"$@\"\n"), false},
		{"an echo naming firejail", write("echo", "#!/bin/sh\necho \"firejail not used\"\nexec brave\n"), false},
		{"a variable named for firejail", write("var", "#!/bin/sh\nFIREJAIL_OFF=1\nexec brave\n"), false},
		{"a firejail path in an argument", write("arg", "#!/bin/sh\nexec /opt/brave --helper=/usr/bin/firejail\n"), false},
		{"a script naming firejail only in a comment", write("comment", "#!/bin/sh\n# not for firejail\nexec /opt/brave.com/brave/brave \"$@\"\n"), false},
		{"a long script", write("long", "#!/bin/sh\n"+strings.Repeat("# padding\n", 500)+"exec firejail brave\n"), false},
		{"a missing file", filepath.Join(dir, "missing"), false},
	} {
		if got := UnderFirejail(c.path); got != c.want {
			t.Errorf("%s: UnderFirejail = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFirejailRules(t *testing.T) {
	home := "/home/u"
	crx := "whitelist ${HOME}/.local/share/mirrormark/mirrormark.crx"
	ext := "whitelist /usr/share/google-chrome/extensions"
	for _, c := range []struct {
		b    Browser
		data string
		want []string
	}{
		{Brave, "/home/u/.local/share/mirrormark", []string{crx}},
		{Brave, "/home/u/data/mirrormark", []string{"whitelist ${HOME}/data/mirrormark/mirrormark.crx"}},
		{Brave, "/mnt/data/mirrormark", nil},
		{Brave, "/home/user2/mirrormark", nil},
		{Chrome, "/home/u/.local/share/mirrormark", []string{crx, ext}},
		{Chrome, "/mnt/data/mirrormark", []string{ext}},
	} {
		if got := c.b.FirejailRules(home, CRXPath(c.data)); !slices.Equal(got, c.want) {
			t.Errorf("%s, %s: %q, want %q", c.b.Name, c.data, got, c.want)
		}
	}
}

func TestFirejailMissing(t *testing.T) {
	home := t.TempDir()
	crx := CRXPath(filepath.Join(home, ".local", "share", "mirrormark"))
	crxLine := "whitelist ${HOME}/.local/share/mirrormark/mirrormark.crx"
	extLine := "whitelist /usr/share/google-chrome/extensions"
	dir := filepath.Join(home, ".config", "firejail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Chrome.FirejailMissing(home, crx); !slices.Equal(got, []string{crxLine, extLine}) {
		t.Fatalf("no .local file: %q", got)
	}
	for _, c := range []struct {
		b     Browser
		local string
		want  []string
	}{
		{Brave, "# mine\n" + crxLine + "\n", nil},
		{Brave, "  whitelist   ${HOME}/.local/share/mirrormark/mirrormark.crx", nil},
		{Brave, "whitelist " + crx + "\n", nil},
		{Brave, "whitelist ~/.local/share/mirrormark/mirrormark.crx\n", nil},
		{Brave, "whitelist $HOME/.local/share/mirrormark/mirrormark.crx/\n", nil},
		{Brave, "whitelist ${HOME}/.local/share/mirrormark\n", []string{crxLine}},
		{Brave, "whitelist ${HOME}/.local/share/mirrormark/mirrormark.crx.old\n", []string{crxLine}},
		{Brave, "# " + crxLine + "\n", []string{crxLine}},
		{Brave, "include other.local\n", []string{crxLine}},
		{Chrome, crxLine + "\n", []string{extLine}},
		{Chrome, extLine + "/\nwhitelist " + crx + "\n", nil},
	} {
		if err := os.WriteFile(filepath.Join(dir, c.b.FirejailLocal()), []byte(c.local), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := c.b.FirejailMissing(home, crx); !slices.Equal(got, c.want) {
			t.Errorf("%s with %q: %q, want %q", c.b.FirejailLocal(), c.local, got, c.want)
		}
	}
}
