package chrome

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// maxWrapper is the size of the largest script UnderFirejail reads: a
// wrapper that starts the browser in firejail is a line or two, and the
// browsers' own launcher scripts never name firejail.
const maxWrapper = 4096

// shellSep splits a shell line into commands.
var shellSep = regexp.MustCompile(`&&|\|\||[;|&(){}]`)

// UnderFirejail reports whether the command at path starts the browser
// in firejail: a symlink to firejail, which firecfg puts in
// /usr/local/bin, or a short script that runs firejail as a command.
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
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "#") {
			continue
		}
		for _, cmd := range shellSep.Split(line, -1) {
			if runsFirejail(strings.Fields(cmd)) {
				return true
			}
		}
	}
	return false
}

// runsFirejail reports whether one shell command's words run firejail:
// its first word, after exec, env, command, keywords and variable
// assignments.
func runsFirejail(words []string) bool {
	for _, w := range words {
		switch {
		case w == "exec" || w == "env" || w == "command" || w == "then" || w == "else" || w == "do" || w == "!":
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "="):
		default:
			return filepath.Base(strings.Trim(w, `"'`)) == "firejail"
		}
	}
	return false
}

// UnderFirejail reports whether any of b's commands on PATH starts it in
// firejail: the one install runs, or another name for it that a desktop
// file or the user may run.
func (b Browser) UnderFirejail() bool {
	for _, name := range append([]string{b.Bin}, b.Aliases...) {
		if bin, err := exec.LookPath(name); err == nil && UnderFirejail(bin) {
			return true
		}
	}
	return false
}

// FirejailLocal is the file, under ~/.config/firejail, where the user adds
// lines to every firejail profile of b. Firejail reads it only there, not
// under $XDG_CONFIG_HOME.
func (b Browser) FirejailLocal() string { return b.Firejail + ".local" }

// FirejailCRXRule is the line that shows the CRX at crx, and only it, to a
// jailed browser, which then cannot reach the databases and tokens beside
// it. It reports false for a CRX outside home: most other places are
// visible in the jail, and a whitelist line does not help in those that
// are blacklisted.
func FirejailCRXRule(home, crx string) (string, bool) {
	rel, err := filepath.Rel(home, crx)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return "whitelist ${HOME}/" + filepath.ToSlash(rel), true
}

// FirejailRules are the lines b's firejail profile needs before b can
// install the CRX at crx, in the form a .local file takes.
func (b Browser) FirejailRules(home, crx string) []string {
	var rules []string
	if r, ok := FirejailCRXRule(home, crx); ok {
		rules = append(rules, r)
	}
	if b.JailHidesExternalDir {
		rules = append(rules, "whitelist "+b.ExternalDir)
	}
	return rules
}

// firejailLine is a .local line in one spelling: single spaces, the home
// directory as ${HOME} however it is written, no trailing slash.
func firejailLine(home, line string) string {
	f := strings.Fields(line)
	if len(f) != 2 {
		return strings.Join(f, " ")
	}
	p := f[1]
	for _, h := range []string{"~", "$HOME", home} {
		if p == h || strings.HasPrefix(p, h+"/") {
			p = "${HOME}" + p[len(h):]
			break
		}
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return f[0] + " " + p
}

// FirejailMissing returns the rules for b that its .local file in home
// lacks. It reads that file as text and does not follow its includes.
func (b Browser) FirejailMissing(home, crx string) []string {
	have := map[string]bool{}
	if f, err := os.ReadFile(filepath.Join(home, ".config", "firejail", b.FirejailLocal())); err == nil {
		for line := range strings.Lines(string(f)) {
			have[firejailLine(home, line)] = true
		}
	}
	var missing []string
	for _, r := range b.FirejailRules(home, crx) {
		if !have[firejailLine(home, r)] {
			missing = append(missing, r)
		}
	}
	return missing
}
