# mirrormark for Firefox Developer Edition: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. When writing Go, use the `use-modern-go` skill.

**Goal:** Firefox Developer Edition profiles join the same unions as Chrome profiles: the one extension runs in both browsers, `mirrormark install --browser firefox` installs and binds Firefox profiles without root, and the e2e tests show sync between two Firefox profiles and between Firefox and Chrome.

**Architecture:** Nothing in the host loop, the merge or the file formats changes. The extension hides Firefox's differences (missing events, root ids, separators, move indexes) so the host sees the same tree shape from both browsers. A new `internal/firefox` package lists profiles, builds the XPI and the per-profile files, reads the extension's UUID from `prefs.js` and binds a profile, reusing `internal/chrome`'s profile type, file helpers and bind loop. The e2e harness gets a driver interface with two implementations: the existing DevTools pipe for Chrome and a small standard-library WebDriver BiDi client for Firefox.

**Tech Stack:** Go 1.27.2 (standard library plus `go.yaml.in/yaml/v3`); plain JavaScript MV3 extension; Docker dev image adding Firefox Developer Edition 159.0b2 and D-Bus beside Google Chrome 155; e2e tests in Go over the DevTools pipe (Chrome) and WebDriver BiDi on a local WebSocket (Firefox).

**Spec:** [`../0001_local-bookmark-sync-host.md`](../0001_local-bookmark-sync-host.md), Section 8 and Phase 5. Empirical findings: [`firefox-install-spike.md`](firefox-install-spike.md). The Chrome plan, [`plan.md`](plan.md), describes the code this builds on.

## Global Constraints

- Everything in the Chrome plan's Global Constraints still holds (module, dependencies, pinned Ubuntu, Go and Chrome, `bin/dev run make` only, no `any` values or unchecked type assertions, no RFC references in code, commit format with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, README updated in the same commit).
- **Chrome stays green at every commit:** `bin/dev run make` and `bin/dev run make check-e2e` pass after each task. Tasks that touch the extension or the e2e harness run the Chrome e2e suite before committing.
- Firefox Developer Edition 159.0b2: `https://download-installer.cdn.mozilla.net/pub/devedition/releases/159.0b2/linux-x86_64/en-US/firefox-159.0b2.tar.xz`, sha256 `710dfa80e9edcad6c31f5aa0b3dffb4164b14a60be09316621de3894d8944b40`, unpacked to `/opt/firefox-devedition` in the dev image.
- No make step builds the XPI. Like the CRX, `install` derives it from the files embedded in the binary, so `extension/` stays the one source and `make build` the one build.
- Gecko id: `mirrormark@chriscz.github.io`. `strict_min_version`: `128.0`.
- Firefox root ids → config keywords: `toolbar_____` → `toolbar`, `unfiled_____` → `other`, `mobile______` → `mobile`, `menu________` → `menu`. `menu` is valid only for Firefox members.
- `user.js` lines `install` adds, exactly:
  `user_pref("xpinstall.signatures.required", false);`
  `user_pref("extensions.autoDisableScopes", 0);`
  `user_pref("extensions.startupScanScopes", 1);`
  `uninstall` writes `user_pref("xpinstall.signatures.required", true);` and `user_pref("extensions.autoDisableScopes", 15);` in place of the first two.
- Per-user native host manifest: `~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json`, with `allowed_extensions`. Never `~/.config/mozilla/native-messaging-hosts/`.
- XPI path: `<profile>/extensions/mirrormark@chriscz.github.io.xpi`. The UUID comes from `extensions.webextensions.uuids` in `<profile>/prefs.js`; never write that pref.
- Firefox binary: `<LastPlatformDir>/firefox` from `<profile>/compatibility.ini`; channel from `<LastPlatformDir>/defaults/pref/channel-prefs.js`. Only channel `aurora` is accepted.
- Ids and labels: `firefox-<slug>`, `firefox/<slug>`, `browser: firefox`, slug of the profile's name as for Chrome.
- Bind command: `<bin> --profile <dir> about:blank moz-extension://<uuid>/bind.html#<db>`; wait up to 15 s for the UUID after starting Firefox; then 3 tries of 4 s each (the Chrome loop).
- Separators are never sent to the host, never moved, edited or copied; the only separators the extension removes are those directly inside a folder it is about to `remove`.

## Verified before planning

From the Firefox spike (2026-10-15, Docker, Developer Edition 159.0b2): the per-profile XPI and `user.js` install; release 158.0 refusing it; the shared manifest in Firefox and Chrome for Testing 155; the missing bookmark events; the per-user host manifest and Firefox's two host arguments; the port keeping the event page alive; the UUID in `prefs.js`; binding cold and over D-Bus remoting (not headless); Firefox's roots, GUID ids, separators, same-parent move indexes and index clamping; profile roots and the new profile switcher; a standard-library BiDi client in content and chrome scope.

**Not verified, and checked by the task named:** BiDi and D-Bus remoting in one Firefox instance (Task 13); Firefox picking up a replaced XPI (Task 13); uninstall's prefs and XPI removal taking effect (Task 15); which URLs Firefox refuses (Task 15); `remote.prefs.recommended` leaving install's prefs alone (Task 13); the reconnect alarm in an event page (covered by Task 14's closed-browser test only indirectly).

## File structure

```
extension/
  manifest.json             + background.scripts, browser_specific_settings.gecko (Task 2)
  background.js             Firefox portability (Task 1)
  embed.go                  + GeckoID (Task 2)
cmd/mirrormark/
  main.go                   host mode for Firefox's arguments (Task 3)
  install.go                --browser firefox for install and uninstall (Tasks 5, 9, 10)
internal/chrome/bind.go     Retry, the bind loop both browsers use (Task 5)
internal/fakebrowser/       NewFirefox: a Firefox-shaped tree (Task 4)
internal/round/round.go     menu is Firefox-only (Task 4)
internal/firefox/           new (Tasks 6–8)
  profiles.go               DefaultRoot, Profiles, Dir
  files.go                  XPI, Files, Channel, UUID, ResetPrefs
  bind.go                   Bind
  firefox_test.go
Dockerfile, Makefile        Developer Edition, D-Bus (Task 11)
e2e/
  browser.go                driver interface, Chrome driver (Task 12)
  bidi.go                   WebDriver BiDi client and Firefox driver (Task 13)
  env.go                    browser kinds per profile (Tasks 12–13)
  install_test.go           Firefox install, bind and upgrade tests (Task 13)
  sync_test.go              sync tests per browser pair (Task 14)
  firefox_test.go           separators, menu, uninstall, URLs (Task 15)
```

## Interfaces between tasks

```go
// extension (Task 2)
const GeckoID = "mirrormark@chriscz.github.io"

// internal/chrome (Task 5)
func Retry(bin string, args []string, bound func(since time.Time) bool) (bool, error)
// existing, reused by firefox: Profile{Dir, Name}, File{Path, Data}, FilesCurrent, WriteFiles, RemoveFiles

// cmd/mirrormark (Task 5)
func ensureDB(p db.Paths, browser, path string, c chrome.Profile) (string, error)

// internal/fakebrowser (Task 4)
func NewFirefox() *Browser   // roots root________, menu________, toolbar_____, unfiled_____, mobile______

// internal/firefox (Tasks 6–8)
func DefaultRoot() (string, error)
func Profiles(root string) ([]chrome.Profile, error)   // Dir as profiles.ini gives it, or the scanned directory's name
func Dir(root string, p chrome.Profile) string
var Prefs []string                                      // the three user.js lines
var ResetPrefs map[string]string                        // install's line → uninstall's line
func XPI() ([]byte, error)
func XPIPath(profileDir string) string
func HostManifestPath() (string, error)
func Files(profileDirs []string, bin string) ([]chrome.File, error)
func Channel(profileDir string) (bin, channel string, err error)
func UUID(profileDir string) (string, error)            // "" while Firefox has not installed the add-on
var UUIDWait = 15 * time.Second
func Bind(bin, profileDir, dbID string, bound func(since time.Time) bool) error

// e2e (Tasks 12–13)
type driver interface {
	eval(expr string) (json.RawMessage, error)
	openPage(url string) error
	close()
}
func (b *Browser) open(t *testing.T, url string)
func (e *Env) open(t *testing.T, b *Browser)
func setupPair(t *testing.T, a, b string) *Env      // kinds "chrome" or "firefox"
```

## Tasks

| # | Commit subject |
| --- | --- |
| 1 | `feat(rfc-0001): let the extension run in Firefox` |
| 2 | `feat(rfc-0001): give the manifest Firefox's background script and id` |
| 3 | `feat(rfc-0001): run as the host when Firefox starts mirrormark` |
| 4 | `feat(rfc-0001): sync Firefox-shaped trees and keep menu to Firefox members` |
| 5 | `refactor(rfc-0001): share the bind loop and database ids between browsers` |
| 6 | `feat(rfc-0001): list Firefox profiles` |
| 7 | `feat(rfc-0001): build the XPI and the files a Firefox profile needs` |
| 8 | `feat(rfc-0001): bind a Firefox profile` |
| 9 | `feat(rfc-0001): install into Firefox Developer Edition [new feature]` |
| 10 | `feat(rfc-0001): uninstall from Firefox and restore its signature checks` |
| 11 | `build(rfc-0001): add Firefox Developer Edition and D-Bus to the dev image` |
| 12 | `refactor(rfc-0001): put the e2e browser behind a driver` |
| 13 | `test(rfc-0001): install and bind Firefox profiles end to end` |
| 14 | `test(rfc-0001): run the sync tests between Firefox and Chrome` |
| 15 | `test(rfc-0001): cover separators, menu, uninstall and URLs in Firefox` |

Tasks 1–4 change nothing for a Chrome user. Tasks 5–10 are a working Firefox install, unit-tested. Tasks 11–15 prove it end to end. Any prefix of the stack can land.

---

### Task 1: Let the extension run in Firefox

**Files:**
- Modify: `extension/background.js`
- Modify: `extension/embed.go` (package comment: mention Firefox)

**Interfaces:**
- Consumes: the protocol as it is.
- Produces: trees with `root` set for Firefox's four top-level folders, no separators, and indexes counting only non-separators; `hello.browser` = `firefox/<version>` in Firefox.

There are no JavaScript unit tests; the Chrome e2e suite is the regression test, and Tasks 13–15 test Firefox.

- [ ] **Step 1: Detect the browser once, at the top of `background.js`**

After `const ROOTS = …`, add:

```js
// Firefox has no folderType; its top-level folders have fixed ids.
const FIREFOX_ROOTS = {
  toolbar_____: "toolbar",
  unfiled_____: "other",
  mobile______: "mobile",
  menu________: "menu",
};
const [, BROWSER = "Chrome", VERSION = ""] =
  navigator.userAgent.match(/(Firefox|Chrome)\/([\d.]+)/) ?? [];
const FIREFOX = BROWSER === "Firefox";
// Separators exist only in Firefox. The host never sees them: they hold
// nothing to sync, and Chrome could not hold them.
const isSeparator = (n) => n.type === "separator";
```

- [ ] **Step 2: Listen only to events that exist**

Replace the event loop:

```js
// Firefox lacks onChildrenReordered and the import events; adding a
// listener to a missing one would stop this script here.
for (const e of [
  chrome.bookmarks.onCreated,
  chrome.bookmarks.onRemoved,
  chrome.bookmarks.onChanged,
  chrome.bookmarks.onMoved,
  chrome.bookmarks.onChildrenReordered,
  chrome.bookmarks.onImportEnded,
]) {
  e?.addListener(changed);
}
```

- [ ] **Step 3: Read the disconnect reason in both browsers, and label the hello**

In `open()`:

```js
  p.onDisconnect.addListener((dp) => {
    // Firefox puts the reason on the port; Chrome in runtime.lastError.
    const err = dp?.error?.message ?? chrome.runtime.lastError?.message;
```

and replace the `version` line and the hello:

```js
  p.postMessage({ v: V, type: "hello", db, browser: BROWSER.toLowerCase() + "/" + VERSION });
```

- [ ] **Step 4: Send the tree without separators, with Firefox's roots**

In `sendTree()`:

```js
  const walk = (n, parent) =>
    (n.children ?? [])
      .filter((c) => !isSeparator(c))
      .forEach((c, index) => {
        nodes.push({
          id: c.id,
          parent,
          index,
          title: c.title,
          url: c.url,
          root: parent === null ? (ROOTS[c.folderType] ?? FIREFOX_ROOTS[c.id]) : undefined,
          syncing: c.syncing || undefined,
        });
        walk(c, c.id);
      });
```

- [ ] **Step 5: Map the host's indexes to the browser's**

Replace `clamp` with:

```js
// realIndex turns the host's index, which counts only siblings that are not
// separators, into the browser's. An index past the end means the end.
// For a move within one parent the host means Chrome's reading, the
// position before the node leaves its old place; Firefox reads it as the
// final position, so a move down there needs one less. The host never sends
// such a move, as reordering stays local, but an op should mean the same in
// both browsers.
async function realIndex(parentId, index, moving) {
  if (index === undefined) return undefined;
  const kids = await chrome.bookmarks.getChildren(parentId);
  const shown = kids.filter((k) => !isSeparator(k));
  let i = index < shown.length ? shown[index].index : kids.length;
  if (FIREFOX && moving?.parentId === parentId && i > moving.index) i--;
  return i;
}
```

In `applyOp`, `create` uses `index: await realIndex(parent, op.index)` and `move` uses `index: await realIndex(parent, op.index, n)`.

- [ ] **Step 6: Take a purged folder's separators with it**

In `applyOp`'s `remove` case, before `chrome.bookmarks.remove(op.id)`:

```js
        // The host removes a folder only after everything it knows inside,
        // but it cannot see separators, and neither browser removes a
        // folder that is not empty.
        if (n.url === undefined) {
          for (const k of await chrome.bookmarks.getChildren(op.id)) {
            if (isSeparator(k)) await chrome.bookmarks.remove(k.id);
          }
        }
```

Update the file's header comment: "Chrome and Firefox install it in every bound profile".

- [ ] **Step 7: Verify Chrome is unchanged**

Run: `bin/dev run make && bin/dev run make check-e2e`
Expected: PASS, every e2e test, and in the host log `browser=chrome/155.0.8059.79`.

- [ ] **Step 8: Commit**

```bash
git add extension/background.js extension/embed.go
git commit -m "feat(rfc-0001): let the extension run in Firefox" -m "Skip bookmark events Firefox lacks, name its top-level folders by id, hide separators from the host and map indexes around them, read the disconnect reason from the port, and label the hello firefox/<version>. Chrome sees no change." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: One manifest for both browsers

**Files:**
- Modify: `extension/manifest.json`
- Modify: `extension/embed.go`
- Test: `extension/embed_test.go`

**Interfaces:**
- Produces: `extension.GeckoID`.

- [ ] **Step 1: Write the failing test** in `extension/embed_test.go`:

```go
// Firefox runs background.scripts as an event page and takes its id from
// browser_specific_settings; Chrome ignores both and runs the service worker.
func TestManifestServesFirefoxToo(t *testing.T) {
	raw, err := Files.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Background struct {
			ServiceWorker string   `json:"service_worker"`
			Scripts       []string `json:"scripts"`
		} `json:"background"`
		Specific struct {
			Gecko struct {
				ID         string `json:"id"`
				MinVersion string `json:"strict_min_version"`
			} `json:"gecko"`
		} `json:"browser_specific_settings"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Background.ServiceWorker != "background.js" || !slices.Equal(m.Background.Scripts, []string{"background.js"}) {
		t.Fatalf("background %+v", m.Background)
	}
	if m.Specific.Gecko.ID != GeckoID || m.Specific.Gecko.MinVersion != "128.0" {
		t.Fatalf("gecko %+v", m.Specific.Gecko)
	}
}
```

- [ ] **Step 2: Run it.** `bin/dev run go test ./extension -run TestManifestServesFirefoxToo` → FAIL: `undefined: GeckoID`.

- [ ] **Step 3: Implement.** In `embed.go` beside `HostName`:

```go
// GeckoID is the extension's id in Firefox. Firefox does not derive ids from
// a key, so every install shares it.
const GeckoID = "mirrormark@chriscz.github.io"
```

In `manifest.json`:

```json
  "background": { "service_worker": "background.js", "scripts": ["background.js"] },
  "browser_specific_settings": {
    "gecko": { "id": "mirrormark@chriscz.github.io", "strict_min_version": "128.0" }
  },
```

- [ ] **Step 4: Run.** `bin/dev run make && bin/dev run make check-e2e` → PASS (Chrome loads the CRX; `TestInstallAndBind` checks it is enabled).

- [ ] **Step 5: Commit** `extension/` with subject `feat(rfc-0001): give the manifest Firefox's background script and id`.

---

### Task 3: Host mode for Firefox's arguments

**Files:**
- Modify: `cmd/mirrormark/main.go`
- Modify: `internal/host/host.go:159` (wording), `internal/host/protocol.go` (comment and error wording)
- Test: `cmd/mirrormark/main_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Firefox starts the host with the host manifest's path and the
// extension's id.
func TestFirefoxArgumentsRunTheHost(t *testing.T) {
	d := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, d+"/"+v)
	}
	var out, errb bytes.Buffer
	args := []string{"/home/u/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json", "mirrormark@chriscz.github.io"}
	if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("host wrote %q to stdout", out.String())
	}
}
```

- [ ] **Step 2: Run.** `bin/dev run go test ./cmd/mirrormark -run TestFirefoxArgumentsRunTheHost` → FAIL with exit code 2 (unknown command).

- [ ] **Step 3: Implement** in `run`:

```go
	// Chrome runs the host with the extension's origin as its only argument;
	// Firefox with the host manifest's path and the extension's id.
	if strings.HasPrefix(args[0], "chrome-extension://") ||
		strings.HasSuffix(args[0], "/"+extension.HostName+".json") || args[0] == "host" {
```

(import `github.com/chriscz/mirrormark/extension`). In `host.go` the version message becomes `"…; restart the browser after upgrading mirrormark"`; in `protocol.go` the size error becomes `"message of %d bytes is over the browsers' 1 MB limit"` and the comment says both browsers limit messages to the extension to 1 MB. `grep -rn "restart Chrome\|Chrome's 1 MB" --include=*.go .` must find nothing left that a test relies on.

- [ ] **Step 4: Run.** `bin/dev run make` → PASS.

- [ ] **Step 5: Commit** subject `feat(rfc-0001): run as the host when Firefox starts mirrormark`.

---

### Task 4: Firefox-shaped trees and the `menu` folder

**Files:**
- Modify: `internal/fakebrowser/fakebrowser.go`
- Modify: `internal/round/round.go`
- Test: `internal/round/round_test.go`, `internal/round/property_test.go`

**Interfaces:**
- Produces: `fakebrowser.NewFirefox() *Browser`.

- [ ] **Step 1: Write the failing tests** in `round_test.go`:

```go
// A Firefox member's path may start with menu, the Bookmarks Menu.
func TestFirefoxMenuPath(t *testing.T) {
	b := fakebrowser.NewFirefox()
	f := profile("f")
	f.Profile.Browser = "firefox"
	p := round.Make(round.Input{Config: config(map[string]string{"f": "menu/Work"}), Self: f, Live: live(t, b), NewM: merge.NewM})
	if !p.EnsureOnly || len(p.Ops) != 1 || p.Ops[0].Parent != "menu________" {
		t.Fatalf("plan %+v, want one create in menu________", p)
	}
	apply(t, b, p)
	if got := b.Lines("menu________"); !slices.Equal(got, []string{"/Work/"}) {
		t.Fatalf("menu %q", got)
	}
}

// Chrome has no menu folder: say so rather than "no top-level folder".
func TestMenuIsFirefoxOnly(t *testing.T) {
	b := fakebrowser.New()
	p := round.Make(round.Input{Config: config(map[string]string{"a": "menu/Work"}), Self: profile("a"), Live: live(t, b), NewM: merge.NewM})
	if len(p.Ops) != 0 || !strings.Contains(p.Skipped["work"], "only Firefox has") {
		t.Fatalf("plan %+v", p)
	}
}
```

And in `property_test.go`'s `newHosts`, make the third member Firefox-shaped, so the fuzz tests mix both browsers:

```go
		b, bar := fakebrowser.New(), "1"
		file := profile(id)
		if i == 2 {
			b, bar = fakebrowser.NewFirefox(), "toolbar_____"
			file.Profile.Browser = "firefox"
		}
		m := &member{b: b, root: b.Add(bar, "U", ""), file: file}
```

- [ ] **Step 2: Run.** `bin/dev run go test ./internal/round` → FAIL: `undefined: fakebrowser.NewFirefox`.

- [ ] **Step 3: Implement `NewFirefox`.** Give `Browser` the tree's root, the root keywords and an id format:

```go
type Browser struct {
	nodes map[string]*node
	next  int
	refs  map[string]string
	top   *node             // the tree's root, which the extension does not send
	roots map[string]string // top-level folder id → config keyword
	guids bool              // Firefox-like 12-character ids
}

// New returns a browser with Chrome's three top-level folders:
// "1" Bookmarks bar, "2" Other bookmarks, "3" Mobile bookmarks.
func New() *Browser {
	b := &Browser{nodes: map[string]*node{}, refs: map[string]string{},
		roots: map[string]string{"1": "toolbar", "2": "other", "3": "mobile"}}
	b.top = b.fixed(nil, "0", "")
	for i, t := range []string{"Bookmarks bar", "Other bookmarks", "Mobile bookmarks"} {
		b.fixed(b.top, strconv.Itoa(i+1), t)
	}
	b.next = 4
	return b
}

// NewFirefox returns a browser shaped like Firefox: fixed top-level ids,
// including the Bookmarks Menu Chrome lacks, and 12-character ids.
func NewFirefox() *Browser {
	b := &Browser{nodes: map[string]*node{}, refs: map[string]string{}, guids: true,
		roots: map[string]string{"menu________": "menu", "toolbar_____": "toolbar", "unfiled_____": "other", "mobile______": "mobile"}}
	b.top = b.fixed(nil, "root________", "")
	for _, r := range [][2]string{{"menu________", "Bookmarks Menu"}, {"toolbar_____", "Bookmarks Toolbar"},
		{"unfiled_____", "Other Bookmarks"}, {"mobile______", "Mobile Bookmarks"}} {
		b.fixed(b.top, r[0], r[1])
	}
	return b
}

func (b *Browser) fixed(parent *node, id, title string) *node {
	n := &node{id: id, title: title}
	b.nodes[id] = n
	if parent != nil {
		b.insert(parent, n, -1)
	}
	return n
}
```

In `make`, the id becomes `fmt.Sprintf("ff%010d", b.next)` when `b.guids`, else `strconv.Itoa(b.next)`. `Tree()` walks `b.top` and takes `Root` from `b.roots[k.id]`.

- [ ] **Step 4: Implement the check** in `round.Make`, right after `segs, err := db.SplitPath(path)` succeeds:

```go
		// Only Firefox has a Bookmarks Menu. Without this a Chrome member
		// would report "no top-level folder", which hides why.
		if segs[0] == "menu" && in.Self.Profile.Browser != "firefox" {
			skip(fmt.Sprintf("%s starts with menu, which only Firefox has; %s is a %s profile", path, me, in.Self.Profile.Browser))
			continue
		}
```

Update the README's config section: "A path starts with `toolbar`, `other` or `mobile`, or, for a Firefox profile, `menu`, or with the title the browser shows for that folder."

- [ ] **Step 5: Run.** `bin/dev run make` → PASS, including the 300-seed fuzz tests with a Firefox member.

- [ ] **Step 6: Commit** `internal/fakebrowser internal/round README.md`, subject `feat(rfc-0001): sync Firefox-shaped trees and keep menu to Firefox members`.

---

### Task 5: Share the bind loop and database ids between browsers

A refactor; Chrome's behaviour and tests stay the same.

**Files:**
- Modify: `internal/chrome/bind.go`, `cmd/mirrormark/install.go`
- Test: `internal/chrome/chrome_test.go`, `cmd/mirrormark/install_test.go`

**Interfaces:**
- Produces: `chrome.Retry`, `ensureDB(p, browser, path, c)`, `selfPath() (string, error)`, `ensureConfig(p db.Paths) error`.

- [ ] **Step 1: Write the failing test** in `chrome_test.go`:

```go
func TestRetryStopsWhenBound(t *testing.T) {
	shortBindWait(t)
	fake, log := fakeChrome(t)
	ok, err := Retry(fake, []string{"x", "y"}, func(time.Time) bool {
		data, _ := os.ReadFile(log)
		return bytes.Count(data, []byte("\n")) >= 2
	})
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	if data, _ := os.ReadFile(log); string(data) != "x y\nx y\n" {
		t.Fatalf("calls:\n%s", data)
	}
}
```

In `install_test.go`, change `TestEnsureDBMakesUniqueIDs`' calls to the new signature, e.g. `ensureDB(p, "chrome", "/u/a/Default", chrome.Profile{Dir: "Default", Name: "Person 1"})`, and add:

```go
func TestEnsureDBNamesFirefoxProfiles(t *testing.T) {
	p := db.Paths{Data: t.TempDir()}
	id, err := ensureDB(p, "firefox", "/h/.config/mozilla/firefox/x.dev-edition-default", chrome.Profile{Dir: "x.dev-edition-default", Name: "dev-edition-default"})
	d, _ := db.LoadDB(p.ProfileFile(id))
	if err != nil || id != "firefox-dev-edition-default" || d.Profile.Label != "firefox/dev-edition-default" || d.Profile.Browser != "firefox" {
		t.Fatalf("%s %+v %v", id, d, err)
	}
}
```

- [ ] **Step 2: Run.** `bin/dev run go test ./internal/chrome ./cmd/mirrormark` → FAIL (undefined `Retry`; wrong argument count).

- [ ] **Step 3: Implement `Retry`** and rewrite `Bind` on it:

```go
// Retry starts bin with args up to three times, each in its own session so
// that a browser it starts outlives mirrormark, and after each waits up to
// BindWait for bound to report a hello since that attempt started. It
// retries because a browser can open the page before the extension loads.
func Retry(bin string, args []string, bound func(since time.Time) bool) (bool, error) {
	for range bindTries {
		start := time.Now()
		cmd := exec.Command(bin, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return false, err
		}
		go cmd.Wait()
		for time.Since(start) < BindWait {
			if bound(start) {
				return true, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return false, nil
}

func Bind(chromeBin, userDataDir, profileDir, extID, dbID string, bound func(since time.Time) bool) error {
	def, _ := DefaultUserDataDir()
	var args []string
	if userDataDir != def {
		args = append(args, "--user-data-dir="+userDataDir)
	}
	args = append(args, "--profile-directory="+profileDir, "chrome-extension://"+extID+"/bind.html#"+dbID)
	ok, err := Retry(chromeBin, args, bound)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("profile %s did not confirm within %d tries; check that Chrome was restarted after install and that mirrormark is enabled in chrome://extensions there, then run mirrormark install again", profileDir, bindTries)
}
```

- [ ] **Step 4: Generalise `ensureDB`** in `install.go`:

```go
// ensureDB returns the database id of the browser profile at path, …(as now)
func ensureDB(p db.Paths, browser, path string, c chrome.Profile) (string, error) {
	…
	id, label := browser+"-"+base, browser+"/"+base
	for n := 2; ; n++ {
		…
		id, label = fmt.Sprintf("%s-%s-%d", browser, base, n), fmt.Sprintf("%s/%s-%d", browser, base, n)
	}
	d := &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: label, Browser: browser, Path: path}}
```

The Chrome caller passes `"chrome", filepath.Join(*udd, c.Dir), c`. Move the executable lookup into `selfPath()` and the `config.yaml` creation into `ensureConfig(p)`, unchanged. `choose`'s messages drop "Chrome": `no profile %q; the profiles are …`, `more than one profile is named %q; …`.

- [ ] **Step 5: Run.** `bin/dev run make` → PASS.

- [ ] **Step 6: Commit** subject `refactor(rfc-0001): share the bind loop and database ids between browsers`.

---

### Task 6: List Firefox profiles

**Files:**
- Create: `internal/firefox/profiles.go`, `internal/firefox/firefox_test.go`

**Interfaces:**
- Produces: `DefaultRoot`, `Profiles`, `Dir`.

- [ ] **Step 1: Write the failing tests**

```go
package firefox

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
```

- [ ] **Step 2: Run.** `bin/dev run go test ./internal/firefox` → FAIL (package has no code).

- [ ] **Step 3: Implement** `profiles.go`:

```go
// Package firefox installs mirrormark into Firefox Developer Edition on
// Linux, one profile at a time and without root: it lists the profiles,
// writes the XPI and three user.js prefs into each, writes the per-user
// native host manifest, and binds each profile by opening bind.html in it.
//
// Release Firefox installs only add-ons Mozilla signed, so only Developer
// Edition, which honours xpinstall.signatures.required, is supported. That
// pref lets the profile take any unsigned add-on; uninstall restores it.
//
// Firefox picks the extension's moz-extension:// UUID per profile and keeps
// it in prefs.js. Setting it there would replace every add-on's UUID and
// lose their storage, so it is only read.
package firefox

// DefaultRoot is the directory holding profiles.ini: the XDG location
// Firefox uses for new installs since 147, if it holds one, else the
// legacy ~/.mozilla/firefox.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cfg := filepath.Join(home, ".config")
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		cfg = v
	}
	if r := filepath.Join(cfg, "mozilla", "firefox"); fileExists(filepath.Join(r, "profiles.ini")) {
		return r, nil
	}
	return filepath.Join(home, ".mozilla", "firefox"), nil
}

// Profiles lists root's profiles, sorted by Dir: those in profiles.ini,
// with Dir as the file gives it (relative to root, or absolute), and every
// other directory in root that holds a prefs.js, named by what follows its
// first ".". Firefox's new profile switcher lists its profiles only in an
// SQLite file, which this does not read, but each one has a prefs.js.
func Profiles(root string) ([]chrome.Profile, error) {
	var out []chrome.Profile
	seen := map[string]bool{}
	if data, err := os.ReadFile(filepath.Join(root, "profiles.ini")); err == nil {
		for _, s := range iniSections(string(data)) {
			if !strings.HasPrefix(s["[]"], "Profile") || s["Path"] == "" {
				continue
			}
			p := chrome.Profile{Dir: s["Path"], Name: s["Name"]}
			if s["IsRelative"] != "0" {
				p.Dir = filepath.FromSlash(p.Dir)
			}
			out = append(out, p)
			seen[Dir(root, p)] = true
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if !e.IsDir() || seen[dir] || !fileExists(filepath.Join(dir, "prefs.js")) {
			continue
		}
		name := e.Name()
		if _, after, ok := strings.Cut(name, "."); ok {
			name = after
		}
		out = append(out, chrome.Profile{Dir: e.Name(), Name: name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Firefox profiles in %s; start Firefox Developer Edition once and close it", root)
	}
	slices.SortFunc(out, func(a, b chrome.Profile) int { return strings.Compare(a.Dir, b.Dir) })
	return out, nil
}

// Dir is profile p's directory.
func Dir(root string, p chrome.Profile) string {
	if filepath.IsAbs(p.Dir) {
		return p.Dir
	}
	return filepath.Join(root, p.Dir)
}

// iniSections parses an INI file into one map per section; the section's
// name is under "[]".
func iniSections(data string) []map[string]string {
	var out []map[string]string
	for line := range strings.Lines(data) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			out = append(out, map[string]string{"[]": line[1 : len(line)-1]})
		} else if k, v, ok := strings.Cut(line, "="); ok && len(out) > 0 {
			out[len(out)-1][k] = v
		}
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
```

- [ ] **Step 4: Run.** `bin/dev run make` → PASS.

- [ ] **Step 5: Commit** subject `feat(rfc-0001): list Firefox profiles`.

---

### Task 7: The XPI and a profile's files

**Files:**
- Create: `internal/firefox/files.go`
- Test: `internal/firefox/firefox_test.go`

**Interfaces:**
- Consumes: `extension.Files`, `extension.GeckoID`, `extension.HostName`, `chrome.File`.
- Produces: `Prefs`, `ResetPrefs`, `XPI`, `XPIPath`, `HostManifestPath`, `Files`, `Channel`, `UUID`.

- [ ] **Step 1: Write the failing tests**

```go
func TestXPIHoldsTheExtensionWithoutAKey(t *testing.T) {
	data, err := XPI()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := XPI()
	if !bytes.Equal(data, again) {
		t.Fatal("two builds differ, so install would always ask for a restart")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"background.js", "bind.html", "bind.js", "manifest.json", "popup.html", "popup.js"}) {
		t.Fatalf("files %v", names)
	}
	m, _ := z.Open("manifest.json")
	raw, _ := io.ReadAll(m)
	if bytes.Contains(raw, []byte(`"key"`)) || !bytes.Contains(raw, []byte(extension.GeckoID)) {
		t.Fatalf("manifest %s", raw)
	}
}

func TestFilesAppendOnlyMissingPrefs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "user.js"), []byte("user_pref(\"browser.startup.page\", 3);\n"+Prefs[1]+"\n"), 0o644)
	files, err := Files([]string{dir}, "/usr/local/bin/mirrormark")
	if err != nil {
		t.Fatal(err)
	}
	host, _ := HostManifestPath()
	if len(files) != 3 || files[0].Path != host || files[1].Path != XPIPath(dir) || files[2].Path != filepath.Join(dir, "user.js") {
		t.Fatalf("paths %v", files)
	}
	if !bytes.Contains(files[0].Data, []byte(`"allowed_extensions": [`+"\n    \""+extension.GeckoID+`"`)) ||
		!bytes.Contains(files[0].Data, []byte(`"path": "/usr/local/bin/mirrormark"`)) {
		t.Fatalf("host manifest %s", files[0].Data)
	}
	want := "user_pref(\"browser.startup.page\", 3);\n" + Prefs[1] + "\n" + Prefs[0] + "\n" + Prefs[2] + "\n"
	if string(files[2].Data) != want {
		t.Fatalf("user.js %q", files[2].Data)
	}
	if !strings.HasSuffix(host, "/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json") {
		t.Fatal(host)
	}
}

func TestChannelFromTheProfilesFirefox(t *testing.T) {
	ff := t.TempDir()
	os.MkdirAll(filepath.Join(ff, "defaults", "pref"), 0o755)
	os.WriteFile(filepath.Join(ff, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "aurora");`+"\n"), 0o644)
	dir := t.TempDir()
	if _, _, err := Channel(dir); err == nil || !strings.Contains(err.Error(), "start Firefox") {
		t.Fatalf("no compatibility.ini: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte("[Compatibility]\nLastVersion=159.0_20261001000000/20261001000000\nLastPlatformDir="+ff+"\nLastAppDir="+ff+"/browser\n"), 0o644)
	bin, ch, err := Channel(dir)
	if err != nil || bin != filepath.Join(ff, "firefox") || ch != "aurora" {
		t.Fatalf("%s %s %v", bin, ch, err)
	}
}

func TestUUIDFromPrefs(t *testing.T) {
	dir := t.TempDir()
	if u, err := UUID(dir); u != "" || err != nil {
		t.Fatalf("no prefs.js: %q %v", u, err)
	}
	os.WriteFile(filepath.Join(dir, "prefs.js"), []byte(
		"user_pref(\"browser.startup.page\", 3);\n"+
			`user_pref("extensions.webextensions.uuids", "{\"other@example.com\":\"0f0f0f0f-1111-4222-8333-444444444444\",\"mirrormark@chriscz.github.io\":\"5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f40\"}");`+"\n"), 0o600)
	if u, err := UUID(dir); u != "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f40" || err != nil {
		t.Fatalf("%q %v", u, err)
	}
}
```

- [ ] **Step 2: Run.** `bin/dev run go test ./internal/firefox` → FAIL (undefined names).

- [ ] **Step 3: Implement** `files.go`:

```go
package firefox

// Prefs are the user.js lines that let Firefox Developer Edition install
// the unsigned XPI from the profile's extensions folder, enabled and
// without a prompt, at its next start. The last makes a profile that has
// run before look in that folder at all.
var Prefs = []string{
	`user_pref("xpinstall.signatures.required", false);`,
	`user_pref("extensions.autoDisableScopes", 0);`,
	`user_pref("extensions.startupScanScopes", 1);`,
}

// ResetPrefs maps the two lines that weaken the profile's add-on checks to
// lines that set their defaults again, for uninstall. A line in user.js
// alone would not do: Firefox keeps the value in prefs.js once set.
var ResetPrefs = map[string]string{
	Prefs[0]: `user_pref("xpinstall.signatures.required", true);`,
	Prefs[1]: `user_pref("extensions.autoDisableScopes", 15);`,
}

// zipTime is every XPI entry's time, so the same files give the same bytes
// and install can tell an XPI is current.
var zipTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// XPI zips the embedded extension as it is: Firefox ignores Chrome's
// service_worker, and the XPI needs no key.
func XPI() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := fs.WalkDir(extension.Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := extension.Files.ReadFile(name)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	err = zw.Close()
	return buf.Bytes(), err
}

// XPIPath is where Firefox looks for the add-on in one profile.
func XPIPath(profileDir string) string {
	return filepath.Join(profileDir, "extensions", extension.GeckoID+".xpi")
}

// HostManifestPath is Firefox's per-user native host manifest. Firefox
// reads only ~/.mozilla/native-messaging-hosts, even when its profiles live
// under ~/.config/mozilla.
func HostManifestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mozilla", "native-messaging-hosts", extension.HostName+".json"), nil
}

type hostManifestJSON struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Path              string   `json:"path"`
	Type              string   `json:"type"`
	AllowedExtensions []string `json:"allowed_extensions"`
}

// Files are the files install writes: the host manifest letting the add-on
// start bin, then for each profile the XPI and its user.js with every line
// of Prefs it lacks appended. The user's own lines stay as they are.
func Files(profileDirs []string, bin string) ([]chrome.File, error) {
	host, err := HostManifestPath()
	if err != nil {
		return nil, err
	}
	m, _ := json.MarshalIndent(hostManifestJSON{Name: extension.HostName, Description: "mirrormark bookmark sync",
		Path: bin, Type: "stdio", AllowedExtensions: []string{extension.GeckoID}}, "", "  ") // cannot fail
	files := []chrome.File{{Path: host, Data: append(m, '\n')}}
	xpi, err := XPI()
	if err != nil {
		return nil, err
	}
	for _, dir := range profileDirs {
		user, err := os.ReadFile(filepath.Join(dir, "user.js"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if len(user) > 0 && user[len(user)-1] != '\n' {
			user = append(user, '\n')
		}
		for _, p := range Prefs {
			if !hasLine(user, p) {
				user = append(user, p+"\n"...)
			}
		}
		files = append(files, chrome.File{Path: XPIPath(dir), Data: xpi}, chrome.File{Path: filepath.Join(dir, "user.js"), Data: user})
	}
	return files, nil
}

func hasLine(data []byte, line string) bool {
	for l := range strings.Lines(string(data)) {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

var channelRe = regexp.MustCompile(`pref\("app\.update\.channel",\s*"([^"]+)"\)`)

// Channel returns the Firefox binary that last ran the profile, from its
// compatibility.ini, and that Firefox's update channel: "aurora" for
// Developer Edition, "release" for release.
func Channel(profileDir string) (bin, channel string, err error) {
	data, err := os.ReadFile(filepath.Join(profileDir, "compatibility.ini"))
	if err != nil {
		return "", "", fmt.Errorf("cannot tell which Firefox uses %s; start Firefox in that profile once and close it: %w", profileDir, err)
	}
	var dir string
	for _, s := range iniSections(string(data)) {
		if s["[]"] == "Compatibility" {
			dir = s["LastPlatformDir"]
		}
	}
	if dir == "" {
		return "", "", fmt.Errorf("%s names no Firefox (LastPlatformDir)", filepath.Join(profileDir, "compatibility.ini"))
	}
	prefs, err := os.ReadFile(filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
	if err != nil {
		return "", "", fmt.Errorf("cannot read the update channel of the Firefox in %s: %w", dir, err)
	}
	m := channelRe.FindSubmatch(prefs)
	if m == nil {
		return "", "", fmt.Errorf("no update channel in %s", filepath.Join(dir, "defaults", "pref", "channel-prefs.js"))
	}
	return filepath.Join(dir, "firefox"), string(m[1]), nil
}

var uuidsRe = regexp.MustCompile(`user_pref\("extensions\.webextensions\.uuids",\s*("(?:[^"\\]|\\.)*")\);`)

// UUID returns the add-on's moz-extension:// host in the profile, or ""
// if Firefox has not installed it yet. Firefox writes prefs.js a few
// seconds after the start that installs the add-on.
func UUID(profileDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, "prefs.js"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	m := uuidsRe.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	// The pref is a JavaScript string holding JSON; for what Firefox
	// escapes in it, a JSON string literal reads the same.
	var s string
	var ids map[string]string
	if err := json.Unmarshal(m[1], &s); err != nil {
		return "", fmt.Errorf("prefs.js: extensions.webextensions.uuids: %w", err)
	}
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return "", fmt.Errorf("prefs.js: extensions.webextensions.uuids: %w", err)
	}
	return ids[extension.GeckoID], nil
}
```

- [ ] **Step 4: Check the defaults `ResetPrefs` writes** against the image's Firefox once Task 11 has added it; until then they come from Firefox's source. After Task 11, run:
`bin/dev run sh -c 'unzip -p /opt/firefox-devedition/browser/omni.ja defaults/preferences/firefox.js | grep -E "autoDisableScopes|startupScanScopes"; unzip -p /opt/firefox-devedition/omni.ja greprefs.js | grep -E "signatures.required|autoDisableScopes|startupScanScopes"'`
Expected: `autoDisableScopes` 15 and `signatures.required` true. If not, fix `ResetPrefs` and the RFC in Task 10.

- [ ] **Step 5: Run.** `bin/dev run make` → PASS.

- [ ] **Step 6: Commit** subject `feat(rfc-0001): build the XPI and the files a Firefox profile needs`.

---

### Task 8: Bind a Firefox profile

**Files:**
- Create: `internal/firefox/bind.go`
- Test: `internal/firefox/firefox_test.go`

- [ ] **Step 1: Write the failing tests.** A fake Firefox is a shell script that logs its arguments, writes the UUID into `prefs.js` when started without a bind URL (as the start that installs the add-on would), and touches a file when given one:

```go
const testUUID = "5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f40"

// fakeFirefox logs each call; with $FAKE_INSTALL set it writes the UUID on
// the first call, and every bind URL touches $dir/bound.
func fakeFirefox(t *testing.T, profileDir string) (bin, log string) {
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "firefox"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"[ -n \"$FAKE_INSTALL\" ] && printf '%s\\n' 'user_pref(\"extensions.webextensions.uuids\", \"{\\\"mirrormark@chriscz.github.io\\\":\\\"" + testUUID + "\\\"}\");' > \"" + profileDir + "/prefs.js\"\n" +
		"case \"$*\" in *bind.html*) touch \"" + dir + "/bound\";; esac\n"
	os.WriteFile(bin, []byte(script), 0o755)
	old, oldWait := chrome.BindWait, UUIDWait
	chrome.BindWait, UUIDWait = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { chrome.BindWait, UUIDWait = old, oldWait })
	return bin, log
}

func boundFile(bin string) func(time.Time) bool {
	return func(time.Time) bool { _, err := os.Stat(filepath.Join(filepath.Dir(bin), "bound")); return err == nil }
}

func TestBindStartsFirefoxForTheUUIDThenOpensThePage(t *testing.T) {
	t.Setenv("FAKE_INSTALL", "1")
	dir := t.TempDir()
	bin, log := fakeFirefox(t, dir)
	if err := Bind(bin, dir, "firefox-work", boundFile(bin)); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	want := "--profile " + dir + " about:blank\n--profile " + dir + " about:blank moz-extension://" + testUUID + "/bind.html#firefox-work\n"
	if string(calls) != want {
		t.Fatalf("calls:\n%s", calls)
	}
}

func TestBindWithoutUUIDSaysToRestart(t *testing.T) {
	dir := t.TempDir()
	bin, _ := fakeFirefox(t, dir)
	err := Bind(bin, dir, "firefox-work", boundFile(bin))
	if err == nil || !strings.Contains(err.Error(), "restart Firefox") {
		t.Fatalf("err %v", err)
	}
}

func TestBindGivesUpWhenThePageNeverConfirms(t *testing.T) {
	t.Setenv("FAKE_INSTALL", "1")
	dir := t.TempDir()
	bin, log := fakeFirefox(t, dir)
	err := Bind(bin, dir, "firefox-work", func(time.Time) bool { return false })
	calls, _ := os.ReadFile(log)
	if err == nil || bytes.Count(calls, []byte("bind.html")) != 3 {
		t.Fatalf("err %v, calls:\n%s", err, calls)
	}
}
```

- [ ] **Step 2: Run.** → FAIL: `undefined: Bind`.

- [ ] **Step 3: Implement** `bind.go`:

```go
package firefox

// UUIDWait is how long Bind waits for Firefox to install the add-on and
// record its UUID after starting it.
var UUIDWait = 15 * time.Second

// Bind opens bind.html#<dbID> in the profile and waits for bound to report
// the host's hello, with the same tries as for Chrome. It needs the
// add-on's UUID from prefs.js; if Firefox has not recorded one, Bind starts
// Firefox in the profile, which installs the add-on if it is not running
// there yet, and waits for the UUID. about:blank opens beside the page
// because the page closes its own tab, and closing the only tab of the
// only window quits Firefox.
func Bind(bin, profileDir, dbID string, bound func(since time.Time) bool) error {
	uuid, err := UUID(profileDir)
	if err != nil {
		return err
	}
	if uuid == "" {
		cmd := exec.Command(bin, "--profile", profileDir, "about:blank")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // it must outlive us
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		for start := time.Now(); uuid == "" && time.Since(start) < UUIDWait; time.Sleep(200 * time.Millisecond) {
			if uuid, err = UUID(profileDir); err != nil {
				return err
			}
		}
		if uuid == "" {
			return fmt.Errorf("Firefox has not installed mirrormark in %s; restart Firefox there (close every window), then run mirrormark install --browser firefox again", profileDir)
		}
	}
	ok, err := chrome.Retry(bin, []string{"--profile", profileDir, "about:blank", "moz-extension://" + uuid + "/bind.html#" + dbID}, bound)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("profile %s did not confirm; check that mirrormark is enabled in about:addons there, then run mirrormark install --browser firefox again", profileDir)
}
```

- [ ] **Step 4: Run.** `bin/dev run make` → PASS.

- [ ] **Step 5: Commit** subject `feat(rfc-0001): bind a Firefox profile`.

---

### Task 9: `install --browser firefox`

**Files:**
- Modify: `cmd/mirrormark/install.go`, `cmd/mirrormark/main.go` (usage)
- Test: `cmd/mirrormark/install_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: Tasks 5–8.

- [ ] **Step 1: Write the failing tests**

```go
// firefoxData makes a profiles root with two Developer Edition profiles,
// alpha and beta, whose Firefox is a fake that logs its calls and, given a
// bind URL, touches the lock file of the database it names. channel is that
// Firefox's update channel.
func firefoxData(t *testing.T, p db.Paths, channel string) (root, calls string) {
	root = filepath.Join(os.Getenv("HOME"), ".config", "mozilla", "firefox")
	ff := t.TempDir()
	os.MkdirAll(filepath.Join(ff, "defaults", "pref"), 0o755)
	os.WriteFile(filepath.Join(ff, "defaults", "pref", "channel-prefs.js"), []byte(`pref("app.update.channel", "`+channel+`");`), 0o644)
	calls = filepath.Join(ff, "calls")
	os.WriteFile(filepath.Join(ff, "firefox"), []byte("#!/bin/sh\nfor a; do url=$a; done\necho \"$@\" >> "+calls+"\n"+
		"case \"$url\" in *bind.html*) touch -d \"@$(date +%s.%N)\" \""+p.ProfilesDir()+"/${url##*#}.lock\";; esac\n"), 0o755)
	ini := "[General]\nVersion=2\n"
	for i, n := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "x"+n+"."+n)
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "prefs.js"), nil, 0o600)
		os.WriteFile(filepath.Join(dir, "compatibility.ini"), []byte("[Compatibility]\nLastPlatformDir="+ff+"\n"), 0o644)
		ini += fmt.Sprintf("[Profile%d]\nName=%s\nIsRelative=1\nPath=x%s.%s\n", i, n, n, n)
	}
	os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644)
	old := chrome.BindWait
	chrome.BindWait = 500 * time.Millisecond
	t.Cleanup(func() { chrome.BindWait = old })
	return root, calls
}

func TestInstallIntoFirefoxThenBind(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, calls := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Restart Firefox") || !strings.Contains(out.String(), "unsigned") {
		t.Fatalf("output:\n%s", out.String())
	}
	for _, n := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "x"+n+"."+n)
		if _, err := os.Stat(firefox.XPIPath(dir)); err != nil {
			t.Fatal(err)
		}
		if user, _ := os.ReadFile(filepath.Join(dir, "user.js")); string(user) != strings.Join(firefox.Prefs, "\n")+"\n" {
			t.Fatalf("user.js %q", user)
		}
		d, err := db.LoadDB(p.ProfileFile("firefox-" + n))
		if err != nil || d.Profile.Path != dir || d.Profile.Browser != "firefox" {
			t.Fatalf("%s: %+v %v", n, d, err)
		}
	}
	host, _ := firefox.HostManifestPath()
	if data, err := os.ReadFile(host); err != nil || !bytes.Contains(data, []byte(extension.GeckoID)) {
		t.Fatalf("host manifest %s %v", data, err)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("bound before Firefox restarted")
	}

	// Firefox has installed the add-on and recorded its UUID.
	for _, n := range []string{"alpha", "beta"} {
		os.WriteFile(filepath.Join(root, "x"+n+"."+n, "prefs.js"), []byte(`user_pref("extensions.webextensions.uuids", "{\"mirrormark@chriscz.github.io\":\"5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4`+n[:1]+`\"}");`+"\n"), 0o600)
	}
	out.Reset()
	if code := run([]string{"install", "--browser", "firefox", "--profile", "beta"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "bound: beta -> firefox-beta\n") || strings.Contains(out.String(), "alpha") {
		t.Fatalf("output:\n%s", out.String())
	}
	if c, _ := os.ReadFile(calls); !bytes.Contains(c, []byte("about:blank moz-extension://5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b/bind.html#firefox-beta")) {
		t.Fatalf("calls:\n%s", c)
	}
}

func TestInstallRefusesReleaseFirefox(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "release")
	var out, errb bytes.Buffer
	if code := run([]string{"install", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "Developer Edition") || !strings.Contains(errb.String(), "release") {
		t.Fatalf("stderr %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "xalpha.alpha", "user.js")); err == nil {
		t.Fatal("wrote into a release profile")
	}
	if dbs, _ := db.LoadAll(p); len(dbs) != 0 {
		t.Fatalf("made databases %v", dbs)
	}
}
```

- [ ] **Step 2: Run.** `bin/dev run go test ./cmd/mirrormark -run 'Firefox'` → FAIL (`flag provided but not defined: -browser`).

- [ ] **Step 3: Implement.** In `cmdInstall`, add `browser := fl.String("browser", "chrome", "chrome or firefox")`, change the `--user-data-dir` help to `"Chrome's data directory (default ~/.config/google-chrome), or Firefox's profiles directory"`, and right after `refuseRoot` and `db.DefaultPaths()`:

```go
	switch *browser {
	case "chrome":
	case "firefox":
		return installFirefox(p, *udd, picks, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "mirrormark: --browser is chrome or firefox, not %q\n", *browser)
		return 2
	}
```

Add:

```go
const firefoxSafety = `Firefox installs only add-ons Mozilla signed unless a profile says
otherwise, and mirrormark is not signed. So install sets, in each profile's
user.js, xpinstall.signatures.required to false (that profile then accepts
any unsigned add-on, not only mirrormark) and extensions.autoDisableScopes
to 0 (an add-on file in the profile starts enabled without asking).
mirrormark uninstall --browser firefox sets them back.`

// installFirefox installs into Firefox Developer Edition profiles, as the
// user: Firefox reads add-ons and host manifests from the user's own
// directories, so no sudo. It refuses profiles of any other Firefox before
// writing anything, since those install only signed add-ons.
func installFirefox(p db.Paths, root string, picks []string, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	var err error
	if root == "" {
		root, err = firefox.DefaultRoot()
	} else {
		root, err = resolveDir(root)
	}
	if err != nil {
		return fail(err)
	}
	profiles, err := firefox.Profiles(root)
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
	var refused []string
	for _, c := range chosen {
		dir := firefox.Dir(root, c)
		bin, channel, err := firefox.Channel(dir)
		switch {
		case err != nil:
			refused = append(refused, fmt.Sprintf("%s: %v", c.Name, err))
		case channel != "aurora":
			refused = append(refused, fmt.Sprintf("%s: its Firefox is on the %s channel, which installs only add-ons Mozilla signed; mirrormark works only in Firefox Developer Edition for now", c.Name, channel))
		default:
			targets = append(targets, target{c, dir, bin})
		}
	}
	if len(refused) > 0 {
		return fail(fmt.Errorf("cannot install into these profiles; leave them out with --profile:\n  %s", strings.Join(refused, "\n  ")))
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
	ready := chrome.FilesCurrent(files)
	if !ready {
		fmt.Fprintln(stdout, firefoxSafety)
		if err := chrome.WriteFiles(files); err != nil {
			return fail(err)
		}
	}
	ids := map[string]string{}
	for _, tg := range targets {
		if ids[tg.dir], err = ensureDB(p, "firefox", tg.dir, tg.c); err != nil {
			return fail(err)
		}
	}
	if err := ensureConfig(p); err != nil {
		return fail(err)
	}
	if !ready {
		fmt.Fprintln(stdout, "Restart Firefox in these profiles if it is running (close every window), then run mirrormark install --browser firefox again to bind them.")
		return 0
	}
	fmt.Fprintln(stdout, "Opening a tab in each profile to bind it.")
	code := 0
	for _, tg := range targets {
		id := ids[tg.dir]
		bound := func(since time.Time) bool { return db.Connected(p, id, since) }
		if err := firefox.Bind(tg.bin, tg.dir, id, bound); err != nil {
			fmt.Fprintf(stdout, "not bound: %s: %v\n", tg.c.Name, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "bound: %s -> %s\n", tg.c.Name, id)
	}
	if code == 0 {
		fmt.Fprintf(stdout, "Now list the folders to share in %s.\nExport your bookmarks from Firefox first (Bookmarks, Manage bookmarks, Import and Backup), so you have a copy.\nThe host logs to %s.\n", p.ConfigFile(), p.LogFile())
	}
	return code
}
```

`WriteFiles` makes `extensions/` with mode 0755 inside the profile, which stays 0700. Firefox prints its profiles by name, so the `bound:` line names the profile, not its directory. Update `usageText`: `install … [--browser chrome|firefox] [--user-data-dir DIR] [--profile DIR_OR_NAME]...`, "Install into Google Chrome or Firefox Developer Edition and bind its profiles".

- [ ] **Step 4: README.** Add `## Install (Firefox Developer Edition on Linux)` after the Chrome section, without repeating what the Chrome section says about ids and `config.yaml` (link to it):
  1. Release Firefox can't run mirrormark yet (it needs a Mozilla-signed add-on); only Developer Edition. **Before you start:** the safety cost, in the words of `firefoxSafety`, and that `uninstall` undoes it.
  2. Run `mirrormark install --browser firefox` as yourself (no `sudo`). It binds every profile in `~/.config/mozilla/firefox` or `~/.mozilla/firefox`; `--profile NAME` and `--user-data-dir DIR` narrow it. It refuses profiles of release Firefox.
  3. Restart Firefox in those profiles if it is running; run `install --browser firefox` again. It starts Firefox in a profile that isn't running, opens a tab and prints `bound: <profile> -> firefox-<name>`.
  4. Paths may start with `menu` in Firefox profiles. Separators stay where you put them and are never synced.
  Add the Firefox files to the Files table (`<profile>/extensions/mirrormark@chriscz.github.io.xpi`, the three `user.js` lines, `~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json`) and change the status line to "Google Chrome and Firefox Developer Edition on Linux".

- [ ] **Step 5: Run.** `bin/dev run make` → PASS.

- [ ] **Step 6: Commit** subject `feat(rfc-0001): install into Firefox Developer Edition [new feature]`.

---

### Task 10: `uninstall --browser firefox`

**Files:**
- Modify: `cmd/mirrormark/install.go`, `README.md`
- Test: `cmd/mirrormark/install_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestUninstallFromFirefoxRestoresItsChecks(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := firefoxData(t, p, "aurora")
	var out, errb bytes.Buffer
	run([]string{"install", "--browser", "firefox"}, strings.NewReader(""), &out, &errb)
	dir := filepath.Join(root, "xalpha.alpha")
	user, _ := os.ReadFile(filepath.Join(dir, "user.js"))
	os.WriteFile(filepath.Join(dir, "user.js"), append([]byte("user_pref(\"browser.startup.page\", 3);\n"), user...), 0o644)

	out.Reset()
	if code := run([]string{"uninstall", "--browser", "firefox"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if _, err := os.Stat(firefox.XPIPath(dir)); err == nil {
		t.Fatal("XPI left")
	}
	want := "user_pref(\"browser.startup.page\", 3);\n" + firefox.ResetPrefs[firefox.Prefs[0]] + "\n" + firefox.ResetPrefs[firefox.Prefs[1]] + "\n" + firefox.Prefs[2] + "\n"
	if got, _ := os.ReadFile(filepath.Join(dir, "user.js")); string(got) != want {
		t.Fatalf("user.js %q", got)
	}
	host, _ := firefox.HostManifestPath()
	if _, err := os.Stat(host); err == nil {
		t.Fatal("host manifest left")
	}
	if _, err := os.Stat(p.ProfileFile("firefox-alpha")); err != nil {
		t.Fatal("database removed without --purge")
	}
}
```

- [ ] **Step 2: Run.** → FAIL (`-browser` not defined for uninstall).

- [ ] **Step 3: Implement.** `cmdUninstall` gets the same `--browser` flag. For `firefox` it calls `uninstallFirefox(p, stdout)` instead of the Chrome block, then the shared purge/keep code (move it into `finishUninstall(p, purge, stdout)` used by both):

```go
// uninstallFirefox removes the add-on from every profile a database file
// names as Firefox, puts back the two prefs that weakened its add-on
// checks, and removes the host manifest if it is this one's.
func uninstallFirefox(p db.Paths, stdout io.Writer) error {
	dbs, errs := db.LoadAll(p)
	for _, err := range errs {
		return fmt.Errorf("%w; fix it so uninstall can tell whether it is a Firefox profile", err)
	}
	var files []chrome.File
	var gone []string // XPIs, and the host manifest if it is this one's
	for _, d := range dbs {
		if d.Profile.Browser != "firefox" {
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
		var b strings.Builder
		for line := range strings.Lines(string(user)) {
			if r, ok := firefox.ResetPrefs[strings.TrimSpace(line)]; ok {
				line = r + "\n"
			}
			b.WriteString(line)
		}
		files = append(files, chrome.File{Path: path, Data: []byte(b.String())})
	}
	if host, err := firefox.HostManifestPath(); err != nil {
		return err
	} else if data, err := os.ReadFile(host); err == nil && bytes.Contains(data, []byte(`"`+extension.GeckoID+`"`)) {
		gone = append(gone, host)
	}
	if err := chrome.RemoveFiles(gone); err != nil {
		return err
	}
	if err := chrome.WriteFiles(files); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Firefox removes mirrormark from each profile when it next starts, and checks add-on signatures there again. Your bookmarks stay in Firefox.")
	return nil
}
```

README: under Upgrade, uninstall, unpack, one paragraph on `uninstall --browser firefox`: what it removes, that `startupScanScopes` stays at 1 and why.

- [ ] **Step 4: Run.** `bin/dev run make` → PASS.

- [ ] **Step 5: Commit** subject `feat(rfc-0001): uninstall from Firefox and restore its signature checks`. If Task 7 Step 4 found other defaults, also commit `docs(rfc-0001): …` correcting Section 8.

---

### Task 11: Firefox Developer Edition and D-Bus in the dev image

**Files:**
- Modify: `Dockerfile`, `Makefile`, `README.md` (Development)

- [ ] **Step 1: Dockerfile**, after the Chrome block:

```dockerfile
# Firefox Developer Edition for the Firefox end-to-end tests: release
# Firefox refuses unsigned add-ons. D-Bus carries a second firefox
# invocation's URL to the running one, which binding a running profile
# needs. Mozilla keeps old releases on its CDN; bump version and checksum
# together.
ARG FIREFOX_DEV_VERSION=159.0b2
ARG FIREFOX_DEV_SHA256=710dfa80e9edcad6c31f5aa0b3dffb4164b14a60be09316621de3894d8944b40
RUN apt-get update \
 && apt-get install -y --no-install-recommends xz-utils libgtk-3-0t64 libasound2t64 \
      libx11-xcb1 libdbus-glib-1-2 libxtst6 libpci3 dbus dbus-x11 \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSLo /tmp/ff.tar.xz "https://download-installer.cdn.mozilla.net/pub/devedition/releases/${FIREFOX_DEV_VERSION}/linux-x86_64/en-US/firefox-${FIREFOX_DEV_VERSION}.tar.xz" \
 && echo "${FIREFOX_DEV_SHA256}  /tmp/ff.tar.xz" | sha256sum -c - \
 && mkdir -p /opt/firefox-devedition \
 && tar -C /opt/firefox-devedition --strip-components=1 -xJf /tmp/ff.tar.xz \
 && rm /tmp/ff.tar.xz
```

- [ ] **Step 2: Makefile.** `check-e2e` runs under a D-Bus session:

```make
# Not part of check: needs Chrome, Firefox Developer Edition, Xvfb and
# D-Bus from the dev image and takes minutes. …(as now)
check-e2e: build
	MIRRORMARK_BIN=$(abspath $(BIN)) dbus-run-session -- xvfb-run -a -s "-screen 0 1280x800x24" \
		$(GO) test -tags e2e -count=1 -timeout 60m -v ./e2e/ $(if $(RUN),-run '$(RUN)')
```

- [ ] **Step 3: Verify.**
Run: `bin/dev image build && bin/dev run /opt/firefox-devedition/firefox --version`
Expected: `Mozilla Firefox 159.0b2`.
Run: `bin/dev run make check-e2e`
Expected: every Chrome e2e test passes under `dbus-run-session`.
Then do Task 7 Step 4's check of the defaults.

- [ ] **Step 4:** README Development: the e2e tests also start Firefox Developer Edition; `bin/dev image build` after this change.

- [ ] **Step 5: Commit** subject `build(rfc-0001): add Firefox Developer Edition and D-Bus to the dev image`.

---

### Task 12: Put the e2e browser behind a driver

A refactor of `e2e/` with no change in what the tests do. Chrome e2e must pass unchanged.

**Files:**
- Modify: `e2e/browser.go`, `e2e/env.go`, `e2e/install_test.go`, `e2e/sync_test.go`

**Interfaces:**
- Produces: `driver`, `chromeDriver`, `Browser.Kind`, `Browser.open(t, url)`, `Env.open(t, b)`, `Env.kind(name)`.

- [ ] **Step 1: Driver interface and Chrome driver** in `browser.go`:

```go
// driver is how the tests reach one browser: Chrome over the DevTools
// pipe, Firefox over WebDriver BiDi.
type driver interface {
	// eval runs expr, a JavaScript expression that may be a promise, in
	// the page openPage loaded, and returns its value as JSON.
	eval(expr string) (json.RawMessage, error)
	// openPage loads url, an extension page, and makes it eval's page.
	openPage(url string) error
	// close asks the browser to quit as a user's quit would, so that it
	// saves its files.
	close()
}

type Browser struct {
	Name string
	Kind string // "chrome" or "firefox"
	UDD  string // Chrome's data directory, or Firefox's profile directory
	cmd  *exec.Cmd
	drv  driver
	done chan struct{}
}

type chromeDriver struct {
	c       *cdp
	session string // an extension page; chrome.* calls run there
}

func (d *chromeDriver) eval(expr string) (json.RawMessage, error) {
	res, err := call[evalParams, evalResult](d.c, d.session, "Runtime.evaluate",
		evalParams{Expression: expr, AwaitPromise: true, ReturnByValue: true})
	if err != nil {
		return nil, err
	}
	if x := res.ExceptionDetails; x != nil {
		return nil, fmt.Errorf("%s %s", x.Text, x.Exception.Description)
	}
	return res.Result.Value, nil
}

func (d *chromeDriver) openPage(url string) error {
	tg, err := call[createParams, target](d.c, "", "Target.createTarget", createParams{URL: url})
	if err != nil {
		return err
	}
	at, err := call[attachParams, struct {
		SessionID string `json:"sessionId"`
	}](d.c, "", "Target.attachToTarget", attachParams{TargetID: tg.TargetID, Flatten: true})
	if err != nil {
		return err
	}
	d.session = at.SessionID
	return nil
}

func (d *chromeDriver) close() { call[none, none](d.c, "", "Browser.close", none{}) }
```

`createParams` and `attachParams` move to package level. In `launch`, keep a local `c := newCDP(inW, outR)`, build `&Browser{Name: name, Kind: "chrome", UDD: udd, cmd: cmd, drv: &chromeDriver{c: c}, done: make(chan struct{})}`, and check `Browser.getVersion` on `c` as now. `Close` calls `b.drv.close()` where it called `Browser.close`. Delete the unused `targets` method.

- [ ] **Step 2: Generic `open` and `evalJS`**

```go
// open loads the extension page url in a tab and makes it the page eval
// runs in. It retries because on a profile's first start the extension
// installs after the first tab has loaded.
func (b *Browser) open(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := b.drv.openPage(url); err != nil {
			t.Fatal(err)
		}
		for range 20 {
			if s, err := evalJS[string](b, "typeof chrome.bookmarks"); err == nil && s == "object" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Fatalf("%s: the extension page never loaded", b.Name)
}

func evalJS[R any](b *Browser, expr string) (R, error) {
	var out R
	raw, err := b.drv.eval("(async () => {" + prelude + "return await (" + expr + ");})()")
	if err != nil {
		return out, fmt.Errorf("%s: %w", expr, err)
	}
	if len(raw) > 0 {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}
```

- [ ] **Step 3: Env.** In `env.go` add `kinds map[string]string` to `Env`, `func (e *Env) kind(name string) string` returning `"chrome"` when unset, and:

```go
// open loads the popup page of b's extension and attaches the tests to it.
func (e *Env) open(t *testing.T, b *Browser) {
	t.Helper()
	b.open(t, "chrome-extension://"+e.ExtID+"/popup.html")
}
```

Replace every `X.open(t, e.ExtID)` with `e.open(t, X)` in `env.go`, `install_test.go` and `sync_test.go`.

- [ ] **Step 4: Verify.** `bin/dev run make && bin/dev run make check-e2e` → PASS, same tests, same latencies logged.

- [ ] **Step 5: Commit** subject `refactor(rfc-0001): put the e2e browser behind a driver`.

---

### Task 13: Install and bind Firefox profiles end to end

**Files:**
- Create: `e2e/bidi.go`
- Modify: `e2e/env.go`, `e2e/browser.go` (prelude), `e2e/install_test.go`

**Interfaces:**
- Consumes: `firefox.UUID`, `firefox.XPIPath`, `extension.GeckoID`, `extension.Version`.
- Produces: `setupPair`, `launchFirefox`, `firefoxDriver`, `Env.ShareAt`, `Browser.AddSeparator`, `Browser.Separators`.

- [ ] **Step 1: BiDi client** `e2e/bidi.go`, adapted from the spike's `bidi/main.go` with typed messages:

```go
//go:build e2e

package e2e

// firefoxBin is Firefox Developer Edition in the dev image.
const firefoxBin = "/opt/firefox-devedition/firefox"

// bidi speaks WebDriver BiDi to Firefox over its WebSocket on
// 127.0.0.1:<port>/session: masked text frames to Firefox, unmasked and
// possibly fragmented frames back. One command at a time; events are
// skipped, as the tests need none.
type bidi struct {
	conn net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
	next int
}

func dialBiDi(port int, within time.Duration) (*bidi, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(within)
	for {
		b, err := handshake(addr)
		if err == nil {
			return b, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("BiDi on %s: %w", addr, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func handshake(addr string) (*bidi, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	rand.Read(key)
	fmt.Fprintf(c, "GET /session HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		addr, base64.StdEncoding.EncodeToString(key))
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err == nil && resp.StatusCode != http.StatusSwitchingProtocols {
		err = fmt.Errorf("handshake: %s", resp.Status)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return &bidi{conn: c, r: r}, nil
}

func (b *bidi) send(p []byte) error {
	h := []byte{0x81} // final text frame
	switch n := len(p); {
	case n < 126:
		h = append(h, 0x80|byte(n))
	case n < 1<<16:
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	default:
		h = binary.BigEndian.AppendUint64(append(h, 0x80|127), uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	h = append(h, mask...)
	for i, c := range p {
		h = append(h, c^mask[i%4])
	}
	_, err := b.conn.Write(h)
	return err
}

func (b *bidi) recv() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(b.r, h[:]); err != nil {
			return nil, err
		}
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var l [2]byte
			if _, err := io.ReadFull(b.r, l[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(l[:]))
		case 127:
			var l [8]byte
			if _, err := io.ReadFull(b.r, l[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(l[:])
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(b.r, p); err != nil {
			return nil, err
		}
		msg = append(msg, p...)
		if h[0]&0x80 != 0 {
			return msg, nil
		}
	}
}

type bidiReply struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

func bidiCall[P, R any](b *bidi, method string, params P) (R, error) {
	var out R
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	msg, err := json.Marshal(struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params P      `json:"params"`
	}{b.next, method, params})
	if err != nil {
		return out, err
	}
	b.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := b.send(msg); err != nil {
		return out, fmt.Errorf("%s: %w", method, err)
	}
	for {
		m, err := b.recv()
		if err != nil {
			return out, fmt.Errorf("%s: %w", method, err)
		}
		var r bidiReply
		if json.Unmarshal(m, &r) != nil || r.ID != b.next {
			continue // an event
		}
		if r.Type == "error" {
			return out, fmt.Errorf("%s: %s: %s", method, r.Error, r.Message)
		}
		return out, json.Unmarshal(r.Result, &out)
	}
}

type firefoxDriver struct {
	c   *bidi
	ctx string // the tab the tests use
}

type bidiTarget struct {
	Context string `json:"context"`
}

// eval returns the expression's value through JSON.stringify: BiDi hands
// back typed remote values, not JSON.
func (d *firefoxDriver) eval(expr string) (json.RawMessage, error) {
	type params struct {
		Expression   string     `json:"expression"`
		Target       bidiTarget `json:"target"`
		AwaitPromise bool       `json:"awaitPromise"`
	}
	r, err := bidiCall[params, struct {
		Type   string `json:"type"`
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}](d.c, "script.evaluate", params{"(async () => JSON.stringify(await " + expr + "))()", bidiTarget{d.ctx}, true})
	if err != nil {
		return nil, err
	}
	if r.Type == "exception" {
		return nil, errors.New(r.ExceptionDetails.Text)
	}
	if r.Result.Type != "string" {
		return nil, nil // undefined
	}
	var s string
	err = json.Unmarshal(r.Result.Value, &s)
	return json.RawMessage(s), err
}

func (d *firefoxDriver) openPage(url string) error {
	type params struct {
		Context string `json:"context"`
		URL     string `json:"url"`
		Wait    string `json:"wait"`
	}
	_, err := bidiCall[params, json.RawMessage](d.c, "browsingContext.navigate", params{d.ctx, url, "complete"})
	return err
}

func (d *firefoxDriver) close() { bidiCall[none, json.RawMessage](d.c, "browser.close", none{}) }

// launchFirefox starts Firefox Developer Edition in profile dir with BiDi
// on port, under the test's Xvfb and D-Bus, not headless: a headless
// Firefox does not take a second invocation's URL, which install's binding
// of a running profile needs.
func launchFirefox(t *testing.T, name, dir string, port int, env []string) *Browser {
	t.Helper()
	logf, err := os.OpenFile(dir+".log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(firefoxBin, "--profile", dir, "--remote-debugging-port", strconv.Itoa(port), "about:blank")
	cmd.Env, cmd.Stdout, cmd.Stderr = env, logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	b := &Browser{Name: name, Kind: "firefox", UDD: dir, cmd: cmd, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		logf.Close()
		close(b.done)
	}()
	c, err := dialBiDi(port, 30*time.Second)
	if err != nil {
		cmd.Process.Kill()
		t.Fatalf("%s: %v", name, err)
	}
	type caps struct {
		Capabilities struct{} `json:"capabilities"`
	}
	if _, err := bidiCall[caps, json.RawMessage](c, "session.new", caps{}); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	type treeParams struct {
		MaxDepth int `json:"maxDepth"`
	}
	tree, err := bidiCall[treeParams, struct {
		Contexts []bidiTarget `json:"contexts"`
	}](c, "browsingContext.getTree", treeParams{0})
	if err != nil || len(tree.Contexts) == 0 {
		t.Fatalf("%s: no tab: %v", name, err)
	}
	b.drv = &firefoxDriver{c: c, ctx: tree.Contexts[0].Context}
	t.Cleanup(b.Close)
	return b
}
```

The `Browser.Close` from Task 12 works unchanged (`drv.close`, wait for `done`, kill after 20 s).

- [ ] **Step 2: Prelude.** In `browser.go`'s `prelude`, find Firefox's roots by id and skip separators:

```js
  roots: { toolbar: ["bookmarks-bar", "toolbar_____"], other: ["other", "unfiled_____"], menu: [null, "menu________"] },
  kids: (n) => (n.children ?? []).filter((c) => c.type !== "separator"),
```

`one` and `lines` use `mm.kids(parent)` instead of `parent.children ?? []`, and `folder` picks the root with `(c) => (c.folderType ? c.folderType === mm.roots[root][0] : c.id === mm.roots[root][1])`. Add:

```go
// AddSeparator puts a separator at index in the folder at path (Firefox).
func (b *Browser) AddSeparator(t *testing.T, path string, index int) {
	t.Helper()
	eval[string](t, b, fmt.Sprintf("mm.folder(%s).then((f) => chrome.bookmarks.create({parentId: f.id, type: 'separator', index: %d})).then(() => '')", q(path), index))
}

// Separators lists the positions of the separators directly in the folder
// at path.
func (b *Browser) Separators(t *testing.T, path string) []int {
	t.Helper()
	return eval[[]int](t, b, "mm.folder("+q(path)+").then((f) => f.children.flatMap((c, i) => c.type === 'separator' ? [i] : []))")
}
```

- [ ] **Step 3: Env for both browsers.** In `env.go`:

```go
// setup is two Chrome instances.
func setup(t *testing.T) *Env { return setupPair(t, "chrome", "chrome") }

// setupPair installs and binds profile a in browser kind ka and b in kb,
// "chrome" or "firefox". Two Firefox profiles share one profiles directory,
// as a user's would, and run as two Firefox processes.
func setupPair(t *testing.T, ka, kb string) *Env {
	e := newEnv(t)
	e.kinds = map[string]string{"a": ka, "b": kb}
	e.firstInstall(t)
	e.A, e.B = e.launch("a"), e.launch("b")
	e.DBA, e.DBB = e.bind("a"), e.bind("b")
	e.open(t, e.A)
	e.open(t, e.B)
	e.LabelA, e.LabelB = e.label(e.DBA), e.label(e.DBB)
	return e
}

func (e *Env) udd(name string) string {
	if e.kind(name) == "firefox" {
		return filepath.Join(e.Home, "firefox", name)
	}
	return filepath.Join(e.Home, "chrome-"+name)
}

func (e *Env) launch(name string) *Browser {
	if e.kind(name) == "firefox" {
		return launchFirefox(e.t, name, e.udd(name), map[string]int{"a": 9301, "b": 9302}[name], e.env)
	}
	return launch(e.t, name, e.udd(name), e.env)
}

func (e *Env) installArgs(name string) []string {
	if e.kind(name) == "firefox" {
		return []string{"install", "--browser", "firefox", "--profile", name}
	}
	return []string{"install", "--user-data-dir", e.udd(name)}
}

// prepare makes the profile exist as a user's would after using the
// browser once: Chrome's data directory with its profile list, or a Firefox
// profile in profiles.ini with its compatibility.ini.
func (e *Env) prepare(t *testing.T, name string) {
	t.Helper()
	if e.kind(name) == "firefox" {
		dir := e.udd(name)
		cmd := exec.Command(firefoxBin, "--headless", "-CreateProfile", name+" "+dir)
		cmd.Env = e.env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create Firefox profile %s: %v\n%s", name, err, out)
		}
		// The remote agent sets "recommended" prefs for automation when a
		// session starts; they could hide whether install's prefs work.
		if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte("user_pref(\"remote.prefs.recommended\", false);\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.launch(name).Close()
}
```

`firstInstall` becomes:

```go
func (e *Env) firstInstall(t *testing.T) {
	t.Helper()
	e.prepare(t, "a")
	e.prepare(t, "b")
	if e.kind("a") == "chrome" || e.kind("b") == "chrome" {
		// (the existing comment) later tests must not see this test's
		// extension in the shared system directories.
		t.Cleanup(func() { e.mm(t, "uninstall") })
	}
	chromeDone := false
	for _, name := range []string{"a", "b"} {
		if e.kind(name) == "chrome" {
			if chromeDone {
				continue // one install writes the system files for every data directory
			}
			chromeDone = true
		}
		out := e.mm(t, e.installArgs(name)...)
		if !strings.Contains(out, "Restart ") {
			t.Fatalf("first install did not ask for a restart:\n%s", out)
		}
		if m := extIDRe.FindStringSubmatch(out); m != nil {
			e.ExtID = m[1]
		}
	}
}
```

`bind(name)` runs `e.mm(e.t, e.installArgs(name)...)` and matches `bound: <Default or name> -> ([a-z0-9-]+)` (`"Default"` for Chrome, the profile's name for Firefox). `Env.open` gains the Firefox branch:

```go
	if b.Kind == "firefox" {
		var uuid string
		eventually(t, 15*time.Second, b.Name+"'s add-on UUID in prefs.js", func() bool {
			uuid, _ = firefox.UUID(b.UDD)
			return uuid != ""
		})
		b.open(t, "moz-extension://"+uuid+"/popup.html")
		return
	}
```

Add `ShareAt(t, path)`, which `Share` calls with `"toolbar/Work"`.

- [ ] **Step 4: Write the tests** in `install_test.go`:

```go
func TestFirefoxInstallAndBind(t *testing.T) {
	e := setupPair(t, "firefox", "firefox")
	if e.DBA != "firefox-a" || e.DBB != "firefox-b" {
		t.Fatalf("bound to %s and %s", e.DBA, e.DBB)
	}
	for _, c := range []struct {
		b  *Browser
		id string
	}{{e.A, e.DBA}, {e.B, e.DBB}} {
		if got := eval[string](t, c.b, `chrome.runtime.id`); got != extension.GeckoID {
			t.Errorf("%s: extension id %q", c.b.Name, got)
		}
		if got := eval[string](t, c.b, `chrome.storage.local.get("db").then((r) => r.db)`); got != c.id {
			t.Errorf("%s is bound to %q, want %q", c.b.Name, got, c.id)
		}
		d, err := db.LoadDB(e.Paths.ProfileFile(c.id))
		if err != nil || d.Profile.Path != c.b.UDD || d.Profile.Browser != "firefox" {
			t.Errorf("%s database %+v, %v", c.id, d, err)
		}
		roundDone(t, c.b, c.b.Name+" round after binding")
	}
	if !strings.Contains(hostLog(t, e), "browser=firefox/159.0") {
		t.Error("no hello from firefox/159.0 in the host log")
	}

	// Binding a running profile again goes through D-Bus to the running
	// Firefox, and the extension moves to a new host.
	old := hostPIDs(t, e, e.DBA)
	if out := e.mm(t, "install", "--browser", "firefox", "--profile", "a"); !strings.Contains(out, "bound: a -> "+e.DBA) {
		t.Fatalf("second install:\n%s", out)
	}
	eventually(t, 10*time.Second, "a new host for a", func() bool { return len(hostPIDs(t, e, e.DBA)) == len(old)+1 })
	eventually(t, 10*time.Second, "the old host to exit", func() bool { return exited(old[len(old)-1]) })

	// A restart keeps the binding: the event page starts with the profile.
	before := time.Now()
	e.A.Close()
	e.A = e.launch("a")
	eventually(t, 15*time.Second, "a hello after restart", func() bool { return db.Connected(e.Paths, e.DBA, before) })
	if log := hostLog(t, e); strings.Contains(log, "level=WARN") || strings.Contains(log, "level=ERROR") {
		t.Errorf("the host warned:\n%s", log)
	}
}

// TestFirefoxBindStartsFirefox: install starts a profile's Firefox itself
// when it is closed, waits for the UUID and binds it.
func TestFirefoxBindStartsFirefox(t *testing.T) {
	e := newEnv(t)
	e.kinds = map[string]string{"a": "firefox", "b": "firefox"}
	e.firstInstall(t)
	t.Cleanup(func() { stopFirefox(t, e.udd("a")) })
	id := e.bind("a")
	stopFirefox(t, e.udd("a"))
	e.A = e.launch("a")
	e.open(t, e.A)
	if got := eval[string](t, e.A, `chrome.storage.local.get("db").then((r) => r.db)`); got != id {
		t.Errorf("a is bound to %q, want %q", got, id)
	}
}

// TestFirefoxPicksUpANewXPI: a profile running an older build gets the new
// one after install replaces the XPI and Firefox restarts.
func TestFirefoxPicksUpANewXPI(t *testing.T) {
	e := newEnv(t)
	e.kinds = map[string]string{"a": "firefox"}
	e.prepare(t, "a")
	e.mm(t, e.installArgs("a")...)
	setXPIVersion(t, firefox.XPIPath(e.udd("a")), "0.0.1")
	e.A = e.launch("a")
	e.open(t, e.A)
	if v := eval[string](t, e.A, `chrome.runtime.getManifest().version`); v != "0.0.1" {
		t.Fatalf("old build not installed: %s", v)
	}
	e.A.Close()
	if out := e.mm(t, e.installArgs("a")...); !strings.Contains(out, "Restart Firefox") {
		t.Fatalf("install did not replace the XPI:\n%s", out)
	}
	e.A = e.launch("a")
	e.open(t, e.A)
	want, _ := extension.Version()
	if v := eval[string](t, e.A, `chrome.runtime.getManifest().version`); v != want {
		t.Fatalf("version %s after upgrade, want %s", v, want)
	}
}
```

`stopFirefox` follows `stopChrome`: the pid is in `<dir>/lock`, a symlink to `<ip>:+<pid>`, so `strconv.Atoi(target[strings.LastIndexByte(target, '+')+1:])`, SIGTERM, wait with `exited`. `setXPIVersion` rewrites `manifest.json` inside the zip with `archive/zip`, decoding it into `map[string]json.RawMessage` and setting `"version"`.

- [ ] **Step 5: Run.**
`bin/dev run make check-e2e RUN='TestFirefox'` → PASS. If binding a running profile fails because the BiDi agent and D-Bus remoting do not mix, record it in the spike doc's "Not verified" list as checked and failing, and change `TestFirefoxInstallAndBind` to close a profile before re-binding; then update Section 8 of the RFC in a `docs(rfc-0001)` commit (binding a running profile is then unverified in e2e, and the manual check covers it).
If `TestFirefoxPicksUpANewXPI` fails, the upgrade path needs a fix in `install` (for example, also bumping a pref Firefox watches); stop and report, as it changes the RFC.
Then `bin/dev run make check-e2e` → every Chrome test still passes.

- [ ] **Step 6: Commit** subject `test(rfc-0001): install and bind Firefox profiles end to end`.

---

### Task 14: Run the sync tests between Firefox and Chrome

**Files:**
- Modify: `e2e/sync_test.go`

- [ ] **Step 1: Pairs.** Add:

```go
// pairs are the browsers of profiles a and b each sync test runs with.
var pairs = [][2]string{{"chrome", "chrome"}, {"firefox", "firefox"}, {"firefox", "chrome"}}

// forPairs runs test once per pair, as a subtest named after it.
func forPairs(t *testing.T, test func(t *testing.T, ka, kb string)) {
	for _, p := range pairs {
		t.Run(p[0]+"-"+p[1], func(t *testing.T) { test(t, p[0], p[1]) })
	}
}
```

`synced(t)` becomes `synced(t, ka, kb)` calling `setupPair`. Wrap the bodies of `TestEditsPropagate`, `TestDeleteRestoreAndPurge`, `TestConflictingEditsMakeSuffixedCopies`, `TestFirstSyncUnionsExistingBookmarks` and `TestClosedBrowserCatchesUpOnStart` in `forPairs(t, func(t *testing.T, ka, kb string) { … })`, using `synced(t, ka, kb)` or `setupPair(t, ka, kb)`. `TestLargeFolderDelete` stays Chrome only. `e.launch` and `e.open` already pick the browser.

- [ ] **Step 2: Run.** `bin/dev run make check-e2e` → PASS for all three pairs, with each latency under 3 s logged. The suite takes about three times as long as before; the Makefile's 60 m timeout covers it.

- [ ] **Step 3: Commit** subject `test(rfc-0001): run the sync tests between Firefox and Chrome`.

---

### Task 15: Separators, `menu`, uninstall and URLs in Firefox

**Files:**
- Create: `e2e/firefox_test.go`
- Maybe modify: `TASKS.md`, the RFC (Step 5)

- [ ] **Step 1: Write the tests**

```go
//go:build e2e

package e2e

// TestFirefoxSeparatorsStayPut: separators never reach Chrome, new
// bookmarks land where the host means despite them, and a purged folder
// that holds one goes.
func TestFirefoxSeparatorsStayPut(t *testing.T) {
	e := setupPair(t, "firefox", "chrome")
	e.Union(t)
	e.A.Add(t, work, "GitHub", gh)
	e.A.AddSeparator(t, work, 0)
	waitLines(t, e.B, work, []string{"/GitHub " + gh}, syncTimeout)
	e.B.Add(t, work, "News", lwn)
	waitLines(t, e.A, work, []string{"/GitHub " + gh, "/News " + lwn}, syncTimeout)
	if got := e.A.Separators(t, work); !slices.Equal(got, []int{0}) {
		t.Fatalf("separators at %v, want [0]", got)
	}

	e.A.Mkdir(t, work+"/Old")
	e.A.Add(t, work+"/Old", "Go", "https://go.dev/")
	e.A.AddSeparator(t, work+"/Old", -1)
	both(t, e, "/GitHub "+gh, "/News "+lwn, "/Old/", "/Old/Go https://go.dev/")
	e.B.Remove(t, work+"/Old/")
	both(t, e, "/GitHub "+gh, "/News "+lwn, "/_deleted/", "/_deleted/Old/", "/_deleted/Old/Go https://go.dev/")
	e.B.Remove(t, work+"/_deleted/Old/") // a purge: A must remove a folder holding a separator
	settled(t, e, nil, "/GitHub "+gh, "/News "+lwn, "/_deleted/")
}

// TestFirefoxMenuUnion shares a folder under Firefox's Bookmarks Menu.
func TestFirefoxMenuUnion(t *testing.T) {
	e := setupPair(t, "firefox", "firefox")
	e.ShareAt(t, "menu/Work")
	e.A.Mkdir(t, "menu/Work")
	e.A.Add(t, "menu/Work", "GitHub", gh)
	waitLines(t, e.B, "menu/Work", []string{"/GitHub " + gh}, syncTimeout)
}

// TestFirefoxUninstall: the add-on goes and the profile checks signatures
// again.
func TestFirefoxUninstall(t *testing.T) {
	e := setupPair(t, "firefox", "firefox")
	e.A.Close()
	e.B.Close()
	e.mm(t, "uninstall", "--browser", "firefox")
	e.A = e.launch("a")
	e.A.Close() // Firefox records the add-on's removal by the time it exits
	data, err := os.ReadFile(filepath.Join(e.udd("a"), "extensions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var x struct {
		Addons []struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		} `json:"addons"`
	}
	json.Unmarshal(data, &x)
	for _, a := range x.Addons {
		if a.ID == extension.GeckoID && a.Active {
			t.Fatal("mirrormark is still active")
		}
	}
	if prefs, _ := os.ReadFile(filepath.Join(e.udd("a"), "prefs.js")); bytes.Contains(prefs, []byte(`"xpinstall.signatures.required", false`)) {
		t.Fatal("prefs.js still turns signature checks off")
	}
}
```

- [ ] **Step 2: Run.** `bin/dev run make check-e2e RUN='TestFirefox'` → PASS. If `TestFirefoxUninstall` fails because the add-on stays active, the uninstall design is wrong (Firefox may need `startupScanScopes` or the XPI removal to happen differently); stop and report, as it changes Section 8.

- [ ] **Step 3: Probe URLs.** Write, run once, and keep as a test whatever holds:

```go
// TestURLsCrossBrowsers adds bookmarks with unusual schemes in Chrome and
// checks they reach Firefox unchanged.
func TestURLsCrossBrowsers(t *testing.T) {
	e := setupPair(t, "firefox", "chrome")
	e.Union(t)
	urls := []string{"javascript:void(0)", "data:text/plain,hi", "file:///etc/hosts", "chrome://settings/", "about:blank", "https://example.com/a%20b?q=1#f"}
	var want []string
	for i, u := range urls {
		e.B.Add(t, work, fmt.Sprint(i), u)
		want = append(want, fmt.Sprintf("/%d %s", i, u))
	}
	settled(t, e, nil, want...)
}
```

`mm.node` treats a bookmark line's URL by `"://"`, so `Lines` is all this needs. If every URL arrives, keep the test. If Firefox refuses or rewrites some, the profile stalls with `apply-failed`: do not work around it here. Remove those URLs from the test, record the finding (which schemes, the error text, the effect: the Firefox member never completes a round while one is in the union) in `TASKS.md` and in the RFC's risk "URLs Firefox refuses" in a `docs(rfc-0001)` commit, and report it to the user, who decides the fix (for example, the host keeping such bookmarks out of a Firefox member).

- [ ] **Step 4: Full run.** `bin/dev run make && bin/dev run make check-e2e` → PASS.

- [ ] **Step 5: Commit** subject `test(rfc-0001): cover separators, menu, uninstall and URLs in Firefox`, plus any `docs(rfc-0001)` commit from Steps 2–3.

## After the last task

- Update the spike doc's "Not verified" list with what Tasks 13 and 15 checked, in a `docs(rfc-0001)` commit.
- Add to `TASKS.md` under "Manual checks still to do": run the README's Firefox steps on a desktop with Firefox Developer Edition, with Firefox open during the first `install`, and check binding a running profile over the desktop's D-Bus.
- Phase 5 of the RFC is done when that manual check is recorded.

## Risks

- **BiDi and remoting in one Firefox** were not tried together. Task 13 says what to do if they don't mix.
- **`remote.prefs.recommended`** may not cover every pref the remote agent sets; if the e2e tests pass only because the agent turned signature checks off, Task 13's install test would not show it. `TestFirefoxPicksUpANewXPI` and a manual run are the cross-checks.
- **Test time** roughly triples (three pairs). Run single tests with `RUN=` while developing.
- **Upgrade and uninstall** depend on Firefox rescanning the profile's `extensions` folder; Tasks 13 and 15 check both and stop the stack if they fail.
- **Unknown URL behaviour** across browsers may need a host change; Task 15 stops for the user rather than guessing.
