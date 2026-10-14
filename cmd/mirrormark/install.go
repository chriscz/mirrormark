package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
)

// Tests move the system files under a temporary root and replace sudo, so
// they never touch the real system files or ask for a password.
var (
	sysroot = ""
	runSudo = sudo
	geteuid = os.Geteuid
)

// refuseRoot reports, and says, whether this runs as root. As root, the
// key, the extension and the databases would be root's, in root's home,
// where the user's Chrome cannot read them.
func refuseRoot(cmd, why string, stderr io.Writer) bool {
	if geteuid() != 0 {
		return false
	}
	fmt.Fprintf(stderr, "mirrormark: run %s as yourself, not as root%s\n", cmd, why)
	return true
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

const configExample = `# mirrormark configuration. Each union lists, for each profile id, the
# folder that profile shares. Profile ids are the file names (without .yaml)
# in ~/.local/share/mirrormark/profiles/. Paths start with toolbar, other or
# mobile. Example:
#
# unions:
#   work:
#     chrome-personal: toolbar/Work
#     chrome-work: toolbar/Work
format: 1
unions: {}
`

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("install", flag.ContinueOnError)
	fl.SetOutput(stderr)
	udd := fl.String("user-data-dir", "", "Chrome's data directory (default ~/.config/google-chrome)")
	var picks multiFlag
	fl.Var(&picks, "profile", `profile to bind, by directory such as "Profile 1" or by name; repeatable (default: every profile)`)
	if fl.Parse(args) != nil {
		return 2
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "mirrormark: install takes no arguments, got %q\n", fl.Args())
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	if refuseRoot("install", "; it uses sudo only to copy two files into place", stderr) {
		return 1
	}
	p, err := db.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	if *udd == "" {
		*udd, err = chrome.DefaultUserDataDir()
	} else {
		*udd, err = resolveDir(*udd)
	}
	if err != nil {
		return fail(err)
	}
	profiles, err := chrome.Profiles(*udd)
	if err != nil {
		return fail(err)
	}
	chosen, err := choose(profiles, picks)
	if err != nil {
		return fail(err)
	}
	ext, err := chrome.Prepare(p.Data)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "Extension ID: %s\n", ext.ID)
	self, err := selfPath()
	if err != nil {
		return fail(err)
	}
	system := chrome.SystemFiles(ext, self)
	for i := range system {
		system[i].Path = filepath.Join(sysroot, system[i].Path)
	}
	// Current files mean an earlier run wrote them and asked for the
	// restart, so the extension is installed and profiles can be bound now.
	ready := chrome.FilesCurrent(system)
	if !ready {
		// The host manifest's name is fixed, so another install's (another
		// user's key, say) is in the same place; replacing it cuts that
		// install off from its host.
		if data, err := os.ReadFile(system[1].Path); err == nil && !namesExtension(data, ext.ID) {
			fmt.Fprintf(stderr, "mirrormark: warning: %s lets another extension, not %s, start mirrormark; replacing it. If another install of mirrormark (with another key) uses it, that one stops working.\n", system[1].Path, ext.ID)
		}
		if err := writeSystemFiles(system, stdout, stderr); err != nil {
			return fail(fmt.Errorf("could not copy the files Chrome needs into place (with sudo): %w", err))
		}
	}
	ids := map[string]string{}
	for _, c := range chosen {
		id, err := ensureDB(p, "chrome", filepath.Join(*udd, c.Dir), c)
		if err != nil {
			return fail(err)
		}
		ids[c.Dir] = id
	}
	if err := ensureConfig(p); err != nil {
		return fail(err)
	}
	if !ready {
		fmt.Fprintln(stdout, "Restart Chrome (close every window), then run mirrormark install again to bind the profiles.")
		return 0
	}
	bin, err := exec.LookPath("google-chrome")
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, "Opening a tab in each profile to bind it.")
	code := 0
	for _, c := range chosen {
		id := ids[c.Dir]
		bound := func(since time.Time) bool { return db.Connected(p, id, since) }
		if err := chrome.Bind(bin, *udd, c.Dir, ext.ID, id, bound); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v\n", c.Dir, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", c.Dir, id)
	}
	if code == 0 {
		fmt.Fprintf(stdout, "Now list the folders to share in %s.\nExport your bookmarks from Chrome first, so you have a copy.\nThe host logs to %s.\n", p.ConfigFile(), p.LogFile())
	}
	return code
}

// selfPath returns the path of this executable with symlinks resolved.
func selfPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(self)
}

// ensureConfig writes the example config unless one exists.
func ensureConfig(p db.Paths) error {
	if _, err := os.Stat(p.ConfigFile()); errors.Is(err, fs.ErrNotExist) {
		return db.WriteAtomic(p.ConfigFile(), []byte(configExample), 0o600)
	}
	return nil
}

// namesExtension reports whether a host manifest lets extension id start
// the host.
func namesExtension(hostManifest []byte, id string) bool {
	return bytes.Contains(hostManifest, []byte(`"chrome-extension://`+id+`/"`))
}

// resolveDir returns dir's absolute path with symlinks resolved, so one
// directory always maps to the same database files however it is named. A
// directory that does not exist yet keeps its absolute path.
func resolveDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return abs, nil
	}
	return real, err
}

// writeSystemFiles writes Chrome's two root-owned files. If this process
// may not, it pipes each file's data to `sudo install`, so no mirrormark
// code runs as root and no staged file can be swapped before root copies it.
func writeSystemFiles(files []chrome.File, stdout, stderr io.Writer) error {
	err := chrome.WriteFiles(files)
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	fmt.Fprintln(stdout, "Chrome reads these two files only from root's directories; writing them with sudo:")
	for _, f := range files {
		args := []string{"install", "-D", "-m", "0644", "/dev/stdin", f.Path}
		fmt.Fprintln(stdout, "  sudo", strings.Join(args, " "))
		if err := runSudo(bytes.NewReader(f.Data), stdout, stderr, args...); err != nil {
			return err
		}
	}
	return nil
}

// sudo runs a command as root with stdin as its input. sudo asks for the
// password on the terminal, not on stdin.
func sudo(stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.Command("sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// choose returns the profiles named in picks, by directory or by a name
// only one profile has, or every profile if picks is empty.
func choose(all []chrome.Profile, picks []string) ([]chrome.Profile, error) {
	if len(picks) == 0 {
		return all, nil
	}
	var got []chrome.Profile
	for _, want := range picks {
		i := slices.IndexFunc(all, func(p chrome.Profile) bool { return p.Dir == want })
		if i < 0 {
			var named []int
			for j, p := range all {
				if p.Name == want {
					named = append(named, j)
				}
			}
			if len(named) > 1 {
				return nil, fmt.Errorf("more than one profile is named %q; name its directory instead: %s", want, listProfiles(all))
			}
			if len(named) == 0 {
				return nil, fmt.Errorf("no profile %q; the profiles are %s", want, listProfiles(all))
			}
			i = named[0]
		}
		if !slices.Contains(got, all[i]) {
			got = append(got, all[i])
		}
	}
	return got, nil
}

func listProfiles(all []chrome.Profile) string {
	var s []string
	for _, p := range all {
		s = append(s, fmt.Sprintf("%s (%s)", p.Dir, p.Name))
	}
	return strings.Join(s, ", ")
}

// ensureDB returns the database id of the browser profile at path, creating its file first
// if none points at the profile yet. It stops if any database file cannot
// be read: that file may be this profile's, and a second one would make
// the profile a new member without the user knowing.
func ensureDB(p db.Paths, browser, path string, c chrome.Profile) (string, error) {
	dbs, errs := db.LoadAll(p)
	for _, err := range errs {
		return "", fmt.Errorf("%w; fix the file or move it out of %s, then run install again", err, p.ProfilesDir())
	}
	for id, d := range dbs {
		if d.Profile.Path == path {
			return id, nil
		}
	}
	base := slug(c.Name)
	if base == "" {
		base = slug(c.Dir)
	}
	id, label := browser+"-"+base, browser+"/"+base
	for n := 2; ; n++ {
		_, err := os.Stat(p.ProfileFile(id))
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		id, label = fmt.Sprintf("%s-%s-%d", browser, base, n), fmt.Sprintf("%s/%s-%d", browser, base, n)
	}
	d := &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: label, Browser: browser, Path: path}}
	return id, db.SaveDB(p.ProfileFile(id), d)
}

// slug lower-cases s and joins its runs of a-z and 0-9 with dashes.
func slug(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			gap = false
			b.WriteRune(r)
		} else {
			gap = true
		}
	}
	return b.String()
}

// cmdUnpack writes the extension with the install's key, so a Load
// unpacked copy has the same id as the installed one. Without a folder it
// uses mirrormark's own, and packs the CRX there too.
func cmdUnpack(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "mirrormark: usage: mirrormark unpack [DIR]")
		return 2
	}
	if refuseRoot("unpack", "", stderr) {
		return 1
	}
	p, err := db.DefaultPaths()
	if err == nil {
		var ext chrome.Extension
		if len(args) == 1 {
			var dir string
			if dir, err = filepath.Abs(args[0]); err == nil {
				ext, err = chrome.Unpack(p.Data, dir)
			}
		} else {
			ext, err = chrome.Prepare(p.Data)
		}
		if err == nil {
			fmt.Fprintf(stdout, "Extension ID: %s\nUnpacked to %s\n", ext.ID, ext.Dir)
			return 0
		}
	}
	fmt.Fprintln(stderr, "mirrormark:", err)
	return 1
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fl.SetOutput(stderr)
	purge := fl.Bool("purge", false, "also delete the config, databases, backups, key and log")
	if fl.Parse(args) != nil {
		return 2
	}
	if refuseRoot("uninstall", "; it uses sudo only to remove two files", stderr) {
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	p, err := db.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	keyFile := filepath.Join(p.Data, "extension.pem")
	key, err := chrome.LoadKey(keyFile)
	switch {
	case err == nil:
		id := chrome.ExtensionID(chrome.PublicKeyDER(key))
		var paths []string
		for _, f := range chrome.SystemFilePaths(id) {
			paths = append(paths, filepath.Join(sysroot, f))
		}
		// The host manifest's name is fixed; one that names another
		// extension belongs to another install.
		if data, err := os.ReadFile(paths[1]); err != nil || !namesExtension(data, id) {
			paths = paths[:1]
		}
		if err := removeSystemFiles(paths, stdout, stderr); err != nil {
			return fail(err)
		}
	case errors.Is(err, fs.ErrNotExist):
		ps := chrome.SystemFilePaths("<id>")
		fmt.Fprintf(stdout, "No key in %s, so the extension id is unknown. Remove %s and %s by hand if they are there.\n",
			keyFile, filepath.Join(sysroot, ps[0]), filepath.Join(sysroot, ps[1]))
	default:
		return fail(err)
	}
	if err := chrome.RemoveFiles([]string{filepath.Join(p.Data, "mirrormark.crx")}); err != nil {
		return fail(err)
	}
	if err := os.RemoveAll(filepath.Join(p.Data, "extension")); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, "Chrome removes mirrormark from every profile when it next starts. Your bookmarks stay in Chrome.")
	if *purge {
		for _, d := range []string{p.Data, p.State} {
			if err := os.RemoveAll(d); err != nil {
				return fail(err)
			}
		}
		if err := chrome.RemoveFiles([]string{p.ConfigFile()}); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Deleted %s, %s and %s.\n", p.ConfigFile(), p.Data, p.State)
		return 0
	}
	fmt.Fprintf(stdout, "Kept your config %s, the databases, backups and key in %s, and the log in %s.\nmirrormark uninstall --purge deletes them.\n", p.ConfigFile(), p.Data, p.State)
	return 0
}

// removeSystemFiles removes Chrome's root-owned files, with `sudo rm` if
// this process may not.
func removeSystemFiles(paths []string, stdout, stderr io.Writer) error {
	err := chrome.RemoveFiles(paths)
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	args := append([]string{"rm", "-f", "--"}, paths...)
	fmt.Fprintln(stdout, "Removing Chrome's files needs root:\n  sudo", strings.Join(args, " "))
	return runSudo(os.Stdin, stdout, stderr, args...)
}
