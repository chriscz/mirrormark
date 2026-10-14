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

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/firefox"
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
	browser := fl.String("browser", "chrome", "chrome, brave or firefox")
	udd := fl.String("user-data-dir", "", "the browser's data directory (default ~/.config/google-chrome for Chrome, ~/.config/BraveSoftware/Brave-Browser for Brave), or Firefox's profiles directory")
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
	if fb, ok := firefox.Lookup(*browser); ok {
		return installFirefox(p, fb, *udd, picks, stdout, stderr)
	}
	cb, ok := chrome.Lookup(*browser)
	if !ok {
		fmt.Fprintf(stderr, "mirrormark: --browser is chrome, brave or firefox, not %q\n", *browser)
		return 2
	}
	if *udd == "" {
		*udd, err = cb.DefaultUserDataDir()
	} else {
		*udd, err = resolveDir(*udd)
	}
	if err != nil {
		return fail(err)
	}
	// A data directory can outlive the browser's package; writing the
	// system files for a browser that is gone would create its directories.
	bin, err := exec.LookPath(cb.Bin)
	if err != nil {
		return fail(fmt.Errorf("%s is not installed (no %s on PATH)", cb.Name, cb.Bin))
	}
	profiles, err := cb.Profiles(*udd)
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
	system := cb.SystemFiles(ext, self)
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
			return fail(fmt.Errorf("could not copy the files %s needs into place (with sudo): %w", cb.Name, err))
		}
	}
	ids := map[string]string{}
	for _, c := range chosen {
		id, err := ensureDB(p, cb.Config, filepath.Join(*udd, c.Dir), c)
		if err != nil {
			return fail(err)
		}
		ids[c.Dir] = id
	}
	if err := ensureConfig(p); err != nil {
		return fail(err)
	}
	if !ready {
		fmt.Fprintf(stdout, "Restart %s (close every window), then run %s again to bind the profiles.\n", cb.Name, cb.InstallCommand())
		return 0
	}
	fmt.Fprintln(stdout, "Opening a tab in each profile to bind it.")
	code := 0
	for _, c := range chosen {
		id := ids[c.Dir]
		bound := func(since time.Time) bool { return db.Connected(p, id, since) }
		if err := cb.Bind(bin, *udd, c.Dir, ext.ID, id, bound); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v\n", c.Dir, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", c.Dir, id)
	}
	if code == 0 {
		fmt.Fprintf(stdout, "Now list the folders to share in %s.\nExport your bookmarks from %s first, so you have a copy.\nThe host logs to %s.\n", p.ConfigFile(), cb.Name, p.LogFile())
	}
	return code
}

// geckoSafety says what install turns off in each profile of b, before it
// does.
func geckoSafety(b firefox.Browser) string {
	return fmt.Sprintf(`%s installs only signed add-ons unless a profile says
otherwise, and mirrormark is not signed. So install sets, in each profile's
user.js, xpinstall.signatures.required to false (that profile then accepts
any unsigned add-on, not only mirrormark) and extensions.autoDisableScopes
to 14 (an add-on file in the profile's extensions folder starts enabled
without asking; add-ons elsewhere still ask).
mirrormark uninstall --browser %s sets them back.`, b.Name, b.Config)
}

// installFirefox installs into profiles of b, as the user: Gecko browsers
// read add-ons and host manifests from the user's own directories, so no
// sudo. It refuses profiles of any other browser before writing anything,
// since those install only signed add-ons.
func installFirefox(p db.Paths, b firefox.Browser, root string, picks []string, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	var err error
	if root == "" {
		root, err = b.DefaultRoot()
	} else {
		root, err = resolveDir(root)
	}
	if err != nil {
		return fail(err)
	}
	profiles, err := b.Profiles(root)
	if err != nil {
		return fail(err)
	}
	chosen, err := choose(profiles, picks)
	if err != nil {
		return fail(err)
	}
	type target struct {
		c        chrome.Profile
		dir, bin string
	}
	var targets []target
	var refused, skipped []string
	for _, c := range chosen {
		dir := firefox.Dir(root, c)
		bin, err := b.Check(dir)
		switch {
		case errors.Is(err, firefox.ErrNeverRun) && len(picks) == 0:
			// The user did not ask for it by name, and it holds nothing yet.
			skipped = append(skipped, fmt.Sprintf("%s: %v", c.Name, err))
		case err != nil:
			refused = append(refused, fmt.Sprintf("%s: %v", c.Name, err))
		default:
			targets = append(targets, target{c, dir, bin})
		}
	}
	if len(refused) > 0 {
		return fail(fmt.Errorf("cannot install into these profiles; leave them out with --profile:\n  %s", strings.Join(refused, "\n  ")))
	}
	if len(targets) == 0 {
		return fail(fmt.Errorf("no profile to install into:\n  %s", strings.Join(skipped, "\n  ")))
	}
	for _, s := range skipped {
		fmt.Fprintf(stdout, "skipped: %s, then run install again to add it\n", s)
	}
	// The databases come before any write into a profile: uninstall finds
	// the profiles whose checks to turn back on through them, so a write
	// that fails part way must not leave a profile without one.
	ids := map[string]string{}
	for _, tg := range targets {
		if ids[tg.dir], err = ensureDB(p, b.Config, tg.dir, tg.c); err != nil {
			return fail(err)
		}
	}
	if err := ensureConfig(p); err != nil {
		return fail(err)
	}
	self, err := selfPath()
	if err != nil {
		return fail(err)
	}
	var dirs []string
	for _, tg := range targets {
		dirs = append(dirs, tg.dir)
	}
	files, err := firefox.Files(dirs, self)
	if err != nil {
		return fail(err)
	}
	if !chrome.FilesCurrent(files) {
		fmt.Fprintln(stdout, geckoSafety(b))
		if err := chrome.WriteFiles(files); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Restart %s in these profiles if it is running (close every window), then run mirrormark install --browser %s again to bind them.\n", b.Name, b.Config)
		return 0
	}
	fmt.Fprintln(stdout, "Opening a tab in each profile to bind it.")
	code := 0
	for _, tg := range targets {
		id := ids[tg.dir]
		bound := func(since time.Time) bool { return db.Connected(p, id, since) }
		if err := b.Bind(tg.bin, tg.dir, id, bound); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v\n", tg.c.Name, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", tg.c.Name, id)
	}
	if code == 0 {
		fmt.Fprintf(stdout, "Now list the folders to share in %s.\nExport your bookmarks from %s first (Bookmarks, Manage bookmarks, Import and Backup), so you have a copy.\nThe host logs to %s.\n", p.ConfigFile(), b.Name, p.LogFile())
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

// writeSystemFiles writes the browsers' root-owned files. If this process
// may not, it pipes each file's data to `sudo install`, so no mirrormark
// code runs as root and no staged file can be swapped before root copies it.
func writeSystemFiles(files []chrome.File, stdout, stderr io.Writer) error {
	err := chrome.WriteFiles(files)
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	fmt.Fprintln(stdout, "The browser reads these files only from root's directories; writing them with sudo:")
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

// ensureDB returns the database id of the browser profile at path,
// creating its file first if none points at the profile yet. It stops if
// any database file cannot be read: that file may be this profile's, and a
// second one would make the profile a new member without the user knowing.
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
	purge := fl.Bool("purge", false, "also delete this browser's databases and backups (and the key with the last Chromium browser's), and the config and log with the last browser's")
	browser := fl.String("browser", "chrome", "chrome, brave or firefox")
	if fl.Parse(args) != nil {
		return 2
	}
	_, isChromium := chrome.Lookup(*browser)
	fb, isGecko := firefox.Lookup(*browser)
	if !isChromium && !isGecko {
		fmt.Fprintf(stderr, "mirrormark: --browser is chrome, brave or firefox, not %q\n", *browser)
		return 2
	}
	if refuseRoot("uninstall", "; it uses sudo only to remove the files it put in root's directories", stderr) {
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
	if isGecko {
		if err := uninstallFirefox(p, fb, stdout); err != nil {
			return fail(err)
		}
		return finishUninstall(p, fb.Config, fb.Name, *purge, false, stdout, stderr)
	}
	cb, _ := chrome.Lookup(*browser) // checked above
	keyFile := filepath.Join(p.Data, "extension.pem")
	key, err := chrome.LoadKey(keyFile)
	host := filepath.Join(sysroot, chrome.HostManifestPath())
	var users []string // other Chromium browsers that still have the extension
	installed := hasDatabase(p, cb.Config)
	switch {
	case err == nil:
		id := chrome.ExtensionID(chrome.PublicKeyDER(key))
		users = chromiumUsers(cb, id, stderr)
		paths := []string{filepath.Join(sysroot, cb.ExternalFile(id))}
		if _, err := os.Stat(paths[0]); !errors.Is(err, fs.ErrNotExist) {
			installed = true
		}
		// The host manifest serves every Chromium browser, so it goes with
		// the last one's external file. Its name is fixed; one that names
		// another extension belongs to another install.
		if data, err := os.ReadFile(host); err == nil && namesExtension(data, id) && len(users) == 0 {
			paths = append(paths, host)
		}
		if err := removeSystemFiles(paths, stdout, stderr); err != nil {
			return fail(err)
		}
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stdout, "No key in %s, so the extension id is unknown. Remove %s and %s by hand if they are there.\n",
			keyFile, filepath.Join(sysroot, cb.ExternalFile("<id>")), host)
	default:
		return fail(err)
	}
	if len(users) == 0 {
		if err := chrome.RemoveFiles([]string{filepath.Join(p.Data, "mirrormark.crx")}); err != nil {
			return fail(err)
		}
		if err := os.RemoveAll(filepath.Join(p.Data, "extension")); err != nil {
			return fail(err)
		}
	}
	if installed {
		fmt.Fprintf(stdout, "%s removes mirrormark from every profile when it next starts. Your bookmarks stay in %s.\n", cb.Name, cb.Name)
	} else {
		fmt.Fprintf(stdout, "mirrormark is not installed in %s: no %s file and no %s database.\n", cb.Name, cb.Name, cb.Name)
	}
	if len(users) > 0 {
		fmt.Fprintf(stdout, "Kept the CRX and the host manifest, which %s still uses.\n", strings.Join(users, " and "))
	}
	return finishUninstall(p, cb.Config, cb.Name, *purge, len(users) > 0, stdout, stderr)
}

// finishUninstall deals with mirrormark's own files once browser's install
// is undone. Without purge it says where they are. With purge it deletes
// browser's databases, with their backups and lock files, and a Chromium
// browser's key unless keyInUse (another Chromium browser still has the
// extension) or another Chromium browser's database is left. The config,
// the log and the rest of the data directory go only with the last
// database: another browser's profiles still sync through them, and its
// uninstall needs its databases to find its profiles.
func finishUninstall(p db.Paths, browser, name string, purge, keyInUse bool, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	_, chromium := chrome.Lookup(browser)
	again, kept := "mirrormark uninstall --purge", "the databases, backups and key"
	if browser != chrome.Chrome.Config {
		again = "mirrormark uninstall --browser " + browser + " --purge"
	}
	if !chromium {
		kept = "the databases and backups" // only the Chromium browsers have a key
	}
	if !purge {
		fmt.Fprintf(stdout, "Kept your config %s, %s in %s, and the log in %s.\n%s deletes %s's databases and backups, and the rest once no other browser's are left.\n", p.ConfigFile(), kept, p.Data, p.State, again, name)
		return 0
	}
	dbs, errs := db.LoadAll(p)
	for _, err := range errs {
		return fail(fmt.Errorf("%w; fix it or move it out of %s so --purge can tell which browser it belongs to", err, p.ProfilesDir()))
	}
	files, dirs, others := []string{}, []string{}, []string{}
	for id, d := range dbs {
		if d.Profile.Browser != browser {
			others = append(others, id)
			if _, ok := chrome.Lookup(d.Profile.Browser); ok {
				keyInUse = true
			}
			continue
		}
		files = append(files, p.ProfileFile(id), p.ProfileLock(id))
		dirs = append(dirs, p.BackupDir(id))
	}
	// The key fixes the id every Chromium browser here installed the
	// extension under.
	if chromium && !keyInUse {
		files = append(files, filepath.Join(p.Data, "extension.pem"))
	}
	if len(others) == 0 {
		files = append(files, p.ConfigFile(), p.GlobalLock())
		dirs = append(dirs, p.ProfilesDir(), filepath.Join(p.Data, "backups"), p.State)
	}
	if err := chrome.RemoveFiles(files); err != nil {
		return fail(err)
	}
	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil {
			return fail(err)
		}
	}
	if len(others) > 0 {
		slices.Sort(others)
		fmt.Fprintf(stdout, "Deleted %s's databases and backups. Kept your config %s, the log in %s, and the rest of %s, which the other databases (%s) still use; uninstalling their browser with --purge deletes them.\n",
			name, p.ConfigFile(), p.State, p.Data, strings.Join(others, ", "))
		return 0
	}
	// Anything still in the data directory is not this browser's to delete.
	if err := os.Remove(p.Data); err != nil && !errors.Is(err, fs.ErrNotExist) {
		entries, rerr := os.ReadDir(p.Data)
		if rerr != nil {
			return fail(err)
		}
		var left []string
		for _, e := range entries {
			left = append(left, e.Name())
		}
		fmt.Fprintf(stdout, "Deleted %s's databases and backups, your config %s and the log in %s. Kept %s: it still holds %s, which are not %s's.\n",
			name, p.ConfigFile(), p.State, p.Data, strings.Join(left, ", "), name)
		return 0
	}
	fmt.Fprintf(stdout, "Deleted %s, %s and %s.\n", p.ConfigFile(), p.Data, p.State)
	return 0
}

// uninstallFirefox removes the add-on from every profile a database file
// names as b's, puts back the two prefs that weakened its add-on
// checks, and removes the host manifest if it is this one's. It writes
// user.js first, so if that fails the add-on is still there and running
// uninstall again finishes the job.
func uninstallFirefox(p db.Paths, b firefox.Browser, stdout io.Writer) error {
	dbs, errs := db.LoadAll(p)
	for _, err := range errs {
		return fmt.Errorf("%w; fix it so uninstall can tell whether it is a %s profile", err, b.Name)
	}
	var files []chrome.File
	var gone []string // XPIs, and the host manifest if it is this one's
	for _, d := range dbs {
		if d.Profile.Browser != b.Config {
			continue
		}
		gone = append(gone, firefox.XPIPath(d.Profile.Path))
		path := filepath.Join(d.Profile.Path, "user.js")
		user, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var sb strings.Builder
		for line := range strings.Lines(string(user)) {
			if r, ok := firefox.ResetPrefs[strings.TrimSpace(line)]; ok {
				line = r + "\n"
			}
			sb.WriteString(line)
		}
		if sb.String() != string(user) {
			files = append(files, chrome.File{Path: path, Data: []byte(sb.String())})
		}
	}
	found := len(gone) > 0
	if host, err := firefox.HostManifestPath(); err != nil {
		return err
	} else if data, err := os.ReadFile(host); err == nil && bytes.Contains(data, []byte(`"`+extension.GeckoID+`"`)) {
		gone = append(gone, host)
	}
	if err := chrome.WriteFiles(files); err != nil {
		return err
	}
	if err := chrome.RemoveFiles(gone); err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(stdout, "No %s profile was found in mirrormark's databases in %s, so no user.js was changed. If a profile still has mirrormark, check its user.js for xpinstall.signatures.required and extensions.autoDisableScopes, and set them back to true and 15.\n", b.Name, p.ProfilesDir())
		return nil
	}
	fmt.Fprintf(stdout, "%s removes mirrormark from each profile when it next starts, and checks add-on signatures there again. Your bookmarks stay in %s.\n", b.Name, b.Name)
	return nil
}

// chromiumUsers names the Chromium browsers other than b whose external
// extension file for id is in place: they still use the CRX and the host
// manifest b shares with them. A browser whose file it cannot check counts
// as one, so a shared file goes only when it is known to be unused.
func chromiumUsers(b chrome.Browser, id string, stderr io.Writer) []string {
	var out []string
	for _, o := range chrome.Browsers {
		if o.Config == b.Config {
			continue
		}
		_, err := os.Stat(filepath.Join(sysroot, o.ExternalFile(id)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "mirrormark: warning: cannot tell whether %s still uses mirrormark, so keeping the files it shares: %v\n", o.Name, err)
		}
		out = append(out, o.Name)
	}
	return out
}

// hasDatabase reports whether a database names a profile of browser. A
// database it cannot read might, so it counts.
func hasDatabase(p db.Paths, browser string) bool {
	dbs, errs := db.LoadAll(p)
	if len(errs) > 0 {
		return true
	}
	for _, d := range dbs {
		if d.Profile.Browser == browser {
			return true
		}
	}
	return false
}

// removeSystemFiles removes the browsers' root-owned files, with `sudo rm` if
// this process may not.
func removeSystemFiles(paths []string, stdout, stderr io.Writer) error {
	err := chrome.RemoveFiles(paths)
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	args := append([]string{"rm", "-f", "--"}, paths...)
	fmt.Fprintln(stdout, "Removing these files needs root:\n  sudo", strings.Join(args, " "))
	return runSudo(os.Stdin, stdout, stderr, args...)
}
