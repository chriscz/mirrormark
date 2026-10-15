# mirrormark explicit profiles and one-shot binding: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. When writing Go, use the `use-modern-go` skill.

**Goal:** `mirrormark install` binds a profile only once the profile's own files say mirrormark is installed and enabled there, opening the bind page exactly once. Without `--profile` it lists the profiles and their state and changes nothing. `--profile` takes an ID or a number.

**Architecture:** Each family's package learns to read the extension's state from a profile's files: `chrome.ReadState` reads `Preferences`, and `firefox.ReadState` reads `extensions.json` and `prefs.js`. Both return one shared `chrome.ProfileState`. `cmd/mirrormark/install.go` uses that state in two places. Before binding, it decides whether to open the page. In the listing, it fills the STATE column. `chrome.Retry` becomes `chrome.Open`, which makes one start and one wait, and Gecko's start-to-learn-the-UUID step goes. The transport behind "the extension reports in" stays as it is: `db.Connected` on the lock file's mtime. No host, extension, merge or file-format change.

**Tech Stack:** Go 1.27.2, standard library (`text/tabwriter` for the listing). Unit tests run with `bin/dev run make`, and e2e tests with `bin/dev run make check-e2e` (Chrome, Brave, Developer Edition and LibreWolf in the dev image from Brave plan Task 6).

**Spec:** [`../0001_local-bookmark-sync-host.md`](../0001_local-bookmark-sync-host.md), Section 10, with the edits to Sections 5, 6, 8 and 9 and Phase 7. Findings: [`profile-state-spike.md`](profile-state-spike.md).

**Why a separate plan file:** this change covers all four browsers, and its two tasks are a stack of their own. Other agents refer to [`plan-brave-librewolf.md`](plan-brave-librewolf.md) by its task numbers, and a separate file keeps those numbers as they are. That plan says where these tasks land: after its Task 6 and before its Tasks 7–8, which are revised there to use the new flow.

## Global Constraints

- Everything in the Chrome, Firefox and Brave/LibreWolf plans' Global Constraints still holds. In particular: `bin/dev run make` only; no `any` values or unchecked type assertions; no RFC numbers, section references or `docs/rfcs/` links in code, comments, tests or build files; README updated in the same commit; `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` on every commit.
- **Each task passes** `bin/dev run make` and `bin/dev run make check-e2e`.
- **Safety stays.** Before any write, the Gecko browser check covers every named profile. Databases come before any profile write. `install --browser brave` refuses when `brave-browser` is not on `PATH`, before anything else. Shared files stay while another browser uses them, and `--purge` works per browser. None of this code moves out of its place in `cmd/mirrormark/install.go`.
- **No retries.** Each ready profile gets one open (`chrome.Open`: one `exec`, one wait of `chrome.BindWait` = `30 * time.Second`). `chrome.Retry`, `bindTries` and `firefox.UUIDWait` are deleted, and nothing starts a Gecko browser just to learn the UUID.
- **What the extension reporting in means:** `db.Connected(p, id, since)`, the lock-file mtime the host touches on each `hello`. Name variables and comments after reporting in (`reported`), not after the lock file, so another transport can replace `db.Connected` in one place.
- **States**, `chrome.State` constants and their words (`ProfileState.String()`):

  | Constant | Word | Chromium (`<profile>/Preferences`) | Gecko |
  | --- | --- | --- | --- |
  | `NeverRun` | `never run` | no `Preferences` file | (from `Check`: `ErrNeverRun`) |
  | `NotInstalled` | `not installed` | no `extensions.settings.<id>` entry with a `path`, and `filePlaced` false | no XPI in the profile |
  | `NotInstalledYet` | `not installed yet` | the same with `filePlaced` true | XPI present, but no `addons[]` entry, entry not `active`, or no UUID in `prefs.js` |
  | `Removed` | `removed` | `<id>` in `extensions.external_uninstalls`, and no `extensions.settings.<id>` entry with a `path` | — |
  | `TurnedOff` | `turned off` | `disable_reasons` present and not `[]` or `0` | `userDisabled` true |
  | `Refused` | `refused` | — | `appDisabled` true |
  | `OldBuild` | `old build <version>` | `manifest.version` ≠ `extension.Version()` | `version` ≠ `extension.Version()` |
  | `Ready` | `ready` | otherwise | otherwise (`active`, UUID present) |

  Gecko checks them in this order: XPI, entry, `userDisabled`, `appDisabled`, version, `active` and UUID. Chromium checks them in this order: entry (with no entry that has a `path`, `external_uninstalls` tells `removed` from `not installed`), disable reasons, version. A copy loaded unpacked after a Remove has a `path` again, and Chromium may leave the id in `external_uninstalls`, so that list alone does not mean removed. The listing adds two words that come from outside `ReadState`. `bound` is `Ready` when a database's `profile.path` is this profile's directory. `not <Browser.Name>` is a Gecko profile that `Check` refuses for any reason other than `ErrNeverRun`.
- **Per-profile output lines on stdout, exactly** (`<ID>` is the profile ID from Task 2 on; in Task 1 Chromium prints `c.Dir` and Gecko `c.Name` as today):
  - `bound: <ID> -> <db>`
  - `not ready: <ID>: <advice>.`
  - `not bound: <ID>: mirrormark did not report in within 30s of opening the page; look at the tab <Name> opened, then run <again>.`
- **Advice texts, exactly** (`%s` filled in order; `<Name>` is `Browser.Name`, `<page>` is `chrome.Browser.ExtensionsPage`, `<again>` is the command to run again, below):
  - Chromium `NeverRun`: `<Name> has never opened this profile; open it in <Name> once, then run <again>`
  - Chromium `NotInstalled`, `NotInstalledYet`: `<Name> has not installed mirrormark in this profile yet. Quit <Name> completely (close every window), start it again and open this profile, wait half a minute, then run <again>`
  - Chromium `Removed`: `you removed mirrormark from this profile, and <Name> will not install it there again. Run mirrormark unpack, turn on Developer mode in <page>, choose Load unpacked and pick the folder it prints, then run <again>`
  - Chromium `TurnedOff`: `mirrormark is turned off in this profile. Turn it on in <page>, wait half a minute, then run <again>`
  - Chromium `OldBuild`: `this profile runs mirrormark <found>, not <want>. Quit <Name> completely (close every window) and start it again, then run <again>`
  - Gecko `NotInstalled`, `NotInstalledYet`: `<Name> has not installed mirrormark in this profile yet. Quit <Name> if it runs in this profile (close every window) and start it there again, then run <again>`
  - Gecko `TurnedOff`: `mirrormark is turned off in this profile. Turn it on in about:addons, then run <again>`
  - Gecko `Refused`: `<Name> refuses mirrormark in this profile because it is not signed. Check that the profile's user.js sets xpinstall.signatures.required to false, restart <Name> there, then run <again>`
  - Gecko `OldBuild`: `this profile runs mirrormark <found>, not <want>. Quit <Name> in this profile (close every window) and start it there again, then run <again>`
  - A `ReadState` error: `cannot read its state: <err>`.
- **`<again>`:** `chrome.Browser.InstallCommand()` (or `mirrormark install --browser <Config>` for Gecko), then ` --user-data-dir <dir>` if the user gave `--user-data-dir`, then ` --profile <ID>`. An ID that holds anything other than `[A-Za-z0-9._-]` is written in double quotes: `--profile "Profile 1"`.
- **`chrome.Browser.ExtensionsPage`:** Chrome `chrome://extensions`, Brave `brave://extensions`. This is the one field added to the descriptions that the Brave plan fixes exactly.
- **Exit codes:** 0 for the listing; 0 for a run that wrote the browser's files and printed the restart line; 1 if any named profile printed `not ready` or `not bound`; 2 for usage errors, as today.
- **Listing, exactly** (Task 2), on stdout, written through `text/tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)`:

  ```
  <Name> profiles in <dir>:
    #  ID          NAME      STATE          DATABASE
    0  Default     Personal  bound          brave-personal
    1  Profile 1   Work      ready          -
    2  Profile 2   Test      not installed  -
    3  Profile 10  Old       removed        brave-old
  Nothing was changed. Name the profiles to install into or bind with --profile ID or --profile #, for example:
    <again for the example profile>
  ```

  An empty name and a missing database print as `-`. The example profile is the first one whose state is not `bound`, or number 0 if every profile is bound. A listing writes no file: no key, extension, CRX, database or config. It makes no `sudo` call and no `exec` of the browser. If the key does not exist, every Chromium profile is `NotInstalled` (or `NeverRun`). If a database cannot be read, the listing prints `mirrormark: warning: <err>` on stderr and goes on.
- **IDs and numbers** (Task 2): `chrome.Profile.ID()` is `filepath.Base(p.Dir)`. `Profiles` sorts by `chrome.CompareIDs`, which compares runs of digits as numbers and everything else bytewise, with ties broken by `Dir`. Numbers count from 0 in that order. `--profile X` takes the profile whose ID is X. Failing that, an all-digit X is a number. Errors (stderr, exit 1, nothing written):
  - `no profile "<X>"; run <list> to list them`
  - `no profile number <X>; run <list> to list them`
  - `<n> profiles have the ID "<X>" (numbers <i>, <j>); name one by its number`

  `<list>` is `<again>` without the `--profile` part. Names no longer match.
- The never-run skip goes in Task 2: no `skipped:` line. A named never-run profile is refused as today, by the existing `refused` path.

## Verified before planning

From [`profile-state-spike.md`](profile-state-spike.md) (2026-10-15, Docker): the state fields and values in both families; that `Secure Preferences` holds no entry; that Chromium's writes lag by 4.5–17 s and Gecko's by at most about 1.6 s; that a running Chromium browser never reads a new external file; that a user's Remove is recorded in `external_uninstalls` and does not come back; that Gecko deletes the XPI on Remove; and the "blocked by Brave/Chrome" page for an extension that is not loaded.

**Not verified, and checked by the task named:** that a ready Chromium profile, cold, reports in within one 30 s wait with no retry (Task 1, the reworked `TestBindStartsChrome`), and the same for Gecko (Task 1, `TestFirefoxBindStartsFirefox`).

## File structure

```
internal/chrome/
  state.go        State, ProfileState, ReadState (Task 1)
  bind.go         BindWait 30 s, Open replaces Retry, Bind opens once (Task 1)
  profiles.go     ExtensionsPage field (Task 1); Profile.ID, CompareIDs,
                  Profiles sorts by them (Task 2)
  chrome_test.go
internal/firefox/
  state.go        ReadState (Task 1)
  bind.go         Bind takes the UUID, opens once; UUIDWait gone (Task 1)
  firefox_test.go
cmd/mirrormark/
  install.go      readiness before bind, advice texts, again() (Task 1);
                  listing, choose by ID or number, no never-run skip (Task 2)
  main.go         usage text (Task 2)
  install_test.go
e2e/
  env.go          ready() before each bind, mmExit (Task 1); --profile in
                  installArgs (Task 2)
  install_test.go reworked "starts the browser" tests (Task 1); second
                  install with --profile (Task 2)
README.md         Tasks 1, 2
```

## Interfaces between tasks

```go
// internal/chrome (Task 1)
type State int
const (NeverRun State = iota; NotInstalled; NotInstalledYet; Removed; TurnedOff; Refused; OldBuild; Ready)
type ProfileState struct {
	State   State
	Version string // the build found, for OldBuild
}
func (s ProfileState) String() string
func ReadState(profileDir, id, version string, filePlaced bool) (ProfileState, error)
var BindWait = 30 * time.Second
func Open(bin string, args []string, reported func(since time.Time) bool) (bool, error)
func (b Browser) Bind(bin, userDataDir, profileDir, extID, dbID string, reported func(since time.Time) bool) error
// Browser gains ExtensionsPage string. Removed: Retry, bindTries.

// internal/chrome (Task 2)
func (p Profile) ID() string
func CompareIDs(a, b string) int

// internal/firefox (Task 1)
func ReadState(profileDir, version string) (chrome.ProfileState, string, error) // state, UUID
func (b Browser) Bind(bin, profileDir, uuid, dbID string, reported func(since time.Time) bool) error
// Removed: UUIDWait.

// cmd/mirrormark (Task 1)
func again(base, udd, id string) string
func chromiumAdvice(b chrome.Browser, s chrome.ProfileState, want, again string) string
func geckoAdvice(b firefox.Browser, s chrome.ProfileState, want, again string) string

// cmd/mirrormark (Task 2)
func choose(all []chrome.Profile, picks []string, list string) ([]chrome.Profile, error)
func listChromium(p db.Paths, b chrome.Browser, udd, list string, profiles []chrome.Profile, stdout, stderr io.Writer) int
func listGecko(p db.Paths, b firefox.Browser, root, list string, profiles []chrome.Profile, stdout, stderr io.Writer) int

// e2e (Task 1)
func (e *Env) ready(name string)
func (e *Env) mmExit(t *testing.T, args ...string) (string, int)
```

## Tasks

| # | Commit subject |
| --- | --- |
| 1 | `feat(rfc-0001): bind each profile once, only where mirrormark is ready` |
| 2 | `feat(rfc-0001): list the profiles, and install only into those named [new feature]` |

Task 1 is a change in behaviour that the user asked for, with no new command surface: install still takes every profile without `--profile`, but now opens nothing in a profile that is not ready, and says why. Task 2 adds the listing and the selection by ID or number, and drops the default "every profile". Either prefix is safe. After Task 1 alone, install writes what it wrote before and opens less.

---

### Task 1: Bind each profile once, only where mirrormark is ready

**Files:**
- Create: `internal/chrome/state.go`, `internal/firefox/state.go`
- Modify: `internal/chrome/bind.go`, `internal/chrome/profiles.go` (ExtensionsPage), `internal/firefox/bind.go`, `cmd/mirrormark/install.go`, `internal/chrome/chrome_test.go`, `internal/firefox/firefox_test.go`, `cmd/mirrormark/install_test.go`, `e2e/env.go`, `e2e/install_test.go`, `README.md`

**Interfaces:**
- Consumes: `extension.Version()`, `extension.GeckoID`, `firefox.UUID`, `firefox.XPIPath`, `db.Connected`, `chrome.Browser.InstallCommand`.
- Produces: everything listed for Task 1 above.

- [ ] **Step 1: Failing tests for `chrome.ReadState`** (in `internal/chrome/chrome_test.go`). The fixtures are the spike's values, cut to the fields that are read:

```go
func TestReadStateFromPreferences(t *testing.T) {
	const id = "pjjjaeommfpeolloibnafkcmkindkagl"
	entry := func(extra string) string {
		return `{"extensions":{"settings":{"` + id + `":{"path":"` + id + `/0.1.0_0","location":2,"manifest":{"version":"0.1.0"}` + extra + `}}}}`
	}
	for _, c := range []struct {
		name, prefs string // prefs "" means no Preferences file
		placed      bool
		want        ProfileState
	}{
		{"never run", "", true, ProfileState{State: NeverRun}},
		{"no entry, no file", `{"extensions":{"settings":{}}}`, false, ProfileState{State: NotInstalled}},
		{"no entry yet", `{}`, true, ProfileState{State: NotInstalledYet}},
		{"enabled", entry(`,"disable_reasons":[]`), true, ProfileState{State: Ready}},
		{"enabled again", entry(``), true, ProfileState{State: Ready}},
		{"turned off", entry(`,"disable_reasons":[1]`), true, ProfileState{State: TurnedOff}},
		{"turned off, old format", entry(`,"disable_reasons":1`), true, ProfileState{State: TurnedOff}},
		{"removed", `{"extensions":{"settings":{"` + id + `":{}},"external_uninstalls":["` + id + `"]}}`, true, ProfileState{State: Removed}},
		{"old build", entry(``), true, ProfileState{State: OldBuild, Version: "0.1.0"}},
	} {
		dir := t.TempDir()
		if c.prefs != "" {
			os.WriteFile(filepath.Join(dir, "Preferences"), []byte(c.prefs), 0o600)
		}
		want := "0.1.0"
		if c.name == "old build" {
			want = "0.2.0"
		}
		got, err := ReadState(dir, id, want, c.placed)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestReadStateReportsABrokenPreferences(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Preferences"), []byte("{"), 0o600)
	if _, err := ReadState(dir, "pjjjaeommfpeolloibnafkcmkindkagl", "0.1.0", true); err == nil || !strings.Contains(err.Error(), "Preferences") {
		t.Fatalf("err %v", err)
	}
}

func TestProfileStateWords(t *testing.T) {
	for s, w := range map[ProfileState]string{
		{State: NeverRun}: "never run", {State: NotInstalled}: "not installed", {State: NotInstalledYet}: "not installed yet",
		{State: Removed}: "removed", {State: TurnedOff}: "turned off", {State: Refused}: "refused",
		{State: OldBuild, Version: "0.1.0"}: "old build 0.1.0", {State: Ready}: "ready",
	} {
		if s.String() != w {
			t.Errorf("%+v: %q, want %q", s, s.String(), w)
		}
	}
}
```

Run `bin/dev run go test ./internal/chrome/ -run 'ReadState|ProfileStateWords'`. It should FAIL, because `ReadState` is undefined.

- [ ] **Step 2: `internal/chrome/state.go`.**

```go
package chrome

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// State is what a profile's own files say about mirrormark there. install
// reads it before it opens anything in the profile: a bind page opened
// where the extension is not loaded only shows the browser's "blocked"
// error page.
type State int

const (
	NeverRun        State = iota // the browser has never opened the profile
	NotInstalled                 // nothing mirrormark wrote reaches the profile
	NotInstalledYet              // mirrormark's files are in place; the browser has not installed it here
	Removed                      // the user removed it, and the browser will not install it again
	TurnedOff                    // installed, but the user turned it off
	Refused                      // installed, but the browser refuses to run it
	OldBuild                     // installed, but not this build
	Ready                        // installed, enabled, this build
)

// ProfileState is a profile's State, with the build found for OldBuild.
type ProfileState struct {
	State   State
	Version string
}

func (s ProfileState) String() string {
	switch s.State {
	case NeverRun:
		return "never run"
	case NotInstalled:
		return "not installed"
	case NotInstalledYet:
		return "not installed yet"
	case Removed:
		return "removed"
	case TurnedOff:
		return "turned off"
	case Refused:
		return "refused"
	case OldBuild:
		return "old build " + s.Version
	}
	return "ready"
}

// ReadState reads extension id's state from a Chromium profile's
// Preferences. filePlaced says whether this browser's external extension
// file is in place, which tells "not installed" from "not installed yet".
// Chromium writes Preferences in batches, up to about 20 seconds after a
// change, so a running browser's file can be behind; a clean close writes
// it at once. The file is not a documented interface.
func ReadState(profileDir, id, version string, filePlaced bool) (ProfileState, error) {
	path := filepath.Join(profileDir, "Preferences")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ProfileState{State: NeverRun}, nil
	}
	if err != nil {
		return ProfileState{}, err
	}
	var prefs struct {
		Extensions struct {
			Settings map[string]struct {
				Path string `json:"path"`
				// A list of reasons since about Chrome 120, a bit mask
				// before; either way, anything but none means off.
				DisableReasons json.RawMessage `json:"disable_reasons"`
				Manifest       struct {
					Version string `json:"version"`
				} `json:"manifest"`
			} `json:"settings"`
			ExternalUninstalls []string `json:"external_uninstalls"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(data, &prefs); err != nil {
		return ProfileState{}, fmt.Errorf("%s: %w", path, err)
	}
	if slices.Contains(prefs.Extensions.ExternalUninstalls, id) {
		return ProfileState{State: Removed}, nil
	}
	e, ok := prefs.Extensions.Settings[id]
	switch r := bytes.TrimSpace(e.DisableReasons); {
	case !ok || e.Path == "":
		if filePlaced {
			return ProfileState{State: NotInstalledYet}, nil
		}
		return ProfileState{State: NotInstalled}, nil
	case len(r) > 0 && !bytes.Equal(r, []byte("[]")) && !bytes.Equal(r, []byte("0")):
		return ProfileState{State: TurnedOff}, nil
	case e.Manifest.Version != version:
		return ProfileState{State: OldBuild, Version: e.Manifest.Version}, nil
	}
	return ProfileState{State: Ready}, nil
}
```

Run the tests from Step 1. They should PASS.

- [ ] **Step 3: Failing tests for `firefox.ReadState`** (in `internal/firefox/firefox_test.go`):

```go
func TestReadStateFromExtensionsJSON(t *testing.T) {
	uuidPref := `user_pref("extensions.webextensions.uuids", "{\"mirrormark@chriscz.github.io\":\"` + testUUID + `\"}");` + "\n"
	addon := func(fields string) string {
		return `{"addons":[{"id":"other@x"},{"id":"mirrormark@chriscz.github.io","location":"app-profile","version":"0.1.0",` + fields + `}]}`
	}
	for _, c := range []struct {
		name          string
		xpi           bool
		ext, prefs    string // "" means no file
		want          chrome.ProfileState
		wantUUID      string
	}{
		{"no XPI", false, "", "", chrome.ProfileState{State: chrome.NotInstalled}, ""},
		{"XPI, not started", true, "", "", chrome.ProfileState{State: chrome.NotInstalledYet}, ""},
		{"no entry", true, `{"addons":[{"id":"other@x"}]}`, "", chrome.ProfileState{State: chrome.NotInstalledYet}, ""},
		{"entry, no UUID yet", true, addon(`"active":true,"userDisabled":false,"appDisabled":false`), "", chrome.ProfileState{State: chrome.NotInstalledYet}, ""},
		{"ready", true, addon(`"active":true,"userDisabled":false,"appDisabled":false`), uuidPref, chrome.ProfileState{State: chrome.Ready}, testUUID},
		{"turned off", true, addon(`"active":false,"userDisabled":true,"appDisabled":false`), uuidPref, chrome.ProfileState{State: chrome.TurnedOff}, testUUID},
		{"refused", true, addon(`"active":false,"userDisabled":false,"appDisabled":true`), uuidPref, chrome.ProfileState{State: chrome.Refused}, testUUID},
	} {
		dir := t.TempDir()
		if c.xpi {
			must(t, os.MkdirAll(filepath.Join(dir, "extensions"), 0o755))
			must(t, os.WriteFile(XPIPath(dir), []byte("xpi"), 0o644))
		}
		if c.ext != "" {
			must(t, os.WriteFile(filepath.Join(dir, "extensions.json"), []byte(c.ext), 0o644))
		}
		if c.prefs != "" {
			must(t, os.WriteFile(filepath.Join(dir, "prefs.js"), []byte(c.prefs), 0o644))
		}
		got, uuid, err := ReadState(dir, "0.1.0")
		if err != nil || got != c.want || uuid != c.wantUUID {
			t.Errorf("%s: %+v %q %v; want %+v %q", c.name, got, uuid, err, c.want, c.wantUUID)
		}
	}
	// Another build.
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "extensions"), 0o755))
	must(t, os.WriteFile(XPIPath(dir), []byte("xpi"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "extensions.json"), []byte(addon(`"active":true`)), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "prefs.js"), []byte(uuidPref), 0o644))
	if got, _, err := ReadState(dir, "0.2.0"); err != nil || got != (chrome.ProfileState{State: chrome.OldBuild, Version: "0.1.0"}) {
		t.Errorf("old build: %+v %v", got, err)
	}
}
```

Run `bin/dev run go test ./internal/firefox/ -run ReadState`. It should FAIL, because `ReadState` is undefined.

- [ ] **Step 4: `internal/firefox/state.go`.**

```go
package firefox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
)

// ReadState reads the add-on's state in a Gecko profile from the XPI,
// extensions.json and the UUID in prefs.js, and returns the UUID with it.
// Firefox writes both files within about a second of a change. A user's
// Remove deletes the XPI and leaves no record, so it reads as not
// installed.
func ReadState(profileDir, version string) (chrome.ProfileState, string, error) {
	if _, err := os.Stat(XPIPath(profileDir)); errors.Is(err, fs.ErrNotExist) {
		return chrome.ProfileState{State: chrome.NotInstalled}, "", nil
	} else if err != nil {
		return chrome.ProfileState{}, "", err
	}
	uuid, err := UUID(profileDir)
	if err != nil {
		return chrome.ProfileState{}, "", err
	}
	path := filepath.Join(profileDir, "extensions.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return chrome.ProfileState{State: chrome.NotInstalledYet}, uuid, nil
	}
	if err != nil {
		return chrome.ProfileState{}, "", err
	}
	type addon struct {
		ID           string `json:"id"`
		Active       bool   `json:"active"`
		UserDisabled bool   `json:"userDisabled"`
		AppDisabled  bool   `json:"appDisabled"`
		Version      string `json:"version"`
	}
	var f struct {
		Addons []addon `json:"addons"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return chrome.ProfileState{}, "", fmt.Errorf("%s: %w", path, err)
	}
	i := slices.IndexFunc(f.Addons, func(a addon) bool { return a.ID == extension.GeckoID })
	if i < 0 {
		return chrome.ProfileState{State: chrome.NotInstalledYet}, uuid, nil
	}
	switch a := f.Addons[i]; {
	case a.UserDisabled:
		return chrome.ProfileState{State: chrome.TurnedOff}, uuid, nil
	case a.AppDisabled:
		return chrome.ProfileState{State: chrome.Refused}, uuid, nil
	case a.Version != version:
		return chrome.ProfileState{State: chrome.OldBuild, Version: a.Version}, uuid, nil
	case !a.Active || uuid == "":
		return chrome.ProfileState{State: chrome.NotInstalledYet}, uuid, nil
	}
	return chrome.ProfileState{State: chrome.Ready}, uuid, nil
}
```

Run the tests. They should PASS.

- [ ] **Step 5: One open, one wait.** Write the failing tests first. In `internal/chrome/chrome_test.go`, replace `TestRetryStopsWhenBound`, `TestBindRetriesUntilConfirmed` and `TestBindGivesUpAfterThreeTries` with:

```go
func TestOpenStartsOnceAndWaits(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	start := time.Now()
	ok, err := Open(fake, []string{"x", "y"}, func(time.Time) bool { return false })
	if err != nil || ok || time.Since(start) < BindWait {
		t.Fatalf("ok %v, err %v, after %v", ok, err, time.Since(start))
	}
	if data, _ := os.ReadFile(log); string(data) != "x y\n" {
		t.Fatalf("calls:\n%s", data)
	}
}

func TestBindOpensThePageOnce(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	udd := filepath.Join(t.TempDir(), "udd")
	err := Chrome.Bind(fake, udd, "Profile 1", "pjjjaeommfpeolloibnafkcmkindkagl", "chrome-work", func(time.Time) bool {
		data, _ := os.ReadFile(log)
		return len(data) > 0
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if want := "--user-data-dir=" + udd + " --profile-directory=Profile 1 chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/bind.html#chrome-work\n"; string(data) != want {
		t.Fatalf("calls:\n%s", data)
	}
}

func TestBindSaysWhenTheExtensionDoesNotReportIn(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	udd, _ := Brave.DefaultUserDataDir()
	err := Brave.Bind(fake, udd, "Default", "pjjjaeommfpeolloibnafkcmkindkagl", "brave-a", func(time.Time) bool { return false })
	if err == nil || err.Error() != "mirrormark did not report in within 200ms of opening the page; look at the tab Brave opened" {
		t.Fatalf("err %v", err)
	}
	// The default data directory is left implicit, as a user would type it.
	if data, _ := os.ReadFile(log); string(data) != "--profile-directory=Default chrome-extension://pjjjaeommfpeolloibnafkcmkindkagl/bind.html#brave-a\n" {
		t.Fatalf("calls:\n%s", data)
	}
}
```

Keep `TestBindPassesTheAttemptStart` and `TestBindReportsAChromeThatWillNotStart`, with `reported` as the parameter name. In `TestBraveDescription` and `TestLookup`, expect `ExtensionsPage` `brave://extensions` and `chrome://extensions`. In `internal/firefox/firefox_test.go`, `fakeFirefox` no longer writes a UUID (drop `$FAKE_INSTALL` and `UUIDWait`). Replace the three Bind tests with:

```go
func TestBindOpensThePageOnceWithTheUUID(t *testing.T) {
	dir := t.TempDir()
	bin, log := fakeFirefox(t, dir)
	if err := Firefox.Bind(bin, dir, testUUID, "firefox-work", boundFile(bin)); err != nil {
		t.Fatal(err)
	}
	if calls, _ := os.ReadFile(log); string(calls) != "--profile "+dir+" about:blank moz-extension://"+testUUID+"/bind.html#firefox-work\n" {
		t.Fatalf("calls:\n%s", calls)
	}
}

func TestBindSaysWhenTheAddonDoesNotReportIn(t *testing.T) {
	dir := t.TempDir()
	bin, log := fakeFirefox(t, dir)
	err := LibreWolf.Bind(bin, dir, testUUID, "librewolf-work", func(time.Time) bool { return false })
	calls, _ := os.ReadFile(log)
	if err == nil || !strings.Contains(err.Error(), "look at the tab LibreWolf opened") || bytes.Count(calls, []byte("bind.html")) != 1 {
		t.Fatalf("err %v, calls:\n%s", err, calls)
	}
}
```

Run `bin/dev run go test ./internal/...`. It should FAIL to compile.

- [ ] **Step 6: Implement.** `internal/chrome/bind.go` becomes:

```go
// BindWait is how long install waits, after opening the bind page once,
// for the extension to report in. It covers a cold start of the browser
// and of the extension's worker; a slow first LibreWolf start in a
// container took over 10 seconds.
var BindWait = 30 * time.Second

// Open starts bin with args once, in its own session so that a browser it
// starts outlives mirrormark, and waits up to BindWait for reported to
// say the extension reported in since. It does not retry: install opens
// the page only where the profile's files show the extension installed,
// so the page cannot load before the extension.
func Open(bin string, args []string, reported func(since time.Time) bool) (bool, error) {
	start := time.Now()
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	go cmd.Wait()
	for time.Since(start) < BindWait {
		if reported(start) {
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false, nil
}

// Bind opens bind.html#<dbID> once in one of b's profiles with bin and
// waits for the extension to report in.
func (b Browser) Bind(bin, userDataDir, profileDir, extID, dbID string, reported func(since time.Time) bool) error {
	def, _ := b.DefaultUserDataDir()
	var args []string
	if userDataDir != def {
		args = append(args, "--user-data-dir="+userDataDir)
	}
	args = append(args, "--profile-directory="+profileDir, "chrome-extension://"+extID+"/bind.html#"+dbID)
	ok, err := Open(bin, args, reported)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("mirrormark did not report in within %v of opening the page; look at the tab %s opened", BindWait, b.Name)
}
```

`internal/firefox/bind.go`:

```go
// Bind opens bind.html#<dbID> once in the profile, under the add-on's
// UUID from ReadState, and waits for the add-on to report in. about:blank
// opens beside the page because the page closes its own tab, and closing
// the only tab of the only window quits Firefox.
func (b Browser) Bind(bin, profileDir, uuid, dbID string, reported func(since time.Time) bool) error {
	ok, err := chrome.Open(bin, []string{"--profile", profileDir, "about:blank", "moz-extension://" + uuid + "/bind.html#" + dbID}, reported)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("mirrormark did not report in within %v of opening the page; look at the tab %s opened", chrome.BindWait, b.Name)
}
```

Delete `UUIDWait`. Add `ExtensionsPage string // where the user turns extensions on and off` to `chrome.Browser`, set to `chrome://extensions` in `Chrome` and `brave://extensions` in `Brave`. Run `bin/dev run go test ./internal/...`. It should PASS.

- [ ] **Step 7: Failing install tests** (in `cmd/mirrormark/install_test.go`). Add these helpers:

```go
// chromiumReady writes the Preferences a Chromium browser has once it has
// installed and enabled the extension in the profile.
func chromiumReady(t *testing.T, profileDir, id string) {
	t.Helper()
	v, err := extension.Version()
	must(t, err)
	must(t, os.MkdirAll(profileDir, 0o700))
	must(t, os.WriteFile(filepath.Join(profileDir, "Preferences"), []byte(`{"extensions":{"settings":{"`+id+`":{"path":"`+id+`/`+v+`_0","location":2,"disable_reasons":[],"manifest":{"version":"`+v+`"}}}}}`), 0o600))
}

// geckoReady writes the extensions.json and prefs.js a Gecko browser has
// once it has installed the add-on in the profile.
func geckoReady(t *testing.T, profileDir, uuid string) {
	t.Helper()
	v, err := extension.Version()
	must(t, err)
	must(t, os.WriteFile(filepath.Join(profileDir, "extensions.json"), []byte(`{"addons":[{"id":"`+extension.GeckoID+`","active":true,"userDisabled":false,"appDisabled":false,"version":"`+v+`","location":"app-profile"}]}`), 0o600))
	must(t, os.WriteFile(filepath.Join(profileDir, "prefs.js"), []byte(`user_pref("extensions.webextensions.uuids", "{\"mirrormark@chriscz.github.io\":\"`+uuid+`\"}");`+"\n"), 0o600))
}
```

`TestInstallWritesSystemFilesWithSudoThenBinds`: before the second run, call `chromiumReady(t, filepath.Join(udd, "Default"), id)` and do the same for `Profile 1`. After the second run, the Chrome call log holds exactly one line per profile. `TestInstallIntoFirefoxThenBind` and `TestInstallIntoBraveThenBind`: replace the prefs.js-only fixtures with `geckoReady` and `chromiumReady`. `TestInstallReportsAProfileThatDoesNotConfirm`: both profiles ready, `FAKE_NO_HELLO=chrome-personal`, exit 1, output holds `not bound: Default: mirrormark did not report in within 300ms of opening the page; look at the tab Chrome opened, then run mirrormark install --user-data-dir <udd> --profile Default.`, and `#chrome-personal` appears once in the call log, not three times. New test:

```go
// TestInstallOpensNothingWhereMirrormarkIsNotReady: each state the
// profile's files can be in gets its own line, and only a ready profile
// gets the bind page.
func TestInstallOpensNothingWhereMirrormarkIsNotReady(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	fakeSystem(t)
	log := fakeChromeOnPath(t, p)
	udd := braveData(t) // Default, Profile 1, Profile 2, Profile 3
	var out, errb bytes.Buffer
	run([]string{"install", "--browser", "brave", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb)
	id := installedID(t, p)
	chromiumReady(t, filepath.Join(udd, "Default"), id)
	must(t, os.MkdirAll(filepath.Join(udd, "Profile 1"), 0o700))
	must(t, os.WriteFile(filepath.Join(udd, "Profile 1", "Preferences"), []byte(`{}`), 0o600))
	must(t, os.MkdirAll(filepath.Join(udd, "Profile 2"), 0o700))
	must(t, os.WriteFile(filepath.Join(udd, "Profile 2", "Preferences"), []byte(`{"extensions":{"settings":{"`+id+`":{}},"external_uninstalls":["`+id+`"]}}`), 0o600))
	chromiumReady(t, filepath.Join(udd, "Profile 3"), id)
	prefs, _ := os.ReadFile(filepath.Join(udd, "Profile 3", "Preferences"))
	must(t, os.WriteFile(filepath.Join(udd, "Profile 3", "Preferences"), bytes.Replace(prefs, []byte(`"disable_reasons":[]`), []byte(`"disable_reasons":[1]`), 1), 0o600))
	os.Remove(log)
	out.Reset()
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	for _, line := range []string{
		"bound: Default -> brave-",
		"not ready: Profile 1: Brave has not installed mirrormark in this profile yet. Quit Brave completely (close every window), start it again and open this profile, wait half a minute, then run mirrormark install --browser brave --user-data-dir " + udd + ` --profile "Profile 1".`,
		"not ready: Profile 2: you removed mirrormark from this profile, and Brave will not install it there again. Run mirrormark unpack, turn on Developer mode in brave://extensions,",
		"not ready: Profile 3: mirrormark is turned off in this profile. Turn it on in brave://extensions, wait half a minute,",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("no %q in:\n%s", line, out.String())
		}
	}
	if calls, _ := os.ReadFile(log); bytes.Count(calls, []byte("\n")) != 1 || !bytes.Contains(calls, []byte("--profile-directory=Default ")) {
		t.Fatalf("brave calls:\n%s", calls)
	}
}
```

`braveData` is `chromeData` with four profiles in `info_cache`, named `Personal`, `Work`, `Old` and `Off`. Add a Gecko twin, `TestFirefoxInstallOpensNothingWhereTheAddonIsNotReady`. alpha has the XPI but no `extensions.json`, so it should print `not ready: alpha: Firefox Developer Edition has not installed mirrormark in this profile yet. Quit Firefox Developer Edition if it runs in this profile (close every window) and start it there again, then run mirrormark install --browser firefox --profile alpha.` beta is `geckoReady` with `"appDisabled":true` and `"active":false`, so it should print `not ready: beta: Firefox Developer Edition refuses mirrormark in this profile because it is not signed.` Exit 1, and the call log has no line. While Task 1 is in force, Gecko lines carry `c.Name`, as today.

Run `bin/dev run go test ./cmd/mirrormark/`. It should FAIL.

- [ ] **Step 8: `cmd/mirrormark/install.go`.** Add these helpers:

```go
// again is the command that runs install for one profile again, as
// messages tell the user to type it.
func again(base, udd, id string) string {
	s := base
	if udd != "" {
		s += " --user-data-dir " + quoteArg(udd)
	}
	return s + " --profile " + quoteArg(id)
}

// quoteArg puts double quotes around an argument a shell would split or
// expand.
func quoteArg(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-/") == "" {
		return s
	}
	return strconv.Quote(s)
}

// chromiumAdvice says why a Chromium profile is not ready and what to do.
func chromiumAdvice(b chrome.Browser, s chrome.ProfileState, want, again string) string {
	switch s.State {
	case chrome.NeverRun:
		return fmt.Sprintf("%s has never opened this profile; open it in %s once, then run %s", b.Name, b.Name, again)
	case chrome.Removed:
		return fmt.Sprintf("you removed mirrormark from this profile, and %s will not install it there again. Run mirrormark unpack, turn on Developer mode in %s, choose Load unpacked and pick the folder it prints, then run %s", b.Name, b.ExtensionsPage, again)
	case chrome.TurnedOff:
		return fmt.Sprintf("mirrormark is turned off in this profile. Turn it on in %s, wait half a minute, then run %s", b.ExtensionsPage, again)
	case chrome.OldBuild:
		return fmt.Sprintf("this profile runs mirrormark %s, not %s. Quit %s completely (close every window) and start it again, then run %s", s.Version, want, b.Name, again)
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
```

In `cmdInstall`, keep the flag the user gave as `uddFlag := *udd` before it is resolved. Replace the bind loop, from `fmt.Fprintln(stdout, "Opening a tab in each profile to bind it.")` to the end of the loop, with:

```go
	want, err := extension.Version()
	if err != nil {
		return fail(err)
	}
	code := 0
	for _, c := range chosen {
		cmd := again(cb.InstallCommand(), uddFlag, c.Dir)
		// The external file is current here, so a profile without the
		// extension is waiting for the browser, not for mirrormark.
		st, err := chrome.ReadState(filepath.Join(*udd, c.Dir), ext.ID, want, true)
		if err != nil {
			fmt.Fprintf(stdout, "not ready: %s: cannot read its state: %v.\n", c.Dir, err)
			code = 1
			continue
		}
		if st.State != chrome.Ready {
			fmt.Fprintf(stdout, "not ready: %s: %s.\n", c.Dir, chromiumAdvice(cb, st, want, cmd))
			code = 1
			continue
		}
		id := ids[c.Dir]
		reported := func(since time.Time) bool { return db.Connected(p, id, since) }
		if err := cb.Bind(bin, *udd, c.Dir, ext.ID, id, reported); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v, then run %s.\n", c.Dir, err, cmd)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", c.Dir, id)
	}
```

In `installFirefox` (which gains a `udd string` parameter for `again`, passed `uddFlag`), replace the bind loop the same way. Use `firefox.ReadState(tg.dir, want)`, `geckoAdvice`, `again("mirrormark install --browser "+b.Config, udd, tg.c.Name)`, `b.Bind(tg.bin, tg.dir, uuid, id, reported)`, and `tg.c.Name` in the lines. Keep the closing "Now list the folders…" text, printed when `code == 0`. Run `bin/dev run make`. It should PASS.

- [ ] **Step 9: e2e harness.** In `e2e/env.go`:

```go
// ready waits until name's profile files say mirrormark is ready there,
// which is what install checks before it opens the bind page. Chromium
// writes Preferences up to about 20 seconds after it installs the
// extension; Gecko within a second or two.
func (e *Env) ready(name string) {
	e.t.Helper()
	want, err := extension.Version()
	if err != nil {
		e.t.Fatal(err)
	}
	eventually(e.t, 40*time.Second, name+"'s files to show mirrormark ready", func() bool {
		if e.kind(name) == "firefox" {
			s, _, err := firefox.ReadState(e.udd(name), want)
			return err == nil && s.State == chrome.Ready
		}
		s, err := chrome.ReadState(filepath.Join(e.udd(name), "Default"), e.ExtID, want, true)
		return err == nil && s.State == chrome.Ready
	})
}

// mmExit runs mirrormark like mm, but returns a failing run's output and
// exit code instead of failing the test.
func (e *Env) mmExit(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env, cmd.Stdin = e.env, strings.NewReader("")
	out, err := cmd.CombinedOutput()
	t.Logf("mirrormark %s:\n%s", strings.Join(args, " "), out)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("mirrormark %v: %v", args, err)
	}
	return string(out), 0
}
```

`bind` calls `e.ready(name)` first. `TestInstallAndBind`'s second install of a bound profile needs nothing new, since the profile is ready already. Rework `TestBindStartsChrome` (keep the name, and change the comment):

```go
// TestBindStartsChrome follows a user who runs install's second pass
// before Chrome has installed the extension in the profile: install says
// so and opens nothing. Once Chrome has run there and closed, install
// starts Chrome itself, opens the page once and binds.
func TestBindStartsChrome(t *testing.T) {
	e := newEnv(t)
	e.firstInstall(t)
	e.noSandbox(t)
	out, code := e.mmExit(t, e.installArgs("a")...)
	if code != 1 || !strings.Contains(out, "not ready: Default: Chrome has not installed mirrormark in this profile yet.") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Lstat(filepath.Join(e.udd("a"), "SingletonLock")); err == nil {
		t.Fatal("install started Chrome in a profile that was not ready")
	}
	e.launch("a").Close() // Chrome installs the extension and writes Preferences on close
	t.Cleanup(func() { stopChrome(t, e.udd("a")) })
	start := time.Now()
	id := e.bind("a")
	t.Logf("bound in %v with one open", time.Since(start).Round(time.Millisecond))
	stopChrome(t, e.udd("a"))
	e.A = e.launch("a")
	e.open(t, e.A)
	if got := eval[string](t, e.A, `chrome.storage.local.get("db").then((r) => r.db)`); got != id {
		t.Errorf("a is bound to %q, want %q", got, id)
	}
	roundDone(t, e.A, "a round after Chrome restarted")
}
```

Rework `TestFirefoxBindStartsFirefox` the same way. First it should get `not ready: a: Firefox Developer Edition has not installed mirrormark in this profile yet.` with no `lock` symlink in the profile. Then `e.launch("a").Close()`, then `e.bind("a")`, which starts Firefox cold with the URL.

- [ ] **Step 10: Run** `bin/dev run make && bin/dev run make check-e2e`. It should PASS. In `TestBindStartsChrome`, the logged bind time should be under 30 s. If a cold Chrome or Firefox does not report in within the single 30 s wait, stop and report the time it took. Do not add a retry.

- [ ] **Step 11: README.** In the Chrome steps, replace step 3 ("Run `mirrormark install` again. It opens a tab in each profile to bind it, waits for the profile to answer…") with: install first checks each profile's files. It opens a tab once in each profile where mirrormark is installed and turned on, waits up to 30 seconds for it to report in, and prints `bound: <profile> -> <id>`. For any other profile it prints `not ready: <profile>: …`, which says what to do, or `not bound: <profile>: …`. Chrome records the install in the profile up to half a minute after it starts, so a `not ready` just after a restart means: wait, and run it again. Add one sentence: "This page has been blocked by Chrome" (or Brave) on a `chrome-extension://` page means the extension is not loaded in that profile. Make the matching change in the Brave and Firefox steps. The "Firefox starts the profile to learn its UUID" text goes.

- [ ] **Step 12: Commit** `feat(rfc-0001): bind each profile once, only where mirrormark is ready`.

---

### Task 2: List the profiles, and install only into those named

**Files:**
- Modify: `internal/chrome/profiles.go`, `internal/firefox/profiles.go`, `cmd/mirrormark/install.go`, `cmd/mirrormark/main.go`, `internal/chrome/chrome_test.go`, `internal/firefox/firefox_test.go`, `cmd/mirrormark/install_test.go`, `e2e/env.go`, `e2e/install_test.go`, `README.md`

**Interfaces:**
- Consumes: Task 1's `ReadState`s, `ProfileState`, `again`.
- Produces: `chrome.Profile.ID`, `chrome.CompareIDs`, `choose(all, picks, list)`, `listChromium`, `listGecko`.

- [ ] **Step 1: Failing tests for the order** (`internal/chrome/chrome_test.go`):

```go
func TestProfilesSortByIDWithNumbersAsNumbers(t *testing.T) {
	udd := t.TempDir()
	os.WriteFile(filepath.Join(udd, "Local State"), []byte(`{"profile":{"info_cache":{
		"Profile 10":{"name":"j"},"Profile 2":{"name":"b"},"Default":{"name":"d"},"Profile 1":{"name":"a"}}}}`), 0o600)
	ps, err := Chrome.Profiles(udd)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.ID())
	}
	if want := []string{"Default", "Profile 1", "Profile 2", "Profile 10"}; !slices.Equal(ids, want) {
		t.Fatalf("order %q, want %q", ids, want)
	}
}

func TestCompareIDs(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"Profile 2", "Profile 10", -1}, {"Profile 10", "Profile 9", 1}, {"Default", "Profile 1", -1},
		{"abc.default", "abc.default", 0}, {"a01", "a1", 1}, {"x9.y", "x10.a", -1},
	} {
		if got := CompareIDs(c.a, c.b); got != c.want {
			t.Errorf("CompareIDs(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
```

`"a01"` versus `"a1"`: the numbers are equal, so the longer run (more leading zeros) sorts after it, which keeps the order total. In `internal/firefox/firefox_test.go`, an absolute `Path=` in profiles.ini gives `ID()` equal to the directory's base name. Run them. They should FAIL.

- [ ] **Step 2: Implement** in `internal/chrome/profiles.go`:

```go
// ID is how install names a profile: its directory's name, such as
// "Profile 1" or a Gecko profile's "x1y2.default-release".
func (p Profile) ID() string { return filepath.Base(p.Dir) }

// CompareIDs orders profile IDs as people count: runs of digits compare as
// numbers, so "Profile 2" comes before "Profile 10"; everything else
// compares bytewise.
func CompareIDs(a, b string) int {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb), cmp.Compare(da, db)); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

// digits is the length of the run of ASCII digits s starts with.
func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}
```

Both `Profiles` functions sort with `func(a, b Profile) int { return cmp.Or(CompareIDs(a.ID(), b.ID()), strings.Compare(a.Dir, b.Dir)) }`. Update the two comments that say "sorted by directory" to say "sorted by ID, numbers as numbers". Run the tests. They should PASS.

- [ ] **Step 3: Failing tests for `choose`.** Replace `TestChoose`:

```go
func TestChoose(t *testing.T) {
	all := []chrome.Profile{{Dir: "Default", Name: "Personal"}, {Dir: "Profile 1", Name: "Work"}, {Dir: "Profile 2", Name: "Work"}}
	for _, c := range []struct {
		picks []string
		want  []string // dirs, or nil for an error
		err   string
	}{
		{[]string{"Profile 1"}, []string{"Profile 1"}, ""},
		{[]string{"2", "0", "Profile 2"}, []string{"Profile 2", "Default"}, ""},
		{[]string{"Work"}, nil, `no profile "Work"; run mirrormark install to list them`},
		{[]string{"3"}, nil, `no profile number 3; run mirrormark install to list them`},
	} {
		got, err := choose(all, c.picks, "mirrormark install")
		var dirs []string
		for _, p := range got {
			dirs = append(dirs, p.Dir)
		}
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%q: err %v, want %q", c.picks, err, c.err)
			}
			continue
		}
		if err != nil || !slices.Equal(dirs, c.want) {
			t.Errorf("%q: %q, %v", c.picks, dirs, err)
		}
	}
	dup := []chrome.Profile{{Dir: "/a/x.default"}, {Dir: "/b/x.default"}}
	if _, err := choose(dup, []string{"x.default"}, "mirrormark install --browser firefox"); err == nil || err.Error() != `2 profiles have the ID "x.default" (numbers 0, 1); name one by its number` {
		t.Errorf("duplicate ID: %v", err)
	}
}
```

- [ ] **Step 4: `choose`.**

```go
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
			n, err := strconv.Atoi(want)
			if err != nil || strings.Trim(want, "0123456789") != "" {
				return nil, fmt.Errorf("no profile %q; run %s to list them", want, list)
			}
			if n >= len(all) {
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
```

Delete `listProfiles`. Run `bin/dev run go test ./cmd/mirrormark/ -run TestChoose`. It should PASS.

- [ ] **Step 5: Failing listing tests.**

```go
// TestInstallWithoutProfileListsAndWritesNothing: no --profile prints the
// list and touches nothing: no key, no extension, no database, no sudo.
func TestInstallWithoutProfileListsAndWritesNothing(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	_, sudoCalls := fakeSystem(t)
	log := fakeChromeOnPath(t, p)
	udd := braveData(t)
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "brave", "--user-data-dir", udd}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	want := "Brave profiles in " + udd + ":\n" +
		"  #  ID         NAME      STATE          DATABASE\n" +
		"  0  Default    Personal  not installed  -\n" +
		"  1  Profile 1  Work      not installed  -\n" +
		"  2  Profile 2  Old       not installed  -\n" +
		"  3  Profile 3  Off       not installed  -\n" +
		"Nothing was changed. Name the profiles to install into or bind with --profile ID or --profile #, for example:\n" +
		"  mirrormark install --browser brave --user-data-dir " + udd + " --profile Default\n"
	if out.String() != want || errb.Len() != 0 {
		t.Fatalf("output:\n%s\nwant:\n%s\nstderr: %s", out.String(), want, errb.String())
	}
	if entries, _ := os.ReadDir(p.Data); len(entries) != 0 || len(*sudoCalls) != 0 {
		t.Fatalf("wrote %v, sudo %v", entries, *sudoCalls)
	}
	if _, err := os.Stat(p.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config: %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("started Brave")
	}
}
```

Each `Profile N` in `braveData` gets a `Preferences` file (`{}`) so that it does not read as `never run`. `Default` gets one too. Add a second listing test after a full install of `Default` and `Profile 1`, using `chromiumReady` for both. `Profile 2` should be `removed` (an `external_uninstalls` fixture), `Profile 3` should be `turned off`, `Default` should be `bound` with `brave-personal`, and the example should be `--profile "Profile 2"`. Add a Gecko listing test from `firefoxData` plus a `neverRun` gamma and a profile last run by release Firefox (as `TestInstallWithOneReleaseProfileWritesNothing` builds it). The rows should be `xalpha.alpha … not installed`, `xbeta.beta … not installed`, `xgamma.gamma gamma never run -`, and the release one `not Firefox Developer Edition`. Nothing may be written in any profile (`user.js` absent).

Rework the tests that ran install without `--profile` to name their profiles: `TestInstallWritesSystemFilesWithSudoThenBinds` (`--profile Default --profile 1`), `TestInstallReportsAProfileThatDoesNotConfirm`, `TestInstallResolvesASymlinkedDataDir`, `TestInstallWarnsWhenItReplacesAnotherExtensionsHostManifest`, `TestInstallIntoFirefoxThenBind`, `installBoth`, `installChromium` and the Brave and Gecko ones. The Gecko ones use the ID: `--profile xbeta.beta`, and the line becomes `bound: xbeta.beta -> firefox-beta`. Delete `TestInstallSkipsAProfileThatHasNeverRun` and `TestInstallStopsWhenNoProfileHasRun`. Keep `TestInstallRefusesANamedProfileThatHasNeverRun`, with `--profile xalpha.alpha --profile xgamma.gamma` and its message `xgamma.gamma: it has never run`. `TestInstallRejectsAnUnknownProfile` expects `no profile "Nope"; run mirrormark install --user-data-dir <dir> to list them`. `TestInstallIntoAbsentBraveWritesNothing` passes `--profile Default` and still expects the `PATH` refusal. Run them. They should FAIL.

- [ ] **Step 6: Implement the listing and the required `--profile`.** In `cmdInstall`, after `profiles, err := cb.Profiles(*udd)` and before `choose`:

```go
	list := cb.InstallCommand()
	if uddFlag != "" {
		list += " --user-data-dir " + quoteArg(uddFlag)
	}
	if len(picks) == 0 {
		return listChromium(p, cb, *udd, list, profiles, stdout, stderr)
	}
	chosen, err := choose(profiles, picks, list)
```

In `installFirefox`, use the same with `list := "mirrormark install --browser " + b.Config` (plus `--user-data-dir` if given), placed before the Check loop. The `skipped` slice and its branch go: the `errors.Is(err, firefox.ErrNeverRun) && len(picks) == 0` case is deleted, so a named never-run profile falls into `refused`. Change the refusal text to `cannot install into these profiles; leave them out:\n  …`. `targets` cannot be empty any more. Profile lines and `again` use `c.ID()` / `tg.c.ID()` in both families. The listing:

```go
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
```

Run `bin/dev run make`. It should PASS.

- [ ] **Step 7: Usage text** in `cmd/mirrormark/main.go`:

```
  install     List a browser's profiles, or install into Google Chrome,
              Brave, Firefox Developer Edition or LibreWolf and bind the
              profiles named
              [--browser chrome|brave|firefox|librewolf] [--user-data-dir DIR]
              [--profile ID_OR_NUMBER]...
```

The flag's help: `profile to install into or bind, by ID (its directory, such as "Profile 1") or by number in the list install prints without --profile; repeatable`. Update any test that compares the usage text.

- [ ] **Step 8: e2e.** In `e2e/env.go`, `installArgs` for Chrome becomes `[]string{"install", "--user-data-dir", e.udd(name), "--profile", "Default"}`. The Firefox one already passes `--profile name`, which is now the directory's base name, because `-CreateProfile "a <dir>"` makes the directory `.../firefox/a`. Update `bind`'s comment: install names every profile by its ID, `Default` or the Gecko directory `a`. In `e2e/install_test.go`, `TestInstallAndBind`'s second install is `e.mm(t, "install", "--user-data-dir", e.A.UDD, "--profile", "Default")`. Add one check to `TestInstallAndBind`, run after the bindings:

```go
	// Without --profile, install lists and changes nothing.
	out := e.mm(t, "install", "--user-data-dir", e.A.UDD)
	if !strings.Contains(out, "  0  Default") || !strings.Contains(out, "bound") || !strings.Contains(out, "Nothing was changed.") {
		t.Fatalf("listing:\n%s", out)
	}
```

Run `bin/dev run make && bin/dev run make check-e2e`. It should PASS.

- [ ] **Step 9: README.** In the Chrome, Brave, Firefox and LibreWolf steps, step 1 becomes this. Run `mirrormark install` (with `--browser …`) to see the profiles, their numbers and state. Then run it with `--profile ID` or `--profile #` for each profile to bind; IDs are directory names (`Default`, `"Profile 1"`, a Firefox profile's folder name). Remove "By default it binds every profile" and `--profile Work` (names), and the never-run skip text. Say once that the numbers follow the IDs in order and stay the same while no profile is added or removed.

- [ ] **Step 10: Commit** `feat(rfc-0001): list the profiles, and install only into those named [new feature]`.

## After the last task

- If anything was built differently from Section 10, record it in a `docs(rfc-0001)` commit: Section 10, Phase 7 and History.
- Add to `TASKS.md` under "Manual checks still to do": on the desktop Brave that showed "blocked by Brave", run `mirrormark install --browser brave`, check the listing, then `--profile` one profile, following the messages. This includes a run under firejail, to see whether it hides `/opt/brave.com/brave/extensions/`.

## Risks

- **Preferences lag.** A Chromium profile reads `not installed yet` for up to about 20 s after a start or a profile switch. The message says to wait. The e2e harness waits 40 s in `ready`.
- **A stale "ready".** The user may turn the extension off seconds before `install` runs. Then it opens the page once and prints `not bound` after 30 s. That is accepted, and the message points at the tab.
- **Undocumented fields.** If Chromium moved `extensions.settings`, every profile would read `not installed yet` and nothing would be bound: a safe failure, which the e2e tests show at once.
- **Test time.** Each Chromium `bind` in e2e now waits for `Preferences`, about 10–17 s, once per setup, because both profiles start together.
