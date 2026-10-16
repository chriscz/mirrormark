package chrome

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxWrapper is the size of the largest script UnderFirejail reads: a
// wrapper that starts the browser in firejail is a line or two, and the
// browsers' own launcher scripts never name firejail.
const maxWrapper = 4096

// UnderFirejail reports whether the command at path starts the browser
// in firejail: a symlink to firejail, which firecfg puts in
// /usr/local/bin, or a short script that names firejail.
func UnderFirejail(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if filepath.Base(real) == "firejail" {
		return true
	}
	f, err := os.Open(real)
	if err != nil {
		return false
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, maxWrapper+1))
	if err != nil || len(head) > maxWrapper || !bytes.HasPrefix(head, []byte("#!")) {
		return false
	}
	for line := range strings.Lines(string(head)) {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, "#") && strings.Contains(line, "firejail") {
			return true
		}
	}
	return false
}

// FirejailLocal is the file, under ~/.config/firejail, where the user adds
// lines to every firejail profile of b. Firejail reads it only there, not
// under $XDG_CONFIG_HOME.
func (b Browser) FirejailLocal() string { return b.Firejail + ".local" }

// FirejailRules are the lines b's firejail profile needs before b can
// install the CRX in data, which firejail's profiles hide in the home
// directory, in the form a .local file takes. Data elsewhere gets no line:
// most other places are visible in the jail, and a whitelist line does not
// help in those that are blacklisted.
func (b Browser) FirejailRules(home, data string) []string {
	var rules []string
	if rel, err := filepath.Rel(home, data); err == nil && rel != "." && filepath.IsLocal(rel) {
		rules = append(rules, "whitelist ${HOME}/"+filepath.ToSlash(rel))
	}
	if b.JailHidesExternalDir {
		rules = append(rules, "whitelist "+b.ExternalDir)
	}
	return rules
}

// FirejailMissing returns the rules for b that its .local file in home
// lacks, written with ${HOME} or with home spelled out.
func (b Browser) FirejailMissing(home, data string) []string {
	have := map[string]bool{}
	if f, err := os.ReadFile(filepath.Join(home, ".config", "firejail", b.FirejailLocal())); err == nil {
		for line := range strings.Lines(string(f)) {
			have[strings.Join(strings.Fields(line), " ")] = true
		}
	}
	var missing []string
	for _, r := range b.FirejailRules(home, data) {
		if !have[r] && !have[strings.Replace(r, "${HOME}", home, 1)] {
			missing = append(missing, r)
		}
	}
	return missing
}
