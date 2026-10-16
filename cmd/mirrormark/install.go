package main

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
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
	browser := fl.String("browser", "chrome", "chrome, brave, firefox or librewolf")
	udd := fl.String("user-data-dir", "", "the browser's data directory (default ~/.config/google-chrome for Chrome, ~/.config/BraveSoftware/Brave-Browser for Brave), or the profiles directory of Firefox Developer Edition or LibreWolf")
	var picks multiFlag
	fl.Var(&picks, "profile", `profile to install into or bind, by ID (its directory, such as "Profile 1") or by number in the list install prints without --profile; repeatable`)
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
	if refuseRoot("install", "; it uses sudo only to copy one file into place", stderr) {
		return 1
	}
	p, err := db.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	uddFlag := *udd // as the user gave it, for the commands messages print
	if fb, ok := firefox.Lookup(*browser); ok {
		return installFirefox(p, fb, *udd, uddFlag, picks, stdout, stderr)
	}
	cb, ok := chrome.Lookup(*browser)
	if !ok {
		fmt.Fprintf(stderr, "mirrormark: --browser is chrome, brave, firefox or librewolf, not %q\n", *browser)
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
	list := cb.InstallCommand()
	if uddFlag != "" {
		list += " --user-data-dir " + quoteArg(uddFlag)
	}
	if len(picks) == 0 {
		return listChromium(p, cb, *udd, list, profiles, stdout, stderr)
	}
	chosen, err := choose(profiles, picks, list)
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
	system := cb.SystemFiles(ext)
	for i := range system {
		system[i].Path = filepath.Join(sysroot, system[i].Path)
	}
	// Current files mean an earlier run wrote them and asked for the
	// restart, so the extension is installed and profiles can be bound now.
	ready := chrome.FilesCurrent(system)
	if !ready {
		if err := writeSystemFiles(system, stdout, stderr); err != nil {
			return fail(fmt.Errorf("could not copy the files %s needs into place (with sudo): %w", cb.Name, err))
		}
	}
	dbIDs := map[string]string{}
	for _, c := range chosen {
		id, err := ensureDB(p, cb.Config, filepath.Join(*udd, c.Dir), c)
		if err != nil {
			return fail(err)
		}
		dbIDs[c.Dir] = id
	}
	if err := ensureConfig(p); err != nil {
		return fail(err)
	}
	if err := startServer(p, self, stdout); err != nil {
		return fail(err)
	}
	// The same profiles, named by ID however the user named them.
	all := again(cb.InstallCommand(), uddFlag, ids(chosen)...)
	if !ready {
		fmt.Fprintf(stdout, "Restart %s (close every window), then run %s again to bind the profiles.\n", cb.Name, all)
		return 0
	}
	if !serverRunning(p) {
		fmt.Fprintf(stdout, "mirrormark is not running, so no profile can report in. Start it with systemctl --user start mirrormark, or run mirrormark serve, then run %s again.\n", all)
		return 1
	}
	want, err := extension.Version()
	if err != nil {
		return fail(err)
	}
	code, bound := 0, []string{}
	for _, c := range chosen {
		cmd := again(cb.InstallCommand(), uddFlag, c.ID())
		// The external file is current here, so a profile without the
		// extension is waiting for the browser, not for mirrormark.
		st, err := chrome.ReadState(filepath.Join(*udd, c.Dir), ext.ID, want, true)
		if err != nil {
			fmt.Fprintf(stdout, "not ready: %s: cannot read its state: %v.\n", c.ID(), err)
			code = 1
			continue
		}
		if st.State != chrome.Ready {
			fmt.Fprintf(stdout, "not ready: %s: %s.\n", c.ID(), chromiumAdvice(cb, st, want, cmd))
			code = 1
			continue
		}
		id := dbIDs[c.Dir]
		reported := func(since time.Time) bool { return db.Connected(p, id, since) }
		token, err := db.Bind(p, id, "chrome-extension://"+ext.ID)
		if err != nil {
			return fail(err)
		}
		if err := cb.Bind(bin, *udd, c.Dir, ext.ID, id, token, reported); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v, then run %s.\n", c.ID(), err, cmd)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", c.ID(), id)
		bound = append(bound, id)
	}
	if code == 0 {
		nextSteps(p, cb.Name, "", bound, stdout)
	}
	return code
}

// listChromium prints b's profiles with their state and changes nothing.
func listChromium(p db.Paths, b chrome.Browser, udd, list string, profiles []chrome.Profile, stdout, stderr io.Writer) int {
	want, err := extension.Version()
	if err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	// Read the key, never create it: a listing writes nothing. Without a
	// key no extension id exists, so nothing can be installed anywhere.
	id, placed := "", false
	if key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem")); err == nil {
		id = chrome.ExtensionID(chrome.PublicKeyDER(key))
		_, err := os.Stat(filepath.Join(sysroot, b.ExternalFile(id)))
		placed = err == nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "mirrormark: warning: %v\n", err)
	}
	states := make([]string, len(profiles))
	for i, c := range profiles {
		dir := filepath.Join(udd, c.Dir)
		if id == "" {
			states[i] = chrome.ProfileState{State: chrome.NotInstalled}.String()
			if _, err := os.Stat(filepath.Join(dir, "Preferences")); errors.Is(err, fs.ErrNotExist) {
				states[i] = chrome.ProfileState{State: chrome.NeverRun}.String()
			}
			continue
		}
		s, err := chrome.ReadState(dir, id, want, placed)
		if err != nil {
			fmt.Fprintf(stderr, "mirrormark: warning: %s: %v\n", c.ID(), err)
			states[i] = "unreadable"
			continue
		}
		states[i] = s.String()
	}
	return printList(p, b.Name, udd, list, profiles, func(c chrome.Profile) string { return filepath.Join(udd, c.Dir) }, states, stdout, stderr)
}

// listGecko prints b's profiles with their state and changes nothing.
func listGecko(p db.Paths, b firefox.Browser, root, list string, profiles []chrome.Profile, stdout, stderr io.Writer) int {
	want, err := extension.Version()
	if err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	states := make([]string, len(profiles))
	for i, c := range profiles {
		dir := firefox.Dir(root, c)
		if _, err := b.Check(dir); errors.Is(err, firefox.ErrNeverRun) {
			states[i] = chrome.ProfileState{State: chrome.NeverRun}.String()
			continue
		} else if err != nil {
			// Another browser's profile, or one whose files Check cannot
			// read; the warning says which.
			fmt.Fprintf(stderr, "mirrormark: warning: %s: %v\n", c.ID(), err)
			states[i] = "not " + b.Name
			continue
		}
		s, _, err := firefox.ReadState(dir, want)
		if err != nil {
			fmt.Fprintf(stderr, "mirrormark: warning: %s: %v\n", c.ID(), err)
			states[i] = "unreadable"
			continue
		}
		states[i] = s.String()
	}
	return printList(p, b.Name, root, list, profiles, func(c chrome.Profile) string { return firefox.Dir(root, c) }, states, stdout, stderr)
}

// printList prints the listing; a ready profile a database names is bound.
func printList(p db.Paths, name, where, list string, profiles []chrome.Profile, path func(chrome.Profile) string, states []string, stdout, stderr io.Writer) int {
	dbs, errs := db.LoadAll(p)
	for _, err := range errs {
		fmt.Fprintf(stderr, "mirrormark: warning: %v\n", err)
	}
	byPath := map[string]string{}
	for id, d := range dbs {
		byPath[d.Profile.Path] = id
	}
	fmt.Fprintf(stdout, "%s profiles in %s:\n", name, where)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  #\tID\tNAME\tSTATE\tDATABASE")
	ready := chrome.ProfileState{State: chrome.Ready}.String()
	example, seen := 0, false
	for i, c := range profiles {
		dbid, state := byPath[path(c)], states[i]
		if dbid != "" && state == ready {
			state = "bound"
		}
		if state != "bound" && !seen {
			example, seen = i, true
		}
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\n", i, c.ID(), cmp.Or(c.Name, "-"), state, cmp.Or(dbid, "-"))
	}
	tw.Flush()
	fmt.Fprintf(stdout, "Nothing was changed. Name the profiles to install into or bind with --profile ID or --profile #, for example:\n  %s --profile %s\n", list, quoteArg(profiles[example].ID()))
	return 0
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
// read add-ons from the profile's own directory, so no sudo. It refuses
// profiles of any other browser before writing anything, since those
// install only signed add-ons.
func installFirefox(p db.Paths, b firefox.Browser, root, udd string, picks []string, stdout, stderr io.Writer) int {
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
	list := "mirrormark install --browser " + b.Config
	if udd != "" {
		list += " --user-data-dir " + quoteArg(udd)
	}
	if len(picks) == 0 {
		return listGecko(p, b, root, list, profiles, stdout, stderr)
	}
	chosen, err := choose(profiles, picks, list)
	if err != nil {
		return fail(err)
	}
	type target struct {
		c        chrome.Profile
		dir, bin string
	}
	var targets []target
	var refused []string
	for _, c := range chosen {
		dir := firefox.Dir(root, c)
		bin, err := b.Check(dir)
		if err != nil {
			refused = append(refused, fmt.Sprintf("%s: %v", c.ID(), err))
			continue
		}
		targets = append(targets, target{c, dir, bin})
	}
	if len(refused) > 0 {
		return fail(fmt.Errorf("cannot install into these profiles; leave them out:\n  %s", strings.Join(refused, "\n  ")))
	}
	// The databases come before any write into a profile: uninstall finds
	// the profiles whose checks to turn back on through them, so a write
	// that fails part way must not leave a profile without one.
	dbIDs := map[string]string{}
	for _, tg := range targets {
		if dbIDs[tg.dir], err = ensureDB(p, b.Config, tg.dir, tg.c); err != nil {
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
	files, err := firefox.Files(dirs)
	if err != nil {
		return fail(err)
	}
	current := chrome.FilesCurrent(files)
	if !current {
		fmt.Fprintln(stdout, geckoSafety(b))
		if err := chrome.WriteFiles(files); err != nil {
			return fail(err)
		}
	}
	if err := startServer(p, self, stdout); err != nil {
		return fail(err)
	}
	all := again("mirrormark install --browser "+b.Config, udd, ids(chosen)...)
	if !current {
		fmt.Fprintf(stdout, "Restart %s in these profiles if it is running (close every window), then run %s again to bind them.\n", b.Name, all)
		return 0
	}
	if !serverRunning(p) {
		fmt.Fprintf(stdout, "mirrormark is not running, so no profile can report in. Start it with systemctl --user start mirrormark, or run mirrormark serve, then run %s again.\n", all)
		return 1
	}
	want, err := extension.Version()
	if err != nil {
		return fail(err)
	}
	code, bound := 0, []string{}
	for _, tg := range targets {
		cmd := again("mirrormark install --browser "+b.Config, udd, tg.c.ID())
		st, uuid, err := firefox.ReadState(tg.dir, want)
		if err != nil {
			fmt.Fprintf(stdout, "not ready: %s: cannot read its state: %v.\n", tg.c.ID(), err)
			code = 1
			continue
		}
		if st.State != chrome.Ready {
			fmt.Fprintf(stdout, "not ready: %s: %s.\n", tg.c.ID(), geckoAdvice(b, st, want, cmd))
			code = 1
			continue
		}
		id := dbIDs[tg.dir]
		reported := func(since time.Time) bool { return db.Connected(p, id, since) }
		token, err := db.Bind(p, id, "moz-extension://"+uuid)
		if err != nil {
			return fail(err)
		}
		if err := b.Bind(tg.bin, tg.dir, uuid, id, token, reported); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v, then run %s.\n", tg.c.ID(), err, cmd)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", tg.c.ID(), id)
		bound = append(bound, id)
	}
	if code == 0 {
		nextSteps(p, b.Name, " (Bookmarks, Manage bookmarks, Import and Backup)", bound, stdout)
	}
	return code
}

// nextSteps says, once every profile named is bound, what to do next: a
// config that shares a folder of the databases just bound, ready to paste,
// exporting the bookmarks from browser first (how says where), and the log.
func nextSteps(p db.Paths, browser, how string, bound []string, stdout io.Writer) {
	fmt.Fprintf(stdout, "Now list the folders to share in %s, for example:\n\n%s\nExport your bookmarks from %s first%s, so you have a copy.\nmirrormark logs to %s.\n",
		p.ConfigFile(), unionExample("", bound...), browser, how, p.LogFile())
}

// again is the command that runs install for the profiles with these IDs
// again, with the data directory as the user gave it, as messages tell the
// user to type it.
func again(base, udd string, ids ...string) string {
	s := base
	if udd != "" {
		s += " --user-data-dir " + quoteArg(udd)
	}
	for _, id := range ids {
		s += " --profile " + quoteArg(id)
	}
	return s
}

// ids is the ID of each profile.
func ids(profiles []chrome.Profile) []string {
	var out []string
	for _, c := range profiles {
		out = append(out, c.ID())
	}
	return out
}

// quoteArg puts single quotes around an argument a shell would split or
// expand; inside them a shell expands nothing.
func quoteArg(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-/") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// chromiumAdvice says why a Chromium profile is not ready and what to do.
func chromiumAdvice(b chrome.Browser, s chrome.ProfileState, want, again string) string {
	switch s.State {
	case chrome.NeverRun:
		return fmt.Sprintf("%s has never opened this profile; open it in %s once, then run %s", b.Name, b.Name, again)
	case chrome.Removed:
		return fmt.Sprintf("you removed mirrormark from this profile, and %s will not install it there again. Run mirrormark unpack ~/mirrormark-extension, turn on Developer mode in %s, choose Load unpacked and pick that folder, then run %s", b.Name, b.ExtensionsPage, again)
	case chrome.TurnedOff:
		return fmt.Sprintf("mirrormark is turned off in this profile. Turn it on in %s, wait half a minute, then run %s", b.ExtensionsPage, again)
	case chrome.OldBuild:
		return fmt.Sprintf("this profile runs mirrormark %s, not %s. Quit %s completely (close every window) and start it again, wait half a minute, then run %s", s.Version, want, b.Name, again)
	}
	return fmt.Sprintf("%s has not installed mirrormark in this profile yet. Quit %s completely (close every window), start it again and open this profile, wait half a minute, then run %s", b.Name, b.Name, again)
}

// geckoAdvice says why a Gecko profile is not ready and what to do.
func geckoAdvice(b firefox.Browser, s chrome.ProfileState, want, again string) string {
	switch s.State {
	case chrome.TurnedOff:
		return "mirrormark is turned off in this profile. Turn it on in about:addons, then run " + again
	case chrome.Refused:
		return fmt.Sprintf("%s refuses mirrormark in this profile because it is not signed. Check that the profile's user.js sets xpinstall.signatures.required to false, restart %s there, then run %s", b.Name, b.Name, again)
	case chrome.OldBuild:
		return fmt.Sprintf("this profile runs mirrormark %s, not %s. Quit %s in this profile (close every window) and start it there again, then run %s", s.Version, want, b.Name, again)
	}
	return fmt.Sprintf("%s has not installed mirrormark in this profile yet. Quit %s if it runs in this profile (close every window) and start it there again, then run %s", b.Name, b.Name, again)
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

// choose returns the profiles picks names, by ID or by number in the
// list, in the order first named. list is the command that lists them.
// Names are not taken: they need not be unique, and the list shows them.
func choose(all []chrome.Profile, picks []string, list string) ([]chrome.Profile, error) {
	var got []chrome.Profile
	for _, want := range picks {
		var hits []int
		for i, p := range all {
			if p.ID() == want {
				hits = append(hits, i)
			}
		}
		if len(hits) > 1 {
			var nums []string
			for _, i := range hits {
				nums = append(nums, strconv.Itoa(i))
			}
			return nil, fmt.Errorf("%d profiles have the ID %q (numbers %s); name one by its number", len(hits), want, strings.Join(nums, ", "))
		}
		if len(hits) == 0 {
			// Only plain digits are a number: Atoi would also take "+1".
			if want == "" || strings.Trim(want, "0123456789") != "" {
				return nil, fmt.Errorf("no profile %q; run %s to list them", want, list)
			}
			// Too many digits for an int is still a number, just no profile's.
			n, err := strconv.Atoi(want)
			if err != nil || n >= len(all) {
				return nil, fmt.Errorf("no profile number %s; run %s to list them", want, list)
			}
			hits = []int{n}
		}
		if !slices.Contains(got, all[hits[0]]) {
			got = append(got, all[hits[0]])
		}
	}
	return got, nil
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
	browser := fl.String("browser", "chrome", "chrome, brave, firefox or librewolf")
	if fl.Parse(args) != nil {
		return 2
	}
	_, isChromium := chrome.Lookup(*browser)
	fb, isGecko := firefox.Lookup(*browser)
	if !isChromium && !isGecko {
		fmt.Fprintf(stderr, "mirrormark: --browser is chrome, brave, firefox or librewolf, not %q\n", *browser)
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
		others := otherUsers(p, fb.Config, stderr)
		if err := uninstallFirefox(p, fb, stdout); err != nil {
			return fail(err)
		}
		if err := stopServer(p, others.all(), stdout); err != nil {
			return fail(err)
		}
		return finishUninstall(p, fb.Config, fb.Name, *purge, false, stdout, stderr)
	}
	cb, _ := chrome.Lookup(*browser) // checked above
	keyFile := filepath.Join(p.Data, "extension.pem")
	key, err := chrome.LoadKey(keyFile)
	legacy := filepath.Join(sysroot, chrome.LegacyHostManifest)
	var users []string // other Chromium browsers that still have the extension
	var others browserUsers
	installed := hasDatabase(p, cb.Config)
	switch {
	case err == nil:
		id := chrome.ExtensionID(chrome.PublicKeyDER(key))
		others = otherUsers(p, cb.Config, stderr)
		users = others.chromium
		paths := []string{filepath.Join(sysroot, cb.ExternalFile(id))}
		if _, err := os.Stat(paths[0]); !errors.Is(err, fs.ErrNotExist) {
			installed = true
		}
		// An earlier build's host manifest starts nothing now, so it goes
		// whatever the other browsers do. Its name is fixed; one that names
		// another extension belongs to another install.
		if data, err := os.ReadFile(legacy); err == nil && namesExtension(data, id) {
			paths = append(paths, legacy)
		}
		if err := removeSystemFiles(paths, stdout, stderr); err != nil {
			return fail(err)
		}
		if err := stopServer(p, others.all(), stdout); err != nil {
			return fail(err)
		}
	case errors.Is(err, fs.ErrNotExist):
		// Without the id, the other Chromium browsers that use the shared
		// files are those with a database or a file that names the CRX.
		others = otherUsers(p, cb.Config, stderr)
		users = others.chromium
		fmt.Fprintf(stdout, "No key in %s, so the extension id is unknown. Remove %s by hand if it is there.\n",
			keyFile, filepath.Join(sysroot, cb.ExternalFile("<id>")))
		if _, err := os.Stat(unitPath(p)); err == nil {
			fmt.Fprintf(stdout, "Kept the mirrormark service %s. If no other browser uses mirrormark, stop and remove it with systemctl --user disable --now %s and rm %s.\n", unitPath(p), unitName, quoteArg(unitPath(p)))
		}
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
	switch {
	case installed:
		fmt.Fprintf(stdout, "%s removes mirrormark from every profile when it next starts. Your bookmarks stay in %s.\n", cb.Name, cb.Name)
	case key == nil:
		fmt.Fprintf(stdout, "mirrormark is not installed in %s: no %s database, and without the key its file cannot be checked.\n", cb.Name, cb.Name)
	default:
		fmt.Fprintf(stdout, "mirrormark is not installed in %s: no %s file and no %s database.\n", cb.Name, cb.Name, cb.Name)
	}
	if len(users) > 0 {
		fmt.Fprintf(stdout, "Kept the CRX, which %s still uses.\n", strings.Join(users, " and "))
	}
	return finishUninstall(p, cb.Config, cb.Name, *purge, len(users) > 0, stdout, stderr)
}

// finishUninstall deals with mirrormark's own files once browser's install
// is undone. Without purge it says where they are. With purge it deletes
// browser's databases, with their backups and lock files, and a Chromium
// browser's key unless keyInUse (another Chromium browser still has the
// extension) or another Chromium browser's database is left. The config,
// the log, the server's lock (unless a server still holds it) and the rest
// of the data directory go only with the last database: another browser's profiles still sync through them, and its
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
	serving := false
	if len(others) == 0 {
		files = append(files, p.ConfigFile(), p.GlobalLock())
		dirs = append(dirs, p.ProfilesDir(), filepath.Join(p.Data, "backups"), p.State)
		// A server started by hand may still hold the serve lock. Deleting
		// its file would let a second server start beside it.
		switch unlock, err := db.TryLock(p.ServeLock()); {
		case err == nil:
			defer unlock()
			files = append(files, p.ServeLock())
		case errors.Is(err, db.ErrLocked):
			serving = true
		default:
			return fail(err)
		}
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
	if serving {
		fmt.Fprintf(stdout, "Deleted %s's databases and backups, your config %s and the log in %s. Kept %s: a mirrormark server still runs and holds serve.lock there. Stop it, then delete %s.\n",
			name, p.ConfigFile(), p.State, p.Data, p.Data)
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
// names as b's, puts back the two prefs that weakened its add-on checks,
// and removes the native host manifest an earlier build left if it names
// the add-on. It writes user.js first, so if that fails the add-on is still
// there and running uninstall again finishes the job.
func uninstallFirefox(p db.Paths, b firefox.Browser, stdout io.Writer) error {
	dbs, errs := db.LoadAll(p)
	for _, err := range errs {
		return fmt.Errorf("%w; fix it so uninstall can tell whether it is a %s profile", err, b.Name)
	}
	var files []chrome.File
	var gone []string // XPIs, and the old host manifest if it is this one's
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
	// An earlier build's host manifest starts nothing now, so it goes
	// whatever the other Gecko browsers do.
	legacy, err := firefox.LegacyHostManifest()
	if err != nil {
		return err
	}
	if data, err := os.ReadFile(legacy); err == nil && bytes.Contains(data, []byte(`"`+extension.GeckoID+`"`)) {
		gone = append(gone, legacy)
	}
	if err := chrome.WriteFiles(files); err != nil {
		return err
	}
	if err := chrome.RemoveFiles(gone); err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(stdout, "No %s profile was found in mirrormark's databases in %s, so no user.js was changed. If a profile still has mirrormark, check its user.js for xpinstall.signatures.required and extensions.autoDisableScopes, and set them back to true and 15.\n", b.Name, p.ProfilesDir())
	} else {
		fmt.Fprintf(stdout, "%s removes mirrormark from each profile when it next starts, and checks add-on signatures there again. Your bookmarks stay in %s.\n", b.Name, b.Name)
	}
	return nil
}

// geckoUsers names the Gecko browsers other than except whose profiles,
// as named by dbs, still hold the add-on. A profile it cannot check counts,
// so a shared file goes only when it is known to be unused.
func geckoUsers(dbs map[string]*db.DB, except string, stderr io.Writer) []string {
	var users []string
	for _, d := range dbs {
		o, ok := firefox.Lookup(d.Profile.Browser)
		if !ok || o.Config == except || slices.Contains(users, o.Name) {
			continue
		}
		_, err := os.Stat(firefox.XPIPath(d.Profile.Path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "mirrormark: warning: cannot tell whether %s still uses mirrormark, so keeping the files it shares: %v\n", o.Name, err)
		}
		users = append(users, o.Name)
	}
	return users
}

// chromiumUsers names the Chromium browsers other than except whose
// external extension file for id is in place: they still use the CRX and
// the server except shares with them. A browser whose file it cannot check
// counts as one, so a shared file goes only when it is known to be unused.
func chromiumUsers(except, id string, stderr io.Writer) []string {
	var out []string
	for _, o := range chrome.Browsers {
		if o.Config == except {
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
