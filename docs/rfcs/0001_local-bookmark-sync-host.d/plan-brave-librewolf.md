# mirrormark for Brave and LibreWolf: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. When writing Go, use the `use-modern-go` skill.

**Goal:** Brave and LibreWolf profiles join the same unions as Chrome and Firefox Developer Edition profiles: `mirrormark install --browser brave` and `--browser librewolf` install and bind them, `uninstall` removes them without breaking the other browser of the same family, and the e2e tests show install, bind and sync in both.

**Architecture:** No new install path. `internal/chrome` gets a `Browser` description (Chrome, then Brave) and `internal/firefox` gets one (Developer Edition, then LibreWolf); the existing install, bind and uninstall code in `cmd/mirrormark/install.go` reads paths, names and checks from them. Files that browsers of one family share (Chromium: key, CRX, unpacked extension, system host manifest; Gecko: per-user host manifest) go only with the last browser that still has its own file in place. Nothing in the host, the merge, the extension or the file formats changes.

**Tech Stack:** Go 1.27.2 (standard library plus `go.yaml.in/yaml/v3`); the dev image adds Brave 1.97.57 and LibreWolf 158.0-1 beside Google Chrome 155 and Firefox Developer Edition 159.0b2; e2e tests over the DevTools pipe (Chrome, Brave) and WebDriver BiDi (Developer Edition, LibreWolf).

**Spec:** [`../0001_local-bookmark-sync-host.md`](../0001_local-bookmark-sync-host.md), Section 9 and Phase 6. Findings: [`brave-install-spike.md`](brave-install-spike.md), [`librewolf-install-spike.md`](librewolf-install-spike.md). The code this builds on is described in [`plan.md`](plan.md) and [`plan-firefox.md`](plan-firefox.md).

## Global Constraints

- Everything in the Chrome and Firefox plans' Global Constraints still holds: module, dependencies, pinned versions, `bin/dev run make` only, no `any` values or unchecked type assertions, no RFC numbers, section references or `docs/rfcs/` links in code, comments, tests or build files, README updated in the same commit, every commit with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Chrome and Firefox stay green at every commit:** `bin/dev run make` passes after each task; `bin/dev run make check-e2e` passes after each task from Task 6 on, and before committing Tasks 1–5 run it too (the image of the time has Chrome and Developer Edition).
- **No prefix weakens a profile without the way back.** Each browser's install and uninstall land in one commit (Tasks 4, 5).
- `--browser` values, database `browser:`, id and label prefixes: `chrome`, `brave`, `firefox`, `librewolf`. Ids `<browser>-<slug>`, labels `<browser>/<slug>`, slug as today.
- Chromium descriptions, exactly:
  - Chrome: `Config "chrome"`, `Name "Chrome"`, `DataDir "google-chrome"`, `Bin "google-chrome"`, `ExternalDir "/usr/share/google-chrome/extensions"`.
  - Brave: `Config "brave"`, `Name "Brave"`, `DataDir "BraveSoftware/Brave-Browser"`, `Bin "brave-browser"`, `ExternalDir "/opt/brave.com/brave/extensions"`.
  - Data directory: `$XDG_CONFIG_HOME/<DataDir>` when `$XDG_CONFIG_HOME` is absolute, else `~/.config/<DataDir>`.
  - One system host manifest for both: `/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json`.
- Gecko descriptions, exactly:
  - Developer Edition: `Config "firefox"`, `Name "Firefox Developer Edition"`, `XDGRoot "mozilla/firefox"`, `HomeRoot ".mozilla/firefox"`, `Bin "firefox"`, `Channel "aurora"`, `AppName ""`.
  - LibreWolf: `Config "librewolf"`, `Name "LibreWolf"`, `XDGRoot "librewolf/librewolf"`, `HomeRoot ".librewolf"`, `Bin "librewolf"`, `Channel ""`, `AppName "LibreWolf"`.
  - Profile root: `$XDG_CONFIG_HOME/<XDGRoot>` (`~/.config/<XDGRoot>` when unset or relative) if it holds `profiles.ini`, else `~/<HomeRoot>`.
  - One per-user host manifest for both: `~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json`. The XPI, the three `user.js` lines and `ResetPrefs` are unchanged.
- **Shared-file rules.** Chromium: an uninstall removes the CRX, the unpacked extension and the host manifest only if no other Chromium browser's external file for the id is in place (under `sysroot`); `--purge` deletes the key only if, in addition, no other Chromium browser's database is left. Gecko: an uninstall removes the host manifest only if no database of another Gecko browser names a profile that still holds the XPI.
- **Install writes only the browser asked for:** its external file and the shared host manifest, never another browser's file, so nothing is created under a browser that is not installed. After an upgrade the user runs `install` once per browser.
- **Never-run Gecko profiles** (no `compatibility.ini`): skipped with `skipped: <name>: …` on stdout when no `--profile` was given; refused when named; `install` stops if every listed profile was skipped. (As built in Task 3. [`plan-profile-selection.md`](plan-profile-selection.md) Task 2 replaces the skip: without `--profile`, install only lists, and shows such a profile as `never run`.)
- **Explicit profiles and one-shot binding** ([`plan-profile-selection.md`](plan-profile-selection.md)) land between Task 6 and Task 7. From Task 7 on, every install names its profiles with `--profile` (ID or number), binding opens the page once with no retries (`chrome.Open`, `chrome.BindWait` 30 s; `chrome.Retry` and `firefox.UUIDWait` are gone), and the e2e harness waits in `e.ready(name)` for the profile's files to show mirrormark ready before each bind.
- Dev image: Brave `1.97.57`, `https://brave-browser-apt-release.s3.brave.com/pool/main/b/brave-browser/brave-browser_1.97.57_amd64.deb`, sha256 `d79919a82aa869cf5121b641afa79958730c4bdaf36a0c51ae0f9c32f9c16bb9`. LibreWolf `158.0-1`, `https://dl.librewolf.net/librewolf/158.0-1/librewolf-158.0-1-linux-x86_64-package.tar.xz`, sha256 `34bb7296feec0c6823abdfaf4fd50e47e9eada9cb4a5ed16e111a951dc9eb16c`, unpacked to `/opt/librewolf`.
- The e2e harness keeps writing `user_pref("remote.prefs.recommended", false);` into every Gecko profile it prepares: the remote agent's recommended prefs set `extensions.autoDisableScopes` to 0.
- E2e budget: one new sync pair only, `brave-librewolf`, plus one install test per new browser.

## Verified before planning

From the spikes (2026-10-15, Docker): Brave's external directory, enabled install without prompt, the shared host manifest directory, `Local State`, `$XDG_CONFIG_HOME`, binding with `brave-browser` and the first-start race, Brave's bookmark roots, the DevTools pipe, uninstall by deleting the file, session restore. LibreWolf: profile roots and `$XDG_CONFIG_HOME`, `compatibility.ini`, `application.ini` `Name=LibreWolf`, channel `release`, `MOZ_REQUIRE_SIGNING` off, the three prefs and their defaults, uninstall's reset, the host manifest search order, binding cold and over D-Bus, bookmark roots, BiDi, the binding surviving the sanitize-on-shutdown defaults.

**Not verified, and checked by the task named:** the real `mirrormark` host in Brave and LibreWolf (Task 7); `librewolf -CreateProfile` registering the profile where `install` looks (Task 7, Step 3); Brave in the dev image under `--remote-debugging-pipe` with the harness's flags (Task 7); Brave's `.deb` installing without Brave's apt repository (Task 6, Step 2).

**Not verified, and not tested (accepted risks in the RFC):** Brave package upgrades and removal leaving `/opt/brave.com/brave/extensions/` alone; Brave Sync; LibreWolf from other packagings; a LibreWolf set to clear more at shutdown.

## File structure

```
internal/chrome/
  profiles.go        Browser, Chrome, Browsers, Lookup, InstallCommand,
                     DefaultUserDataDir, Profiles (Task 1); Brave (Task 4)
  files.go           ExternalFile, HostManifestPath, Browser.SystemFiles (Task 1)
  bind.go            Browser.Bind (Task 1)
  chrome_test.go
internal/firefox/
  profiles.go        Browser, Firefox, Browsers, Lookup, DefaultRoot,
                     Profiles (Task 2); LibreWolf (Task 5)
  files.go           Browser.Check replaces Channel (Task 2); ErrNeverRun
                     (Task 3); AppName check (Task 5)
  bind.go            Browser.Bind (Task 2)
  firefox_test.go
cmd/mirrormark/
  install.go         read the descriptions (Tasks 1–2), skip never-run
                     (Task 3), shared Chromium files (Task 4), shared Gecko
                     host manifest (Task 5)
  main.go            usage (Tasks 4–5)
  install_test.go
Dockerfile, Makefile Brave, LibreWolf (Task 6)
e2e/
  env.go             kinds chrome|brave|firefox|librewolf (Task 7)
  browser.go, bidi.go  launch by kind (Task 7)
  install_test.go    TestBraveInstallAndBind, TestLibreWolfInstallAndBind (Task 7)
  sync_test.go       pair brave-librewolf (Task 8)
README.md            Tasks 3, 4, 5, 6, 7, 8
```

## Interfaces between tasks

```go
// internal/chrome (Task 1; Brave added in Task 4)
type Browser struct{ Config, Name, DataDir, Bin, ExternalDir string }
var Chrome, Brave Browser
var Browsers []Browser
func Lookup(config string) (Browser, bool)
func (b Browser) InstallCommand() string            // "mirrormark install" for Chrome, else "… --browser <Config>"
func (b Browser) DefaultUserDataDir() (string, error)
func (b Browser) Profiles(userDataDir string) ([]Profile, error)
func (b Browser) ExternalFile(id string) string
func HostManifestPath() string
func (b Browser) SystemFiles(e Extension, bin string) []File      // b's external file, then the host manifest
func (b Browser) Bind(bin, userDataDir, profileDir, extID, dbID string, bound func(since time.Time) bool) error
// removed: ExternalDir const, SystemFilePaths, package-level DefaultUserDataDir, Profiles, Bind

// internal/firefox (Task 2; ErrNeverRun Task 3; LibreWolf Task 5)
type Browser struct{ Config, Name, XDGRoot, HomeRoot, Bin, Channel, AppName string }
var Firefox, LibreWolf Browser
var Browsers []Browser
func Lookup(config string) (Browser, bool)
func (b Browser) DefaultRoot() (string, error)
func (b Browser) Profiles(root string) ([]chrome.Profile, error)
func (b Browser) Check(profileDir string) (bin string, err error)   // replaces Channel
var ErrNeverRun error
func (b Browser) Bind(bin, profileDir, dbID string, bound func(since time.Time) bool) error
// unchanged: Dir, XPI, XPIPath, HostManifestPath, Files, Prefs, ResetPrefs, UUID, UUIDWait
// (plan-profile-selection.md Task 1 then changes Bind to take the UUID, adds
// ReadState and chrome.ReadState, and removes UUIDWait and chrome.Retry)

// cmd/mirrormark
func installFirefox(p db.Paths, b firefox.Browser, root string, picks []string, stdout, stderr io.Writer) int   // Task 2
func uninstallFirefox(p db.Paths, b firefox.Browser, stdout io.Writer) error                                  // Task 2
func finishUninstall(p db.Paths, browser, name string, purge bool, stdout, stderr io.Writer) int               // Task 2
func finishUninstall(p db.Paths, browser, name string, purge, keyInUse bool, stdout, stderr io.Writer) int     // Task 4
func chromiumUsers(b chrome.Browser, id string) []string                                                       // Task 4: names

// e2e (Task 7)
var bins map[string]string        // kind → command
var names map[string]string       // kind → name in install's restart line
func gecko(kind string) bool
func launch(t *testing.T, name, kind, udd string, env []string) *Browser
func launchFirefox(t *testing.T, name, kind, dir string, port int, env []string) *Browser
```

## Tasks

| # | Commit subject |
| --- | --- |
| 1 | `refactor(rfc-0001): describe Chrome as one Chromium browser` |
| 2 | `refactor(rfc-0001): describe Firefox Developer Edition as one Gecko browser` |
| 3 | `feat(rfc-0001): skip profiles that have never run unless named` |
| 4 | `feat(rfc-0001): install into and uninstall from Brave [new feature]` |
| 5 | `feat(rfc-0001): install into and uninstall from LibreWolf [new feature]` |
| 6 | `build(rfc-0001): add Brave and LibreWolf to the dev image` |
| P1 | `feat(rfc-0001): bind each profile once, only where mirrormark is ready` ([`plan-profile-selection.md`](plan-profile-selection.md) Task 1) |
| P2 | `feat(rfc-0001): list the profiles, and install only into those named [new feature]` ([`plan-profile-selection.md`](plan-profile-selection.md) Task 2) |
| 7 | `test(rfc-0001): install and bind Brave and LibreWolf end to end` |
| 8 | `test(rfc-0001): run the sync tests between Brave and LibreWolf` |

Tasks 1–2 change nothing a user sees except messages that now name the browser. Task 3 is a compatibility change LibreWolf needs, and applies to Developer Edition too. Tasks 4 and 5 each deliver one browser with its install, uninstall, shared-file rules, unit tests and README. Tasks 6–8 prove them end to end. P1 and P2, planned in their own file after the user tried Brave on a desktop, land between Task 6 and Task 7, so Tasks 7–8 are written for their flow: an explicit `--profile` and one open per ready profile. Any prefix of the stack can land.

---

### Task 1: Describe Chrome as one Chromium browser

**Files:**
- Modify: `internal/chrome/profiles.go`, `internal/chrome/files.go`, `internal/chrome/bind.go`
- Modify: `cmd/mirrormark/install.go`
- Test: `internal/chrome/chrome_test.go`, `cmd/mirrormark/install_test.go`

**Interfaces:**
- Consumes: the code as it is.
- Produces: `chrome.Browser`, `chrome.Chrome`, `chrome.Browsers`, `chrome.Lookup`, `Browser.InstallCommand`, `Browser.DefaultUserDataDir`, `Browser.Profiles`, `Browser.ExternalFile`, `chrome.HostManifestPath`, `Browser.SystemFiles(e, bin)`, `Browser.Bind`. Removes `ExternalDir`, `SystemFilePaths` and the package-level `DefaultUserDataDir`, `Profiles`, `Bind`.

Behaviour is unchanged: every output line and every file Chrome's install writes stays byte for byte the same, so the existing tests are the regression test once their call sites are updated.

- [ ] **Step 1: Update the tests to the new names (they fail to compile)**

In `internal/chrome/chrome_test.go`:
- `DefaultUserDataDir()` → `Chrome.DefaultUserDataDir()` (3 places);
- `Profiles(udd)` → `Chrome.Profiles(udd)` (every call);
- `Bind(` → `Chrome.Bind(` (every call);
- in `TestSystemFiles`, `SystemFiles(e, "/usr/local/bin/mirrormark")` → `Chrome.SystemFiles(e, "/usr/local/bin/mirrormark")`, and replace its last check with:

```go
	if fs[0].Path != Chrome.ExternalFile(e.ID) || fs[1].Path != HostManifestPath() || len(fs) != 2 {
		t.Fatalf("paths %s %s", fs[0].Path, fs[1].Path)
	}
```

Add:

```go
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
}
```

In `cmd/mirrormark/install_test.go`, replace each `chrome.SystemFilePaths(id)` (and `chrome.SystemFilePaths(chrome.ExtensionID(...))`) with `[]string{chrome.Chrome.ExternalFile(id), chrome.HostManifestPath()}` using the same id expression, and in `TestUninstallLeavesAnotherExtensionsHostManifest` replace `chrome.SystemFiles(chrome.Extension{…}, "/bin/x")[1]` with `chrome.Chrome.SystemFiles(chrome.Extension{…}, "/bin/x")[1]` (same literal).

- [ ] **Step 2: Run to see them fail**

Run: `bin/dev run go test ./internal/chrome/ ./cmd/mirrormark/`
Expected: FAIL to compile: `undefined: Chrome`, `undefined: HostManifestPath`.

- [ ] **Step 3: The description, in `internal/chrome/profiles.go`**

Package comment: replace "installs mirrormark into Google Chrome on Linux" with "installs mirrormark into Chromium-based browsers on Linux, Google Chrome first", and "the two system files Chrome reads" with "the system files they read". Replace `DefaultUserDataDir` and `Profiles` with:

```go
// Browser is one Chromium-based browser mirrormark installs into. They
// share the key, the CRX, the extension id and the system host manifest;
// each has its own data directory, command and external extensions
// directory.
type Browser struct {
	Config      string // the --browser value, and browser: in a database
	Name        string // what messages call it
	DataDir     string // its data directory under $XDG_CONFIG_HOME or ~/.config
	Bin         string // the command that starts it
	ExternalDir string // where it reads external extension files at start
}

// Chrome is Google Chrome. On Linux it installs a CRX named by a file in
// its external extensions directory, without a prompt, in every profile.
var Chrome = Browser{Config: "chrome", Name: "Chrome", DataDir: "google-chrome", Bin: "google-chrome",
	ExternalDir: "/usr/share/google-chrome/extensions"}

// Browsers are the Chromium-based browsers install knows.
var Browsers = []Browser{Chrome}

// Lookup returns the browser whose Config is config.
func Lookup(config string) (Browser, bool) {
	i := slices.IndexFunc(Browsers, func(b Browser) bool { return b.Config == config })
	if i < 0 {
		return Browser{}, false
	}
	return Browsers[i], true
}

// InstallCommand is the command that installs into b, as messages tell
// the user to run it; Chrome is install's default.
func (b Browser) InstallCommand() string {
	if b.Config == Chrome.Config {
		return "mirrormark install"
	}
	return "mirrormark install --browser " + b.Config
}

// DefaultUserDataDir is b's default data directory on Linux.
func (b Browser) DefaultUserDataDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		return filepath.Join(v, b.DataDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", b.DataDir), nil
}

// Profiles lists the profiles in b's "Local State" file, sorted by
// directory. The file is not a documented interface; it has held
// profile.info_cache for years.
func (b Browser) Profiles(userDataDir string) ([]Profile, error) {
	data, err := os.ReadFile(filepath.Join(userDataDir, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s's profile list (has %s run with %s?): %w", b.Name, b.Name, userDataDir, err)
	}
	var ls struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(userDataDir, "Local State"), err)
	}
	var out []Profile
	for dir, info := range ls.Profile.InfoCache {
		out = append(out, Profile{Dir: dir, Name: info.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no profiles in %s; start %s once and close it", b.Name, userDataDir, b.Name)
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(a.Dir, b.Dir) })
	return out, nil
}
```

- [ ] **Step 4: Files, in `internal/chrome/files.go`**

Delete the `ExternalDir` constant (keep `SystemHostDir`, its comment now "native host manifests here reach every data directory of every Chromium browser mirrormark supports"). Replace `SystemFilePaths` and `SystemFiles` with:

```go
// ExternalFile is the file that tells b to install the extension id.
func (b Browser) ExternalFile(id string) string { return filepath.Join(b.ExternalDir, id+".json") }

// HostManifestPath is the native host manifest every browser in Browsers
// reads.
func HostManifestPath() string { return filepath.Join(SystemHostDir, extension.HostName+".json") }

// SystemFiles are the two files that tell b about the extension and let it
// start bin as the native host: b's external file, then the host manifest.
func (b Browser) SystemFiles(e Extension, bin string) []File {
	return []File{
		{b.ExternalFile(e.ID), indented(externalJSON{CRX: e.CRX, Version: e.Version})},
		{HostManifestPath(), indented(hostManifestJSON{Name: extension.HostName, Description: "mirrormark bookmark sync",
			Path: bin, Type: "stdio", AllowedOrigins: []string{"chrome-extension://" + e.ID + "/"}})},
	}
}
```

- [ ] **Step 5: Bind, in `internal/chrome/bind.go`**

```go
// Bind opens bind.html#<dbID> in one of b's profiles with bin and waits
// for bound to report the host's hello since the attempt started.
func (b Browser) Bind(bin, userDataDir, profileDir, extID, dbID string, bound func(since time.Time) bool) error {
	def, _ := b.DefaultUserDataDir()
	var args []string
	if userDataDir != def {
		args = append(args, "--user-data-dir="+userDataDir)
	}
	args = append(args, "--profile-directory="+profileDir, "chrome-extension://"+extID+"/bind.html#"+dbID)
	ok, err := Retry(bin, args, bound)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("profile %s did not confirm within %d tries; check that %s was restarted after install and that mirrormark is enabled in chrome://extensions there, then run %s again", profileDir, bindTries, b.Name, b.InstallCommand())
}
```

- [ ] **Step 6: Install and uninstall read the description, in `cmd/mirrormark/install.go`**

In `cmdInstall`, replace the `switch *browser { … }` with:

```go
	if *browser == "firefox" {
		return installFirefox(p, *udd, picks, stdout, stderr)
	}
	cb, ok := chrome.Lookup(*browser)
	if !ok {
		fmt.Fprintf(stderr, "mirrormark: --browser is chrome or firefox, not %q\n", *browser)
		return 2
	}
```

and in the rest of the Chrome path:
- `chrome.DefaultUserDataDir()` → `cb.DefaultUserDataDir()`, `chrome.Profiles(*udd)` → `cb.Profiles(*udd)`;
- `system := chrome.SystemFiles(ext, self)` → `system := cb.SystemFiles(ext, self)` (the rest of that block, `system[1]` included, is unchanged);
- the sudo failure: `fmt.Errorf("could not copy the files %s needs into place (with sudo): %w", cb.Name, err)`;
- `ensureDB(p, "chrome", …)` → `ensureDB(p, cb.Config, …)`;
- the restart line: `fmt.Fprintf(stdout, "Restart %s (close every window), then run %s again to bind the profiles.\n", cb.Name, cb.InstallCommand())`;
- `exec.LookPath("google-chrome")` → `exec.LookPath(cb.Bin)`; `chrome.Bind(bin, …)` → `cb.Bind(bin, …)`;
- the closing line: `"Now list the folders to share in %s.\nExport your bookmarks from %s first, so you have a copy.\nThe host logs to %s.\n", p.ConfigFile(), cb.Name, p.LogFile()`.

In `cmdUninstall`, after the Firefox branch add `cb, _ := chrome.Lookup(*browser) // checked above`, and replace the key `switch` and the line after it with:

```go
	keyFile := filepath.Join(p.Data, "extension.pem")
	key, err := chrome.LoadKey(keyFile)
	host := filepath.Join(sysroot, chrome.HostManifestPath())
	switch {
	case err == nil:
		id := chrome.ExtensionID(chrome.PublicKeyDER(key))
		paths := []string{filepath.Join(sysroot, cb.ExternalFile(id))}
		// The host manifest's name is fixed; one that names another
		// extension belongs to another install.
		if data, err := os.ReadFile(host); err == nil && namesExtension(data, id) {
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
```

and the message after removing the CRX and the unpacked extension:

```go
	fmt.Fprintf(stdout, "%s removes mirrormark from every profile when it next starts. Your bookmarks stay in %s.\n", cb.Name, cb.Name)
```

- [ ] **Step 7: Run**

Run: `bin/dev run make`
Expected: PASS. Then `bin/dev run make check-e2e RUN='TestInstallAndBind|TestBindStartsChrome'` → PASS.

- [ ] **Step 8: Commit** subject `refactor(rfc-0001): describe Chrome as one Chromium browser`.

---

### Task 2: Describe Firefox Developer Edition as one Gecko browser

**Files:**
- Modify: `internal/firefox/profiles.go`, `internal/firefox/files.go`, `internal/firefox/bind.go`
- Modify: `cmd/mirrormark/install.go`
- Test: `internal/firefox/firefox_test.go`

**Interfaces:**
- Consumes: Task 1's `chrome.Lookup`.
- Produces: `firefox.Browser`, `firefox.Firefox`, `firefox.Browsers`, `firefox.Lookup`, `Browser.DefaultRoot`, `Browser.Profiles`, `Browser.Check` (replaces `Channel`), `Browser.Bind`; `installFirefox(p, b, root, picks, stdout, stderr)`, `uninstallFirefox(p, b, stdout)`, `finishUninstall(p, browser, name, purge, stdout, stderr)`.

Behaviour is unchanged except that Firefox's messages say "Firefox Developer Edition" where they said "Firefox"; every test's substring checks (`Restart Firefox`, `start Firefox`, `restart Firefox`, `Developer Edition`, `unsigned`) still hold.

- [ ] **Step 1: Update the tests to the new names**

In `internal/firefox/firefox_test.go`: `DefaultRoot()` → `Firefox.DefaultRoot()`, `Profiles(` → `Firefox.Profiles(`, `Bind(` → `Firefox.Bind(`, and replace `TestChannelFromTheProfilesFirefox` with:

```go
func TestCheckTakesOnlyDeveloperEdition(t *testing.T) {
	ff := t.TempDir()
	os.MkdirAll(filepath.Join(ff, "defaults", "pref"), 0o755)
	prefs := filepath.Join(ff, "defaults", "pref", "channel-prefs.js")
	os.WriteFile(prefs, []byte(`pref("app.update.channel", "aurora");`+"\n"), 0o644)
	dir := t.TempDir()
	if _, err := Firefox.Check(dir); err == nil || !strings.Contains(err.Error(), "start Firefox") {
		t.Fatalf("no compatibility.ini: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte("[Compatibility]\nLastVersion=159.0_20261001000000/20261001000000\nLastPlatformDir="+ff+"\nLastAppDir="+ff+"/browser\n"), 0o644)
	if bin, err := Firefox.Check(dir); err != nil || bin != filepath.Join(ff, "firefox") {
		t.Fatalf("%s %v", bin, err)
	}
	os.WriteFile(prefs, []byte(`pref("app.update.channel", "release");`+"\n"), 0o644)
	if _, err := Firefox.Check(dir); err == nil || !strings.Contains(err.Error(), "release") {
		t.Fatalf("release: %v", err)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `bin/dev run go test ./internal/firefox/`
Expected: FAIL to compile: `undefined: Firefox`.

- [ ] **Step 3: The description, in `internal/firefox/profiles.go`**

Package comment: "installs mirrormark into Firefox Developer Edition on Linux" → "installs mirrormark into Firefox-family browsers on Linux, Firefox Developer Edition first". Replace `DefaultRoot` with the type and its method, and make `Profiles` a method:

```go
// Browser is one Gecko browser mirrormark installs into. They share the
// XPI, the user.js prefs and the per-user host manifest; each has its own
// profiles root and binary, and its own way to tell its profiles from
// other Gecko browsers'.
type Browser struct {
	Config   string // the --browser value, and browser: in a database
	Name     string // what messages call it
	XDGRoot  string // the profiles root under $XDG_CONFIG_HOME, if it holds profiles.ini
	HomeRoot string // the profiles root under the home directory otherwise
	Bin      string // its binary, in the directory compatibility.ini names
	Channel  string // the update channel its builds have, if it is told by that
	AppName  string // the Name in its application.ini, if it is told by that
}

// Firefox is Firefox Developer Edition: release Firefox installs only
// add-ons Mozilla signed, Developer Edition honours
// xpinstall.signatures.required.
var Firefox = Browser{Config: "firefox", Name: "Firefox Developer Edition", XDGRoot: "mozilla/firefox",
	HomeRoot: ".mozilla/firefox", Bin: "firefox", Channel: "aurora"}

// Browsers are the Gecko browsers install knows.
var Browsers = []Browser{Firefox}

// Lookup returns the browser whose Config is config.
func Lookup(config string) (Browser, bool) {
	i := slices.IndexFunc(Browsers, func(b Browser) bool { return b.Config == config })
	if i < 0 {
		return Browser{}, false
	}
	return Browsers[i], true
}

// DefaultRoot is the directory holding b's profiles.ini: the XDG location,
// if it holds one, else the legacy one in the home directory.
func (b Browser) DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cfg := filepath.Join(home, ".config")
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		cfg = v
	}
	if r := filepath.Join(cfg, b.XDGRoot); fileExists(filepath.Join(r, "profiles.ini")) {
		return r, nil
	}
	return filepath.Join(home, b.HomeRoot), nil
}
```

`Profiles` keeps its body, becomes `func (b Browser) Profiles(root string) ([]chrome.Profile, error)`, and its last error becomes `fmt.Errorf("no %s profiles in %s; start %s once and close it", b.Name, root, b.Name)`.

- [ ] **Step 4: `Check`, in `internal/firefox/files.go`**

Replace `Channel` with:

```go
// Check returns the binary of the browser that last ran the profile, from
// its compatibility.ini, or why b cannot install into the profile: it was
// last run by another browser, or nothing tells which.
func (b Browser) Check(profileDir string) (bin string, err error) {
	data, err := os.ReadFile(filepath.Join(profileDir, "compatibility.ini"))
	if err != nil {
		return "", fmt.Errorf("cannot tell which browser uses %s; start %s in that profile once and close it: %w", profileDir, b.Name, err)
	}
	var dir string
	for _, s := range iniSections(string(data)) {
		if s["[]"] == "Compatibility" {
			dir = s["LastPlatformDir"]
		}
	}
	if dir == "" {
		return "", fmt.Errorf("%s names no browser (LastPlatformDir)", filepath.Join(profileDir, "compatibility.ini"))
	}
	if b.Channel != "" {
		prefs, err := os.ReadFile(filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
		if err != nil {
			return "", fmt.Errorf("cannot read the update channel of the Firefox in %s: %w", dir, err)
		}
		m := channelRe.FindSubmatch(prefs)
		if m == nil {
			return "", fmt.Errorf("no update channel in %s", filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
		}
		if string(m[1]) != b.Channel {
			return "", fmt.Errorf("its Firefox is on the %s channel, which installs only add-ons Mozilla signed; mirrormark works only in Firefox Developer Edition for now", m[1])
		}
	}
	return filepath.Join(dir, b.Bin), nil
}
```

- [ ] **Step 5: `Bind`, in `internal/firefox/bind.go`**

`func Bind(bin, profileDir, …)` → `func (b Browser) Bind(bin, profileDir, dbID string, bound func(since time.Time) bool) error`, with the body unchanged except the two errors:

```go
			return fmt.Errorf("%s has not installed mirrormark in %s; restart %s there (close every window), then run mirrormark install --browser %s again", b.Name, profileDir, b.Name, b.Config)
```

```go
	return fmt.Errorf("profile %s did not confirm; check that mirrormark is enabled in about:addons there, then run mirrormark install --browser %s again", profileDir, b.Config)
```

- [ ] **Step 6: `cmd/mirrormark/install.go`**

`cmdInstall`: replace `if *browser == "firefox" { return installFirefox(p, *udd, picks, stdout, stderr) }` with:

```go
	if fb, ok := firefox.Lookup(*browser); ok {
		return installFirefox(p, fb, *udd, picks, stdout, stderr)
	}
```

Replace the `firefoxSafety` constant with:

```go
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
```

`installFirefox(p db.Paths, b firefox.Browser, root string, picks []string, stdout, stderr io.Writer) int`, with comment "installs into profiles of b, as the user: … It refuses profiles of any other browser before writing anything, since those install only signed add-ons." In its body: `firefox.DefaultRoot()` → `b.DefaultRoot()`, `firefox.Profiles(root)` → `b.Profiles(root)`; the target loop becomes

```go
	for _, c := range chosen {
		dir := firefox.Dir(root, c)
		bin, err := b.Check(dir)
		if err != nil {
			refused = append(refused, fmt.Sprintf("%s: %v", c.Name, err))
			continue
		}
		targets = append(targets, target{c, dir, bin})
	}
```

`ensureDB(p, "firefox", …)` → `ensureDB(p, b.Config, …)`; `fmt.Fprintln(stdout, firefoxSafety)` → `fmt.Fprintln(stdout, geckoSafety(b))`; the restart line `fmt.Fprintf(stdout, "Restart %s in these profiles if it is running (close every window), then run mirrormark install --browser %s again to bind them.\n", b.Name, b.Config)`; `firefox.Bind(tg.bin, …)` → `b.Bind(tg.bin, …)`; the closing line `"Now list the folders to share in %s.\nExport your bookmarks from %s first (Bookmarks, Manage bookmarks, Import and Backup), so you have a copy.\nThe host logs to %s.\n", p.ConfigFile(), b.Name, p.LogFile()`.

`cmdUninstall`: replace the browser check and the Firefox branch with

```go
	_, isChromium := chrome.Lookup(*browser)
	fb, isGecko := firefox.Lookup(*browser)
	if !isChromium && !isGecko {
		fmt.Fprintf(stderr, "mirrormark: --browser is chrome or firefox, not %q\n", *browser)
		return 2
	}
```

(before `refuseRoot`, where the old check was) and, after `p` is known,

```go
	if isGecko {
		if err := uninstallFirefox(p, fb, stdout); err != nil {
			return fail(err)
		}
		return finishUninstall(p, fb.Config, fb.Name, *purge, stdout, stderr)
	}
```

The Chrome path ends `return finishUninstall(p, cb.Config, cb.Name, *purge, stdout, stderr)`.

`finishUninstall(p db.Paths, browser, name string, purge bool, stdout, stderr io.Writer) int`: delete the `name := map[string]string{…}[browser]` line; the rest is unchanged.

`uninstallFirefox(p db.Paths, b firefox.Browser, stdout io.Writer) error`: comment "removes the add-on from every profile a database file names as b's, …"; `if d.Profile.Browser != "firefox"` → `if d.Profile.Browser != b.Config`; the error `"%w; fix it so uninstall can tell whether it is a %s profile", err, b.Name`; the not-found line `"No %s profile was found in mirrormark's databases in %s, so no user.js was changed. …", b.Name, p.ProfilesDir()`; the last line `fmt.Fprintf(stdout, "%s removes mirrormark from each profile when it next starts, and checks add-on signatures there again. Your bookmarks stay in %s.\n", b.Name, b.Name)`.

- [ ] **Step 7: Run**

Run: `bin/dev run make`
Expected: PASS. Then `bin/dev run make check-e2e RUN='TestFirefox'` → PASS.

- [ ] **Step 8: Commit** subject `refactor(rfc-0001): describe Firefox Developer Edition as one Gecko browser`.

---

### Task 3: Skip profiles that have never run unless named

**Files:**
- Modify: `internal/firefox/files.go`, `cmd/mirrormark/install.go`, `README.md`
- Test: `internal/firefox/firefox_test.go`, `cmd/mirrormark/install_test.go`

**Interfaces:**
- Consumes: Task 2's `Browser.Check`, `installFirefox`.
- Produces: `firefox.ErrNeverRun`.

- [ ] **Step 1: Write the failing tests**

`internal/firefox/firefox_test.go` (add `"errors"` to the imports):

```go
func TestCheckSaysWhenAProfileHasNeverRun(t *testing.T) {
	_, err := Firefox.Check(t.TempDir())
	if !errors.Is(err, ErrNeverRun) || !strings.Contains(err.Error(), "start Firefox Developer Edition") {
		t.Fatalf("err %v", err)
	}
}
```

`cmd/mirrormark/install_test.go`:

```go
// neverRun lists profile name in root's profiles.ini with a directory but
// no compatibility.ini, like the second profile LibreWolf makes.
func neverRun(t *testing.T, root, name string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(root, "x"+name+"."+name), 0o700))
	f, err := os.OpenFile(filepath.Join(root, "profiles.ini"), os.O_APPEND|os.O_WRONLY, 0)
	must(t, err)
	_, err = fmt.Fprintf(f, "[Profile9]\nName=%s\nIsRelative=1\nPath=x%s.%s\n", name, name, name)
	must(t, err)
	must(t, f.Close())
}

func TestInstallSkipsAProfileThatHasNeverRun(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	neverRun(t, root, "gamma")
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "skipped: gamma: it has never run") {
		t.Fatalf("output:\n%s", out.String())
	}
	mustExist(t, "install", p.ProfileFile("firefox-alpha"), filepath.Join(root, "xalpha.alpha", "user.js"))
	mustBeGone(t, "install", p.ProfileFile("firefox-gamma"), filepath.Join(root, "xgamma.gamma", "user.js"))
}

func TestInstallRefusesANamedProfileThatHasNeverRun(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	neverRun(t, root, "gamma")
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "firefox", "--profile", "alpha", "--profile", "gamma"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "gamma: it has never run") {
		t.Fatalf("stderr %q", errb.String())
	}
	mustBeGone(t, "refused install", filepath.Join(root, "xalpha.alpha", "user.js"), p.ProfileFile("firefox-alpha"))
}

func TestInstallStopsWhenNoProfileHasRun(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	for _, n := range []string{"alpha", "beta"} {
		must(t, os.Remove(filepath.Join(root, "x"+n+"."+n, "compatibility.ini")))
	}
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "never run") {
		t.Fatalf("stderr %q", errb.String())
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 0 {
		t.Fatalf("made databases %v", dbs)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `bin/dev run go test ./internal/firefox/ ./cmd/mirrormark/ -run 'NeverRun|HasRun'`
Expected: FAIL: `undefined: ErrNeverRun`, then the install tests failing on the refusal.

- [ ] **Step 3: `ErrNeverRun`, in `internal/firefox/files.go`**

```go
// ErrNeverRun means no browser has run the profile yet, so nothing in it
// says which browser it belongs to. LibreWolf makes a second profile at its
// first start that never runs.
var ErrNeverRun = errors.New("it has never run")
```

In `Check`, the `compatibility.ini` read becomes:

```go
	data, err := os.ReadFile(filepath.Join(profileDir, "compatibility.ini"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w; start %s in it once and close it", ErrNeverRun, b.Name)
	}
	if err != nil {
		return "", fmt.Errorf("cannot tell which browser uses %s: %w", profileDir, err)
	}
```

- [ ] **Step 4: Skip in `installFirefox`**

```go
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
```

- [ ] **Step 5: README.** In "Install (Firefox Developer Edition on Linux)" step 1, after "`--user-data-dir DIR` reads the profiles in `DIR` instead.", add: "A profile that has never run is skipped with a `skipped:` line, unless you name it with `--profile`; start Firefox in it once, then run `install` again to add it."

- [ ] **Step 6: Run** `bin/dev run make` → PASS; `bin/dev run make check-e2e RUN='TestFirefox'` → PASS.

- [ ] **Step 7: Commit** subject `feat(rfc-0001): skip profiles that have never run unless named`.

---

### Task 4: Install into and uninstall from Brave

**Files:**
- Modify: `internal/chrome/profiles.go`, `cmd/mirrormark/install.go`, `cmd/mirrormark/main.go`, `README.md`
- Test: `internal/chrome/chrome_test.go`, `cmd/mirrormark/install_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces: `chrome.Brave`; `chromiumUsers`; `finishUninstall(p, browser, name, purge, keyInUse, stdout, stderr)`.

- [ ] **Step 1: Write the failing tests**

`internal/chrome/chrome_test.go`:

```go
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
}
```

`cmd/mirrormark/install_test.go`. First the helpers: in `testHome`, write the guard script as both `google-chrome` and `brave-browser`:

```go
	for _, n := range []string{"google-chrome", "brave-browser"} {
		os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\necho 'unit tests must not start a browser' >&2\nexit 1\n"), 0o755)
	}
```

in `fakeSystem`, `for _, d := range []string{"usr", "etc", "opt"}`; in `fakeChromeOnPath`, write `script` as both `google-chrome` and `brave-browser` in `dir` (same log). Then:

```go
// installedID is the extension id of the key install made.
func installedID(t *testing.T, p db.Paths) string {
	t.Helper()
	key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem"))
	must(t, err)
	return chrome.ExtensionID(chrome.PublicKeyDER(key))
}

// installChromium runs install's first pass for each browser, each in a
// data directory of its own with profiles Default ("Personal") and
// Profile 1 ("Work").
func installChromium(t *testing.T, browsers ...string) {
	t.Helper()
	for _, b := range browsers {
		var out, errb bytes.Buffer
		if code := run([]string{"install", "--browser", b, "--user-data-dir", chromeData(t)}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("install %s: exit %d: %s%s", b, code, out.String(), errb.String())
		}
	}
}

func TestInstallIntoBraveThenBind(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := fakeSystem(t)
	calls := fakeChromeOnPath(t, p)
	udd := chromeData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Restart Brave") || !strings.Contains(out.String(), "mirrormark install --browser brave again") {
		t.Fatalf("output:\n%s", out.String())
	}
	id := installedID(t, p)
	mustExist(t, "install", filepath.Join(root, chrome.Brave.ExternalFile(id)), filepath.Join(root, chrome.HostManifestPath()))
	mustBeGone(t, "install", filepath.Join(root, chrome.Chrome.ExternalFile(id)))
	for dir, id := range map[string]string{"Default": "brave-personal", "Profile 1": "brave-work"} {
		d, err := db.LoadDB(p.ProfileFile(id))
		if err != nil || d.Profile.Browser != "brave" || d.Profile.Label != "brave/"+strings.TrimPrefix(id, "brave-") || d.Profile.Path != filepath.Join(udd, dir) {
			t.Fatalf("%s: %+v, %v", id, d, err)
		}
	}

	out.Reset()
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "bound: Profile 1 -> brave-work\n") {
		t.Fatalf("output:\n%s", out.String())
	}
	if c, _ := os.ReadFile(calls); !bytes.Contains(c, []byte("--user-data-dir="+udd+" --profile-directory=Profile 1 chrome-extension://"+id+"/bind.html#brave-work\n")) {
		t.Fatalf("calls:\n%s", c)
	}
}

// The CRX and the host manifest serve every Chromium browser: they go with
// the last one's external file, whichever goes first.
func TestUninstallingOneChromiumBrowserKeepsTheSharedFiles(t *testing.T) {
	for _, c := range []struct{ gone, stays chrome.Browser }{{chrome.Brave, chrome.Chrome}, {chrome.Chrome, chrome.Brave}} {
		t.Run(c.gone.Config, func(t *testing.T) {
			skipIfRoot(t)
			p := testHome(t)
			root, _ := fakeSystem(t)
			fakeChromeOnPath(t, p)
			installChromium(t, "chrome", "brave")
			id := installedID(t, p)
			host, crx := filepath.Join(root, chrome.HostManifestPath()), filepath.Join(p.Data, "mirrormark.crx")
			var out, errb bytes.Buffer
			if code := run([]string{"uninstall", "--browser", c.gone.Config}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, "first uninstall", filepath.Join(root, c.gone.ExternalFile(id)))
			mustExist(t, "first uninstall", filepath.Join(root, c.stays.ExternalFile(id)), host, crx, filepath.Join(p.Data, "extension"))
			if !strings.Contains(out.String(), "which "+c.stays.Name+" still uses") {
				t.Fatalf("output:\n%s", out.String())
			}
			if code := run([]string{"uninstall", "--browser", c.stays.Config}, strings.NewReader(""), &out, &errb); code != 0 {
				t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
			}
			mustBeGone(t, "second uninstall", filepath.Join(root, c.stays.ExternalFile(id)), host, crx)
		})
	}
}

// The key fixes the id both browsers use; purging Brave keeps it while
// Chrome is installed.
func TestBravePurgeKeepsTheKeyWhileChromeIsInstalled(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	fakeChromeOnPath(t, p)
	installChromium(t, "chrome", "brave")
	var out, errb bytes.Buffer
	if code := run([]string{"uninstall", "--browser", "brave", "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "brave purge", p.ProfileFile("brave-personal"), p.ProfileFile("brave-work"))
	mustExist(t, "brave purge", filepath.Join(p.Data, "extension.pem"), p.ProfileFile("chrome-personal"), p.ConfigFile())
	if code := run([]string{"uninstall", "--purge"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "last purge", p.ConfigFile(), p.Data, p.State)
}
```

- [ ] **Step 2: Run to see them fail**

Run: `bin/dev run go test ./internal/chrome/ ./cmd/mirrormark/`
Expected: FAIL: `undefined: Brave`.

- [ ] **Step 3: Brave's description, in `internal/chrome/profiles.go`**

```go
// Brave is Brave stable from Brave's package. It reads external extension
// files from its own install directory, and native host manifests from
// Chrome's system directory, so it shares Chrome's host manifest.
var Brave = Browser{Config: "brave", Name: "Brave", DataDir: "BraveSoftware/Brave-Browser", Bin: "brave-browser",
	ExternalDir: "/opt/brave.com/brave/extensions"}

// Browsers are the Chromium-based browsers install knows.
var Browsers = []Browser{Chrome, Brave}
```

- [ ] **Step 4: Shared files, in `cmd/mirrormark/install.go`**

Add:

```go
// chromiumUsers names the Chromium browsers other than b whose external
// extension file for id is in place: they still use the CRX and the host
// manifest b shares with them.
func chromiumUsers(b chrome.Browser, id string) []string {
	var out []string
	for _, o := range chrome.Browsers {
		if o.Config == b.Config {
			continue
		}
		if _, err := os.Stat(filepath.Join(sysroot, o.ExternalFile(id))); err == nil {
			out = append(out, o.Name)
		}
	}
	return out
}
```

`cmdInstall` keeps Task 1's shape: it writes `cb.SystemFiles(ext, self)`, that is `cb`'s external file and the host manifest, and never another browser's file. An install for Brave on a computer without Brave fails at `cb.Profiles` (no `Local State`) before anything is written, so it creates nothing under `/opt/brave.com`.

`writeSystemFiles`: the line becomes `fmt.Fprintln(stdout, "The browser reads these files only from root's directories; writing them with sudo:")` and its comment "writes the browsers' root-owned files". `removeSystemFiles`: `fmt.Fprintln(stdout, "Removing these files needs root:\n  sudo", strings.Join(args, " "))`.

In `cmdUninstall`, the Chrome path becomes:

```go
	keyFile := filepath.Join(p.Data, "extension.pem")
	key, err := chrome.LoadKey(keyFile)
	host := filepath.Join(sysroot, chrome.HostManifestPath())
	var users []string // other Chromium browsers that still have the extension
	switch {
	case err == nil:
		id := chrome.ExtensionID(chrome.PublicKeyDER(key))
		users = chromiumUsers(cb, id)
		paths := []string{filepath.Join(sysroot, cb.ExternalFile(id))}
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
	fmt.Fprintf(stdout, "%s removes mirrormark from every profile when it next starts. Your bookmarks stay in %s.\n", cb.Name, cb.Name)
	if len(users) > 0 {
		fmt.Fprintf(stdout, "Kept the CRX and the host manifest, which %s still uses.\n", strings.Join(users, " and "))
	}
	return finishUninstall(p, cb.Config, cb.Name, *purge, len(users) > 0, stdout, stderr)
```

The Gecko branch calls `finishUninstall(p, fb.Config, fb.Name, *purge, false, stdout, stderr)`. `finishUninstall` gains `keyInUse bool` after `purge` (comment: "keyInUse says another Chromium browser still has the extension installed; the key that fixes its id stays then"), and:

```go
	_, chromium := chrome.Lookup(browser)
	again, kept := "mirrormark uninstall --purge", "the databases, backups and key"
	if browser != chrome.Chrome.Config {
		again = "mirrormark uninstall --browser " + browser + " --purge"
	}
	if !chromium {
		kept = "the databases and backups" // only the Chromium browsers have a key
	}
```

in the database loop:

```go
		if d.Profile.Browser != browser {
			others = append(others, id)
			if _, ok := chrome.Lookup(d.Profile.Browser); ok {
				keyInUse = true
			}
			continue
		}
```

and `if browser == "chrome" { files = append(files, …extension.pem) }` becomes

```go
	// The key fixes the id every Chromium browser here installed the
	// extension under.
	if chromium && !keyInUse {
		files = append(files, filepath.Join(p.Data, "extension.pem"))
	}
```

Flag help and the two `--browser` errors: `"chrome, brave or firefox"`; `--user-data-dir`'s help: `"the browser's data directory (default ~/.config/google-chrome for Chrome, ~/.config/BraveSoftware/Brave-Browser for Brave), or Firefox's profiles directory"`. In `main.go`'s usage: `install     Install into Google Chrome, Brave or Firefox Developer Edition and bind its profiles`, `[--browser chrome|brave|firefox]` in both commands, `uninstall   Remove from a browser; keeps your files unless --purge`.

- [ ] **Step 5: README.** Status line: "It works with Google Chrome, Brave and Firefox Developer Edition on Linux." In "Upgrade, uninstall, unpack", "unless a Firefox profile's database is left: Firefox's profiles still sync through them, and Firefox's `uninstall` needs those databases to find its profiles" → "unless another browser's database is left: its profiles still sync through them, and its `uninstall` needs those databases to find its profiles". After the Chrome section, add:

```markdown
## Install (Brave on Linux)

Brave installs as Chrome does (see [Install (Google Chrome on
Linux)](#install-google-chrome-on-linux)), with `--browser brave`:

1. Run `mirrormark install --browser brave`. It binds every profile in
   `~/.config/BraveSoftware/Brave-Browser` (or `$XDG_CONFIG_HOME/…`);
   `--profile` and `--user-data-dir` work as for Chrome. With `sudo` it
   writes `/opt/brave.com/brave/extensions/<extension id>.json` and the same
   host manifest Chrome uses, so Chrome and Brave share one extension id.
2. Restart Brave (`pgrep -x brave` shows none left), then run
   `mirrormark install --browser brave` again to bind the profiles. Profile
   ids start with `brave-`, labels with `brave/`.

Brave's extensions page may show a Safety Check card saying it can't verify
where mirrormark comes from; mirrormark stays on. Brave reopens your last session
when it starts, so a bind tab left from a failed try may open again and bind
the profile again to the same database; that is harmless.

After upgrading mirrormark, run `mirrormark install --browser brave` as well
as `mirrormark install` if you use both. `mirrormark uninstall --browser brave` removes Brave's file. It keeps the
CRX and the host manifest while Chrome still has mirrormark installed, and
`--purge` keeps the key while Chrome has it installed or a Chrome profile's
database is left; the same goes for Chrome while Brave has it.

Only Brave stable from Brave's own package is supported. Brave Beta and
Nightly, and Brave from Flatpak or Snap, are untested and not supported.
```

- [ ] **Step 6: Run** `bin/dev run make` → PASS; `bin/dev run make check-e2e RUN='TestInstallAndBind|TestBindStartsChrome'` → PASS.

- [ ] **Step 7: Commit** subject `feat(rfc-0001): install into and uninstall from Brave [new feature]`.

---

### Task 5: Install into and uninstall from LibreWolf

**Files:**
- Modify: `internal/firefox/profiles.go`, `internal/firefox/files.go`, `cmd/mirrormark/install.go`, `cmd/mirrormark/main.go`, `README.md`
- Test: `internal/firefox/firefox_test.go`, `cmd/mirrormark/install_test.go`

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces: `firefox.LibreWolf`.

- [ ] **Step 1: Write the failing tests**

`internal/firefox/firefox_test.go`:

```go
func TestLibreWolfDefaultRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if r, _ := LibreWolf.DefaultRoot(); r != filepath.Join(home, ".librewolf") {
		t.Fatalf("without profiles.ini: %s", r)
	}
	xdg := filepath.Join(home, "xdg", "librewolf", "librewolf")
	os.MkdirAll(xdg, 0o700)
	os.WriteFile(filepath.Join(xdg, "profiles.ini"), nil, 0o644)
	if r, _ := LibreWolf.DefaultRoot(); r != xdg {
		t.Fatalf("with profiles.ini: %s", r)
	}
}

// LibreWolf's channel says release, so its application.ini tells it apart.
func TestCheckTellsLibreWolfFromFirefox(t *testing.T) {
	install := func(name, channel string) string {
		d := t.TempDir()
		os.MkdirAll(filepath.Join(d, "defaults", "pref"), 0o755)
		os.WriteFile(filepath.Join(d, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "`+channel+`");`+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "application.ini"), []byte("[App]\nVendor=x\nName="+name+"\n"), 0o644)
		return d
	}
	profile := func(platformDir string) string {
		d := t.TempDir()
		os.WriteFile(filepath.Join(d, "compatibility.ini"), []byte("[Compatibility]\nLastPlatformDir="+platformDir+"\n"), 0o644)
		return d
	}
	lw, release := profile(install("LibreWolf", "release")), profile(install("Firefox", "release"))
	if bin, err := LibreWolf.Check(lw); err != nil || filepath.Base(bin) != "librewolf" {
		t.Fatalf("LibreWolf: %s %v", bin, err)
	}
	if _, err := LibreWolf.Check(release); err == nil || !strings.Contains(err.Error(), "not LibreWolf") {
		t.Fatalf("release Firefox as LibreWolf: %v", err)
	}
	if _, err := Firefox.Check(lw); err == nil || !strings.Contains(err.Error(), "--browser librewolf") {
		t.Fatalf("LibreWolf as Firefox: %v", err)
	}
}
```

`cmd/mirrormark/install_test.go`: replace `firefoxData`'s body with a general helper:

```go
// geckoData makes a profiles root with two profiles, alpha and beta, last
// run by a fake browser in a directory of its own: its binary is called
// bin, its application.ini names app and its channel-prefs.js says
// channel. The fake logs its calls to calls and, given a bind URL, touches
// the lock file of the database it names.
func geckoData(t *testing.T, p db.Paths, root, bin, app, channel string) (calls string) {
	ff := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(ff, "defaults", "pref"), 0o755))
	must(t, os.WriteFile(filepath.Join(ff, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "`+channel+`");`), 0o644))
	must(t, os.WriteFile(filepath.Join(ff, "application.ini"), []byte("[App]\nName="+app+"\n"), 0o644))
	calls = filepath.Join(ff, "calls")
	must(t, os.WriteFile(filepath.Join(ff, bin), []byte("#!/bin/sh\nfor a; do url=$a; done\necho \"$@\" >> "+calls+"\n"+
		"case \"$url\" in *bind.html*) touch -d \"@$(date +%s.%N)\" \""+p.ProfilesDir()+"/${url##*#}.lock\";; esac\n"), 0o755))
	ini := "[General]\nVersion=2\n"
	for i, n := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "x"+n+"."+n)
		must(t, os.MkdirAll(dir, 0o700))
		must(t, os.WriteFile(filepath.Join(dir, "prefs.js"), nil, 0o600))
		must(t, os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte("[Compatibility]\nLastPlatformDir="+ff+"\n"), 0o644))
		ini += fmt.Sprintf("[Profile%d]\nName=%s\nIsRelative=1\nPath=x%s.%s\n", i, n, n, n)
	}
	must(t, os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644))
	old := chrome.BindWait
	chrome.BindWait = 500 * time.Millisecond
	t.Cleanup(func() { chrome.BindWait = old })
	return calls
}

// firefoxData makes Developer Edition's (or, with another channel, another
// Firefox's) profiles root with alpha and beta.
func firefoxData(t *testing.T, p db.Paths, channel string) (root, calls string) {
	root = filepath.Join(os.Getenv("HOME"), ".config", "mozilla", "firefox")
	return root, geckoData(t, p, root, "firefox", "Firefox", channel)
}

// librewolfData makes LibreWolf's profiles root with alpha and beta.
func librewolfData(t *testing.T, p db.Paths) (root, calls string) {
	root = filepath.Join(os.Getenv("HOME"), ".config", "librewolf", "librewolf")
	return root, geckoData(t, p, root, "librewolf", "LibreWolf", "release")
}

func TestInstallIntoLibreWolfThenBind(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := librewolfData(t, p)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "librewolf"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Restart LibreWolf") || !strings.Contains(out.String(), "unsigned") {
		t.Fatalf("output:\n%s", out.String())
	}
	for _, n := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "x"+n+"."+n)
		mustExist(t, "install", firefox.XPIPath(dir))
		if user, _ := os.ReadFile(filepath.Join(dir, "user.js")); string(user) != strings.Join(firefox.Prefs, "\n")+"\n" {
			t.Fatalf("user.js %q", user)
		}
		d, err := db.LoadDB(p.ProfileFile("librewolf-" + n))
		if err != nil || d.Profile.Path != dir || d.Profile.Browser != "librewolf" || d.Profile.Label != "librewolf/"+n {
			t.Fatalf("%s: %+v %v", n, d, err)
		}
	}
	host, _ := firefox.HostManifestPath()
	mustExist(t, "install", host)

	for _, n := range []string{"alpha", "beta"} {
		must(t, os.WriteFile(filepath.Join(root, "x"+n+"."+n, "prefs.js"), []byte(`user_pref("extensions.webextensions.uuids", "{\"mirrormark@chriscz.github.io\":\"5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4`+n[:1]+`\"}");`+"\n"), 0o600))
	}
	out.Reset()
	if code := run([]string{"install", "--browser", "librewolf", "--profile", "beta"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "bound: beta -> librewolf-beta\n") {
		t.Fatalf("output:\n%s", out.String())
	}
	if c, _ := os.ReadFile(calls); !bytes.Contains(c, []byte("about:blank moz-extension://5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b/bind.html#librewolf-beta")) {
		t.Fatalf("calls:\n%s", c)
	}
}

// Each Gecko browser takes only its own profiles, and writes nothing when
// it refuses.
func TestEachGeckoBrowserTakesOnlyItsOwnProfiles(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	lwRoot, _ := librewolfData(t, p)
	ffRoot, _ := firefoxData(t, p, "aurora")
	for _, c := range []struct{ browser, root, want string }{
		{"librewolf", ffRoot, "not LibreWolf"},
		{"firefox", lwRoot, "--browser librewolf"},
	} {
		var out, errb bytes.Buffer
		if code := run([]string{"install", "--browser", c.browser, "--user-data-dir", c.root}, strings.NewReader(""), &out, &errb); code != 1 {
			t.Fatalf("%s: exit %d: %s%s", c.browser, code, out.String(), errb.String())
		}
		if !strings.Contains(errb.String(), c.want) {
			t.Fatalf("%s: stderr %q", c.browser, errb.String())
		}
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 0 {
		t.Fatalf("made databases %v", dbs)
	}
}

// Developer Edition and LibreWolf read one host manifest: it goes with the
// last browser whose profiles still hold the add-on.
func TestUninstallingOneGeckoBrowserKeepsTheSharedHostManifest(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	lwRoot, _ := librewolfData(t, p)
	ffRoot, _ := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	for _, b := range []string{"firefox", "librewolf"} {
		if code := run([]string{"install", "--browser", b}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("install %s: exit %d: %s%s", b, code, out.String(), errb.String())
		}
	}
	host, _ := firefox.HostManifestPath()
	out.Reset()
	if code := run([]string{"uninstall", "--browser", "librewolf"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "librewolf uninstall", firefox.XPIPath(filepath.Join(lwRoot, "xalpha.alpha")))
	mustExist(t, "librewolf uninstall", host, firefox.XPIPath(filepath.Join(ffRoot, "xalpha.alpha")))
	if user, _ := os.ReadFile(filepath.Join(ffRoot, "xalpha.alpha", "user.js")); !bytes.Contains(user, []byte(firefox.Prefs[0])) {
		t.Fatalf("Developer Edition's user.js was reset: %q", user)
	}
	if !strings.Contains(out.String(), "Firefox Developer Edition's profiles still use") {
		t.Fatalf("output:\n%s", out.String())
	}
	if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	mustBeGone(t, "firefox uninstall", host)
}
```

- [ ] **Step 2: Run to see them fail**

Run: `bin/dev run go test ./internal/firefox/ ./cmd/mirrormark/`
Expected: FAIL: `undefined: LibreWolf`.

- [ ] **Step 3: LibreWolf's description, in `internal/firefox/profiles.go`**

```go
// LibreWolf is built without MOZ_REQUIRE_SIGNING, so it honours
// xpinstall.signatures.required like Developer Edition. Its channel says
// release, so its application.ini tells it from release Firefox.
var LibreWolf = Browser{Config: "librewolf", Name: "LibreWolf", XDGRoot: "librewolf/librewolf",
	HomeRoot: ".librewolf", Bin: "librewolf", AppName: "LibreWolf"}

// Browsers are the Gecko browsers install knows.
var Browsers = []Browser{Firefox, LibreWolf}
```

- [ ] **Step 4: Recognise it, in `Check` (`internal/firefox/files.go`)**

The channel refusal becomes `"its Firefox is on the %s channel, which installs only add-ons Mozilla signed; mirrormark works only in Firefox Developer Edition, and in LibreWolf with --browser librewolf"`. Before `return filepath.Join(dir, b.Bin), nil` add (and `"cmp"` to the imports):

```go
	if b.AppName != "" {
		ini, err := os.ReadFile(filepath.Join(dir, "application.ini"))
		if err != nil {
			return "", fmt.Errorf("cannot tell which browser is in %s: %w", dir, err)
		}
		var name string
		for _, s := range iniSections(string(ini)) {
			if s["[]"] == "App" {
				name = s["Name"]
			}
		}
		if name != b.AppName {
			return "", fmt.Errorf("it was last run by %s from %s, not %s", cmp.Or(name, "a browser that does not name itself"), dir, b.AppName)
		}
	}
```

- [ ] **Step 5: Shared host manifest, in `uninstallFirefox`**

```go
	shared := "" // another Gecko browser whose profiles still hold the add-on
	for _, d := range dbs {
		if d.Profile.Browser != b.Config {
			if o, ok := firefox.Lookup(d.Profile.Browser); ok {
				if _, err := os.Stat(firefox.XPIPath(d.Profile.Path)); err == nil {
					shared = o.Name
				}
			}
			continue
		}
		// … as before
	}
	found := len(gone) > 0
	// Every Gecko browser reads the one host manifest; it goes with the
	// last whose profiles still hold the add-on.
	host, err := firefox.HostManifestPath()
	if err != nil {
		return err
	}
	if data, err := os.ReadFile(host); err == nil && shared == "" && bytes.Contains(data, []byte(`"`+extension.GeckoID+`"`)) {
		gone = append(gone, host)
	}
```

and after the closing message:

```go
	if shared != "" {
		fmt.Fprintf(stdout, "Kept the host manifest %s, which %s's profiles still use.\n", host, shared)
	}
```

Flag help and the `--browser` errors: `"chrome, brave, firefox or librewolf"`; `--user-data-dir` help ends "…, or the profiles directory of Firefox Developer Edition or LibreWolf". `main.go` usage: `install     Install into Google Chrome, Brave, Firefox Developer Edition or LibreWolf and bind its profiles`, `[--browser chrome|brave|firefox|librewolf]` in both commands.

- [ ] **Step 6: README.** Status: "Google Chrome, Brave, Firefox Developer Edition and LibreWolf on Linux." After "Upgrade and uninstall (Firefox)", add:

```markdown
## Install (LibreWolf on Linux)

LibreWolf installs as Firefox Developer Edition does (see [Install (Firefox
Developer Edition on Linux)](#install-firefox-developer-edition-on-linux)),
with `--browser librewolf`, and the same three prefs in each profile's
`user.js`; read "Before you start" there first.

1. Run `mirrormark install --browser librewolf`. It takes the profiles in
   `$XDG_CONFIG_HOME/librewolf/librewolf` (`~/.config/librewolf/librewolf`)
   when that holds a `profiles.ini`, else in `~/.librewolf`. LibreWolf makes
   a second profile, `default`, that it never uses; `install` skips it.
2. Restart LibreWolf in those profiles if it is running, then run
   `mirrormark install --browser librewolf` again to bind them. Profile ids
   start with `librewolf-`, labels with `librewolf/`.

The host manifest goes in `~/.mozilla/native-messaging-hosts/`, which
LibreWolf reads after `~/.librewolf/native-messaging-hosts/`; a mirrormark
manifest of your own there takes precedence. `mirrormark uninstall --browser
librewolf` works as for Firefox, and keeps the host manifest while a
Developer Edition profile still has mirrormark, and the other way round.

Only LibreWolf's official Linux tarball has been tried. LibreWolf from its
Debian/Ubuntu repository, Flatpak, AppImage or the AUR is untested. If you
set LibreWolf to clear more than its defaults when it closes, the profile may
lose its binding at each restart; the popup then says it is not bound.
```

- [ ] **Step 7: Run** `bin/dev run make` → PASS; `bin/dev run make check-e2e RUN='TestFirefox'` → PASS.

- [ ] **Step 8: Commit** subject `feat(rfc-0001): install into and uninstall from LibreWolf [new feature]`.

---

### Task 6: Brave and LibreWolf in the dev image

**Files:**
- Modify: `Dockerfile`, `Makefile`, `README.md` (Development)

- [ ] **Step 1: Dockerfile**, after the Firefox Developer Edition block:

```dockerfile
# Brave stable for the Brave end-to-end tests, from Brave's own apt pool.
# Brave removes old packages from the pool; bump version and checksum
# together (both are in the pool's dists/stable/main/binary-amd64/Packages).
ARG BRAVE_VERSION=1.97.57
ARG BRAVE_SHA256=d79919a82aa869cf5121b641afa79958730c4bdaf36a0c51ae0f9c32f9c16bb9
RUN curl -fsSLo /tmp/brave.deb "https://brave-browser-apt-release.s3.brave.com/pool/main/b/brave-browser/brave-browser_${BRAVE_VERSION}_amd64.deb" \
 && echo "${BRAVE_SHA256}  /tmp/brave.deb" | sha256sum -c - \
 && apt-get update \
 && apt-get install -y --no-install-recommends /tmp/brave.deb \
 && rm -rf /var/lib/apt/lists/* /tmp/brave.deb

# LibreWolf for the LibreWolf end-to-end tests: the official tarball, using
# the libraries installed for Firefox above. Bump version and checksum (the
# release's .sha256sum file) together.
ARG LIBREWOLF_VERSION=158.0-1
ARG LIBREWOLF_SHA256=34bb7296feec0c6823abdfaf4fd50e47e9eada9cb4a5ed16e111a951dc9eb16c
RUN curl -fsSLo /tmp/lw.tar.xz "https://dl.librewolf.net/librewolf/${LIBREWOLF_VERSION}/librewolf-${LIBREWOLF_VERSION}-linux-x86_64-package.tar.xz" \
 && echo "${LIBREWOLF_SHA256}  /tmp/lw.tar.xz" | sha256sum -c - \
 && mkdir -p /opt/librewolf \
 && tar -C /opt/librewolf --strip-components=1 -xJf /tmp/lw.tar.xz \
 && rm /tmp/lw.tar.xz
```

and the system-directories block becomes:

```dockerfile
RUN mkdir -p /usr/share/google-chrome/extensions /opt/brave.com/brave/extensions /etc/opt/chrome/native-messaging-hosts \
 && chmod 1777 /usr/share/google-chrome/extensions /opt/brave.com/brave/extensions /etc/opt/chrome/native-messaging-hosts
```

- [ ] **Step 2: Build and check**

Run: `bin/dev image build`
Expected: success. If `apt-get install /tmp/brave.deb` cannot resolve a dependency, add Brave's repository before it as the spike did (`brave-spike/Dockerfile`: the keyring from `https://brave-browser-apt-release.s3.brave.com/brave-browser-archive-keyring.gpg` and `deb [signed-by=…] https://brave-browser-apt-release.s3.brave.com/ stable main`).
Run: `bin/dev run brave-browser --version` → `Brave Browser 155.1.97.57`.
Run: `bin/dev run sh -c '/opt/librewolf/librewolf --version; grep ^Name= /opt/librewolf/application.ini'` → a version line with `158.0`, and `Name=LibreWolf`.

- [ ] **Step 3: Makefile.** The `check-e2e` comment: "needs Chrome, Brave, Firefox Developer Edition, LibreWolf, Xvfb and D-Bus from the dev image …"; the rest unchanged.

- [ ] **Step 4: README Development.** "The end-to-end tests run real browsers from the dev image under Xvfb: Google Chrome and Brave, driven over the DevTools protocol, and Firefox Developer Edition and LibreWolf, driven over WebDriver BiDi in a D-Bus session, …"; "they share the Chromium browsers' system directories".

- [ ] **Step 5: Run** `bin/dev run make && bin/dev run make check-e2e` → PASS (no new tests yet).

- [ ] **Step 6: Commit** subject `build(rfc-0001): add Brave and LibreWolf to the dev image`.

---

### Task 7: Install and bind Brave and LibreWolf end to end

**Files:**
- Modify: `e2e/env.go`, `e2e/browser.go`, `e2e/bidi.go`, `e2e/install_test.go`, `README.md` (Development)

**Interfaces:**
- Consumes: Tasks 4–6, and P1–P2 ([`plan-profile-selection.md`](plan-profile-selection.md)): `--profile` on every install, `e.ready`, `chrome.ReadState`, `firefox.ReadState`.
- Produces: kinds `chrome`, `brave`, `firefox`, `librewolf` for `setupPair`; `bins`, `names`, `gecko`; `launch(t, name, kind, udd, env)`, `launchFirefox(t, name, kind, dir, port, env)`.

- [ ] **Step 1: The harness knows four kinds.** In `e2e/env.go`, add:

```go
// bins start each kind of browser in the dev image.
var bins = map[string]string{
	"chrome":    "google-chrome",
	"brave":     "brave-browser",
	"firefox":   "/opt/firefox-devedition/firefox",
	"librewolf": "/opt/librewolf/librewolf",
}

// names are how install's restart line names each kind.
var names = map[string]string{"chrome": "Chrome", "brave": "Brave", "firefox": "Firefox", "librewolf": "LibreWolf"}

// gecko reports whether kind is a Firefox-family browser.
func gecko(kind string) bool { return kind == "firefox" || kind == "librewolf" }
```

The `kinds` field comment: "browser name to its kind, a key of bins". In `open`, `if b.Kind == "firefox"` → `if gecko(b.Kind)`. `setupPair`'s comment: "…in browser kind ka and b in kb (keys of bins)…". `firstInstall` becomes:

```go
// firstInstall prepares both profiles and runs install's first pass, which
// writes the files and asks for a restart: once per Chromium kind, since
// its files reach every data directory, and once per Gecko profile, since
// each holds its own add-on and prefs.
func (e *Env) firstInstall(t *testing.T) {
	t.Helper()
	e.prepare(t, "a")
	e.prepare(t, "b")
	done := map[string]bool{}
	for _, name := range []string{"a", "b"} {
		k := e.kind(name)
		if !gecko(k) {
			if done[k] {
				continue
			}
			done[k] = true
			// Registered before install runs, so an install that fails
			// part-way leaves nothing behind either, and run before
			// t.TempDir's cleanup: later tests must not see this test's
			// extension in the shared system directories.
			t.Cleanup(func() { e.mm(t, "uninstall", "--browser", k) })
		}
		out := e.mm(t, e.installArgs(name)...)
		if !strings.Contains(out, "Restart "+names[k]) {
			t.Fatalf("first install did not ask for a restart:\n%s", out)
		}
		if !gecko(k) {
			m := extIDRe.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("first install did not print the extension id:\n%s", out)
			}
			if e.ExtID != "" && m[1] != e.ExtID {
				t.Fatalf("%s got extension id %s, the other browser %s", k, m[1], e.ExtID)
			}
			e.ExtID = m[1]
		}
	}
}
```

In `prepare`: `if gecko(e.kind(name))`, `exec.Command(bins[e.kind(name)], "--headless", "-CreateProfile", name+" "+dir)`, and "create %s profile %s" with the kind. `udd`:

```go
func (e *Env) udd(name string) string {
	k := e.kind(name)
	if gecko(k) {
		return filepath.Join(e.Home, k, name)
	}
	return filepath.Join(e.Home, k+"-"+name)
}
```

`launch`:

```go
func (e *Env) launch(name string) *Browser {
	if k := e.kind(name); gecko(k) {
		return launchFirefox(e.t, name, k, e.udd(name), map[string]int{"a": 9301, "b": 9302}[name], e.env)
	}
	return launch(e.t, name, e.kind(name), e.udd(name), e.env)
}
```

`installArgs`:

```go
func (e *Env) installArgs(name string) []string {
	if k := e.kind(name); gecko(k) {
		return []string{"install", "--browser", k, "--profile", name}
	}
	return []string{"install", "--browser", e.kind(name), "--user-data-dir", e.udd(name), "--profile", "Default"}
}
```

`bind`: `if gecko(e.kind(name)) { profile = name }` (a Gecko profile's ID is its directory's base name, `name`, because `prepare` creates it at `<Home>/<kind>/<name>`). `ready`, from P1, reads the profile's files by family:

```go
	eventually(e.t, 40*time.Second, name+"'s files to show mirrormark ready", func() bool {
		if gecko(e.kind(name)) {
			s, _, err := firefox.ReadState(e.udd(name), want)
			return err == nil && s.State == chrome.Ready
		}
		s, err := chrome.ReadState(filepath.Join(e.udd(name), "Default"), e.ExtID, want, true)
		return err == nil && s.State == chrome.Ready
	})
```

In `e2e/browser.go`: `Kind string // a key of bins`; `func launch(t *testing.T, name, kind, udd string, env []string) *Browser` with `exec.Command(bins[kind], "--user-data-dir="+udd, …)` (flags unchanged; the comment on `--no-sandbox` now says "Chromium's sandbox") and `Kind: kind`. In `e2e/bidi.go`: delete `firefoxBin`; `func launchFirefox(t *testing.T, name, kind, dir string, port int, env []string) *Browser` with `exec.Command(bins[kind], "--profile", dir, …)` and `Kind: kind`.

- [ ] **Step 2: Check the refactor** — `bin/dev run make check-e2e RUN='TestInstallAndBind|TestFirefoxInstallAndBind'` → PASS.

- [ ] **Step 3: Write the tests,** in `e2e/install_test.go` (add `"github.com/chriscz/mirrormark/internal/firefox"` to the imports):

```go
// TestBraveInstallAndBind: Brave installs the extension under Chrome's id
// and binds as Chrome does, and uninstalling Brave leaves the host manifest
// Chrome uses in place.
func TestBraveInstallAndBind(t *testing.T) {
	e := setupPair(t, "chrome", "brave")
	if !strings.HasPrefix(e.DBB, "brave-") {
		t.Fatalf("Brave bound to %s", e.DBB)
	}
	self := eval[struct {
		ID          string `json:"id"`
		InstallType string `json:"installType"`
		Enabled     bool   `json:"enabled"`
	}](t, e.B, `chrome.management.getSelf()`)
	if self.ID != e.ExtID || self.InstallType != "sideload" || !self.Enabled {
		t.Errorf("Brave: extension %+v, want id %s, sideload, enabled", self, e.ExtID)
	}
	d, err := db.LoadDB(e.Paths.ProfileFile(e.DBB))
	if err != nil || d.Profile.Browser != "brave" || d.Profile.Path != filepath.Join(e.B.UDD, "Default") {
		t.Errorf("%s database %+v, %v", e.DBB, d, err)
	}
	roundDone(t, e.A, "Chrome's round after binding")
	roundDone(t, e.B, "Brave's round after binding")

	e.B.Close()
	e.mm(t, "uninstall", "--browser", "brave")
	if _, err := os.Stat(chrome.Brave.ExternalFile(e.ExtID)); !os.IsNotExist(err) {
		t.Errorf("Brave's external file is still there: %v", err)
	}
	if _, err := os.Stat(chrome.HostManifestPath()); err != nil {
		t.Fatalf("the host manifest went with Brave: %v", err)
	}
}

// TestLibreWolfInstallAndBind: LibreWolf takes Developer Edition's route,
// and uninstalling it resets its prefs and leaves the host manifest
// Developer Edition uses in place.
func TestLibreWolfInstallAndBind(t *testing.T) {
	e := setupPair(t, "librewolf", "firefox")
	if e.DBA != "librewolf-a" || e.DBB != "firefox-b" {
		t.Fatalf("bound to %s and %s", e.DBA, e.DBB)
	}
	if got := eval[string](t, e.A, `chrome.runtime.id`); got != extension.GeckoID {
		t.Errorf("LibreWolf: extension id %q", got)
	}
	d, err := db.LoadDB(e.Paths.ProfileFile(e.DBA))
	if err != nil || d.Profile.Browser != "librewolf" || d.Profile.Path != e.A.UDD {
		t.Errorf("%s database %+v, %v", e.DBA, d, err)
	}
	roundDone(t, e.A, "LibreWolf's round after binding")
	roundDone(t, e.B, "Developer Edition's round after binding")

	e.A.Close()
	e.mm(t, "uninstall", "--browser", "librewolf")
	if _, err := os.Stat(firefox.XPIPath(e.udd("a"))); !os.IsNotExist(err) {
		t.Errorf("the XPI is still in LibreWolf's profile: %v", err)
	}
	if user, _ := os.ReadFile(filepath.Join(e.udd("a"), "user.js")); !bytes.Contains(user, []byte(`user_pref("xpinstall.signatures.required", true);`)) {
		t.Errorf("LibreWolf's user.js not put back:\n%s", user)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".mozilla", "native-messaging-hosts", extension.HostName+".json")); err != nil {
		t.Fatalf("the host manifest went with LibreWolf: %v", err)
	}
}
```

- [ ] **Step 4: Run.** `bin/dev run make check-e2e RUN='TestBraveInstallAndBind|TestLibreWolfInstallAndBind'` → PASS.
  - If LibreWolf's install says it found no profile `a`, check where `-CreateProfile` registered it: `bin/dev run sh -c 'export HOME=$(mktemp -d); /opt/librewolf/librewolf --headless -CreateProfile "a $HOME/p"; find $HOME -name profiles.ini'`. If it is neither `~/.config/librewolf/librewolf` nor `~/.librewolf`, stop and report: Section 9's profile root is then wrong.
  - If a LibreWolf profile's first start does not show the add-on ready within `e.ready`'s 40 s, or a ready profile does not report in within `chrome.BindWait` (30 s, one open), stop and report the time it took. Do not raise the waits silently, and do not add a retry.
  - If Brave's `Preferences` does not show the extension within `e.ready`'s 40 s of the profile's start, record how long it took (the profile-state spike measured 10.3 s), and stop.
  - If `chrome.management.getSelf()` gives Brave another `installType`, record what it gives and assert that instead, with a comment.

- [ ] **Step 5: README Development:** "Besides installing and binding in each of the four browsers, …". Run `bin/dev run make && bin/dev run make check-e2e` → PASS.

- [ ] **Step 6: Commit** subject `test(rfc-0001): install and bind Brave and LibreWolf end to end`.

---

### Task 8: Run the sync tests between Brave and LibreWolf

**Files:**
- Modify: `e2e/sync_test.go`, `README.md` (Development)

- [ ] **Step 1: The pair.**

```go
// pairs are the browsers of profiles a and b each sync test runs with. One
// cross-engine pair covers Brave and LibreWolf; the others add little
// beyond what their families' pairs show, and each pair adds minutes.
var pairs = [][2]string{{"chrome", "chrome"}, {"firefox", "firefox"}, {"firefox", "chrome"}, {"brave", "librewolf"}}
```

- [ ] **Step 2: Run.** `bin/dev run make check-e2e RUN='/brave-librewolf'` → PASS, each latency under 3 s logged. The pair's setup binds through P1–P2's flow (`--profile Default` for Brave, `--profile a`/`b` for LibreWolf, `e.ready` before each bind); nothing in this task changes for it, but setup now takes up to about 20 s longer while Brave writes `Preferences`. Then the full `bin/dev run make check-e2e` → PASS; the suite grows by about a third and stays inside the 60 m timeout.

- [ ] **Step 3: README Development:** "The sync tests run four times: between two Chromes, between two Firefoxes, between a Firefox and a Chrome, and between Brave and LibreWolf, as subtests named `chrome-chrome`, `firefox-firefox`, `firefox-chrome` and `brave-librewolf` …".

- [ ] **Step 4: Commit** subject `test(rfc-0001): run the sync tests between Brave and LibreWolf`.

## After the last task

- In a `docs(rfc-0001)` commit, update Section 9 and the History if anything was built differently from the RFC, and move what Tasks 6–7 checked out of the spikes' "Not verified" lists (the real host in both browsers, `-CreateProfile`, Brave's `.deb` without the repository).
- Add to `TASKS.md` under "Manual checks still to do": run the README's Brave steps on a desktop with Brave from Brave's repository and real `sudo`, beside Chrome, including an upgrade and `uninstall --browser brave` with Chrome still installed; run the README's LibreWolf steps on a desktop with LibreWolf from the tarball (and, if at hand, from LibreWolf's Debian repository), with LibreWolf open during the first `install`. Phase 6 is done when these runs are recorded.

## Risks

- **Brave in the dev image.** The spike ran Brave with `--no-sandbox --no-first-run` under Xvfb but not with the harness's `--remote-debugging-pipe` and `--disable-dev-shm-usage` together; Task 7 Step 2 shows it at once.
- **LibreWolf's slow first start** (over 10 s in the spike's container) must fit in `e.ready`'s 40 s and the single 30 s bind wait. Task 7 stops rather than widening them.
- **Test time** grows by one sync pair and two install tests, about a third. Use `RUN=` while developing.
- **Upgrades are per browser.** Install writes only the browser asked for, so a user who upgrades and runs `install` for Chrome but not Brave keeps Brave on the old build until `install --browser brave`; the README says to run both. Accepted in the RFC.
- **Accepted, untested:** Brave package upgrades and removal touching `/opt/brave.com/brave/extensions/`, Brave Sync, LibreWolf's other packagings and stricter sanitize settings. All stay in the RFC's risks.
