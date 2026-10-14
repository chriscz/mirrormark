# mirrormark

mirrormark keeps chosen bookmark folders the same across browser profiles on
one computer. It is a browser extension plus one Go program, and it stores
everything in YAML files you can read. No server, no account, no network.

Status: early. It works with Google Chrome and Firefox Developer Edition on
Linux.

## Install (Google Chrome on Linux)

You need the `mirrormark` binary (`make` builds `build/mirrormark`). Copy it
somewhere stable such as `~/.local/bin`, because Chrome runs it from there:
the host manifest names the path `install` ran it from, with symlinks
resolved. If you move or rename the binary, run `mirrormark install` again.

1. Run `mirrormark install` as yourself, not with `sudo`. Chrome may be open
   or closed. By default it binds every profile in `~/.config/google-chrome`;
   `--profile "Profile 1"` or `--profile Work` (a directory or a profile's
   name; repeat it for more) binds only those, and `--user-data-dir DIR` reads
   another Chrome data directory. A bound profile syncs nothing until
   `config.yaml` names it.
2. Restart Chrome when it asks: close every window, and check that no
   `chrome` process is left (`pgrep -x chrome`), because Chrome may keep
   running in the background. Chrome reads the extension's file only when
   it starts.
3. Run `mirrormark install` again. It opens a tab in each profile to bind it,
   waits for the profile to answer, and prints `bound: <profile> -> <id>` or
   `not bound: ...`.
4. Export your bookmarks from Chrome (Bookmark manager, Export bookmarks) so
   you have a copy, then edit `~/.config/mirrormark/config.yaml` to share
   folders (see below). Changes appear in the other profiles within a few
   seconds.

### What `sudo` does

Chrome installs a local extension only if a file in a root-owned directory
names it, and it looks for native messaging hosts in one, so `install` asks
for your password once. For each of two small JSON files it runs
`sudo install -D -m 0644 /dev/stdin <target>` and writes the file's
contents to that command's input; no mirrormark code runs as root. The
targets are:

- `/usr/share/google-chrome/extensions/<extension id>.json`, naming
  `~/.local/share/mirrormark/mirrormark.crx` and its version. Chrome installs
  the extension from it, without a prompt, into every profile of every Chrome
  on this computer. Profiles you don't bind leave it idle.
- `/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json`,
  which lets only that extension start the `mirrormark` binary.

Before this step, `install` has written the key, `extension/` and
`mirrormark.crx` in `~/.local/share/mirrormark/`. If `sudo` fails, `install`
stops: it makes no database files and no `config.yaml`, but if the second
file fails, the first stays in place. Run `install` again when `sudo` works;
it writes both files again. When the files are already up to date,
`install` skips this step.

### Ids, labels and the key

Each bound profile gets a database id: the `<id>` in the
`bound: <profile> -> <id>` line that `install` prints, and the name of its
file, `~/.local/share/mirrormark/profiles/<id>.yaml`. `config.yaml` names
profiles by these ids. The id comes from the profile's name in Chrome: a
profile named "Your Chrome" gets `chrome-your-chrome`, and a second profile
of that name gets `chrome-your-chrome-2` (then `-3` and so on). To share a
folder between those two:

    format: 1
    unions:
      work:
        chrome-your-chrome: toolbar/Work
        chrome-your-chrome-2: toolbar/Work

Each profile also gets a label, such as `chrome/your-chrome`, used in
conflict copies. Edit the label in its database file if you like.

The first `install` or `unpack` creates `~/.local/share/mirrormark/extension.pem`.
The extension id comes from this key, so keep it: a new key is a new
extension.

The host manifest has one fixed path for the whole computer and lets one
extension id start the host. Two installs with different keys, such as two
users on one computer, replace each other's manifest, and only the last
one to run `install` reaches its host. `install` warns when it replaces a
manifest that names another extension.

### Upgrade, uninstall, unpack

Upgrading: replace the binary and run `mirrormark install` again, then
restart Chrome if it asks. If the new build has the same extension version,
`install` changes neither system file and Chrome keeps its copy of the
extension, even if the extension's files changed (see
[Development](#development)).

`mirrormark uninstall` removes the two system files (with `sudo rm` if
needed), the CRX and the unpacked extension. It leaves the host manifest
alone if another install (a different key) has since replaced it. Chrome
removes the extension from every profile when it next starts. Your
bookmarks stay in Chrome, and your config, databases, backups, key and log
stay where they are; it says so. `mirrormark uninstall --purge` also
deletes Chrome's databases, their backups and the key. It deletes the
config and log too, unless a Firefox profile's database is left: Firefox's
profiles still sync through them, and Firefox's `uninstall` needs those
databases to find its profiles.

`mirrormark unpack [DIR]` writes the extension, with the same id as the
installed one, to `DIR` or to `~/.local/share/mirrormark/extension/`, for
Load unpacked in `chrome://extensions` (Developer mode). It still needs
`install`'s host manifest to reach the host. For Load unpacked, give a
folder of your own, such as `~/mirrormark-extension`: `install` deletes and
rewrites `~/.local/share/mirrormark/extension/`, and `uninstall` deletes it.

## Install (Firefox Developer Edition on Linux)

Release Firefox cannot run mirrormark yet. Firefox Developer Edition can.

**Before you start.** Firefox installs only add-ons Mozilla signed unless a
profile says otherwise, and mirrormark is not signed. So `install` sets
three prefs in each profile's `user.js`:

- `xpinstall.signatures.required` false: that profile then accepts any
  unsigned add-on, not only mirrormark.
- `extensions.autoDisableScopes` 14: an add-on file in the profile's
  `extensions` folder starts enabled without asking; add-ons elsewhere
  still ask.
- `extensions.startupScanScopes` 1: Firefox looks in the profile's own
  `extensions` folder at start, which is how it finds the add-on, and
  later notices it is gone.

`mirrormark uninstall --browser firefox` sets the first two back (see
[Upgrade and uninstall (Firefox)](#upgrade-and-uninstall-firefox)).

The binary needs a stable place, as for Chrome.

1. Run `mirrormark install --browser firefox` as yourself; it needs no
   `sudo`. It takes every profile in `$XDG_CONFIG_HOME/mozilla/firefox`
   (`~/.config/mozilla/firefox` if that is unset) when that holds a
   `profiles.ini`, else in `~/.mozilla/firefox`.
   `--profile NAME` (repeatable) picks some of them, and
   `--user-data-dir DIR` reads the profiles in `DIR` instead. It refuses,
   before writing anything, if a chosen profile was last run by a Firefox
   other than Developer Edition. A profile that has never run is skipped with
   a `skipped:` line, unless you name it with `--profile`; start Firefox in it
   once, then run `install` again to add it.
2. Restart Firefox in those profiles if it is running (close every window).
3. Run `mirrormark install --browser firefox` again. For each profile it
   starts Firefox there if it is not running, opens a tab and prints
   `bound: <profile> -> firefox-<name>`.
4. Export your bookmarks from Firefox (Bookmarks, Manage bookmarks, Import
   and Backup), then share folders in `config.yaml` as for Chrome (see
   [Ids, labels and the key](#ids-labels-and-the-key) and
   [config.yaml](#configyaml)). Separators stay where you put them;
   mirrormark never syncs them.

### Upgrade and uninstall (Firefox)

Upgrading: replace or move the binary, run
`mirrormark install --browser firefox` again, then restart Firefox in
those profiles.

`mirrormark uninstall --browser firefox` removes the XPI from each profile
whose database file says `browser: firefox`, and the host manifest if it
names mirrormark. In each such profile's `user.js` it sets
`xpinstall.signatures.required` back to `true` and
`extensions.autoDisableScopes` back to `15`. It changes every line that
sets them to mirrormark's values, including lines you wrote yourself; that
only turns the checks back on. If no database names a Firefox profile, it
changes no `user.js` and says so. It leaves `extensions.startupScanScopes`
at 1, so Firefox notices the XPI is gone. Your bookmarks stay in Firefox,
and your config, databases, backups and log stay where they are.
`--purge` deletes the Firefox profiles' databases and their backups, and
the config and log too unless a Chrome profile's database is left.
Installing again turns the two lines back, without adding new ones.

## Files

| File | What it is |
| --- | --- |
| `~/.config/mirrormark/config.yaml` | Your unions. You write it. |
| `~/.local/share/mirrormark/profiles/<id>.yaml` | One database per bound profile. You may edit its `profile:` block; mirrormark writes the rest. |
| `~/.local/share/mirrormark/backups/<id>/` | One copy of each database a day, the newest 30. For reading only. |
| `~/.local/share/mirrormark/extension.pem` | The key that fixes the extension id (mode 0600). |
| `~/.local/share/mirrormark/mirrormark.crx`, `extension/` | The packed and unpacked extension, written by `install`. |
| `<profile>/extensions/mirrormark@chriscz.github.io.xpi` | The add-on, in each Firefox profile, written by `install --browser firefox`. |
| `<profile>/user.js` | The three prefs `install --browser firefox` sets in each Firefox profile (see [Before you start](#install-firefox-developer-edition-on-linux)); `uninstall --browser firefox` sets two of them back. |
| `~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json` | Lets the Firefox add-on start the `mirrormark` binary. |
| `~/.local/state/mirrormark/log` | The host's log. Over 1 MiB, it moves to `log.1`, replacing the one before, when a host starts. |

`$XDG_CONFIG_HOME`, `$XDG_DATA_HOME` and `$XDG_STATE_HOME` move these when set.
Database and backup files are readable only by you (mode 0600); they hold
your bookmarks in plain text.

### config.yaml

    format: 1
    unions:
      work:                            # any name
        chrome-personal: toolbar/Work  # profile id: folder path
        chrome-work: toolbar/Work
      reading:
        chrome-personal: other/Reading
        chrome-work: other/Reading

A path starts with `toolbar`, `other` or `mobile`, or, for a Firefox
profile, `menu`, or with the title the browser shows for that folder, such
as `Bookmarks bar`. Write `\/` for a slash inside a folder name. One
profile's folders may not be inside each other.

## How syncing behaves

- A union holds everything any member holds, at the same relative paths.
- An edit shows in the other running profiles within a second or two. A
  profile whose Chrome was closed catches up as soon as Chrome opens it,
  without any window or the popup open.
- Deleting a bookmark in a union moves it to a `_deleted` folder inside the
  union, in every profile. Restore it by moving it out. Delete it from
  `_deleted` to remove it everywhere.
- A guard stops a round that looks like a broken tree rather than your
  edits, in a union of more than five bookmarks and folders: an empty member
  folder (a browser that has not loaded its bookmarks), a member folder that
  holds almost nothing the profile's database knows (a profile bound to the
  wrong database), or a database file that lacks more than half of the union
  (a damaged file). It also stops a round that deletes or purges more than
  half of the union, but a deleted folder counts once, however much it holds.
  The union then syncs nothing and the popup shows `db-mismatch` (see
  [below](#a-union-reports-db-mismatch)). To be sure a large delete goes
  through, move the bookmarks into `_deleted` instead of deleting them, and
  empty a large `_deleted` by deleting the `_deleted` folder itself.
- When two profiles change the same bookmark differently, you get both,
  titled for example `GitHub (chrome/personal)` and `GitHub (chrome/work)`.
  For folders, the change that reached the files first wins. Deleting a
  bookmark that another profile moved meanwhile counts as such a change: the
  moved copy stays where it went, and yours lands in `_deleted`.
- Reordering bookmarks inside a folder stays local.
- A missing member folder is created on the first round. If it later
  disappears or is replaced by another folder at that path, that profile
  stops syncing the union until you restore the folder or change its path in
  `config.yaml`; a new path makes the profile a new member, which deletes
  nothing.
- While any member's database file cannot be read, the union does not sync.

## When something is wrong

Everything the host does, including skipped unions, conflicts and failed
changes, goes to `~/.local/state/mirrormark/log`, every time it happens.

A change the browser refuses stops that profile from saving its round. The
host retries every 5 seconds until the change goes through, and a changed
file starts a round sooner. The same goes for a database file the host
cannot write.

### What the popup shows

The extension's popup is the mirrormark button in Chrome's toolbar. It
shows:

- the database the profile syncs as, such as `Syncs as chrome-work.`, or
  that the profile is not bound;
- why the host stopped, if it did, such as `db-locked: …`, or why Chrome
  could not start it;
- each problem the last round found, as `<code>: <message>`, such as
  `union-skipped: …` or `db-mismatch: …`. A problem stays until a round no
  longer finds it, so nothing below the binding means the last round synced
  cleanly.

### The popup says Chrome cannot reach the host

`Specified native messaging host not found.` or `Access to the specified
native messaging host is forbidden.` means Chrome could not start
mirrormark. The host manifest is missing, or another install of mirrormark,
with another key, replaced it, so it names another extension. Run
`mirrormark install` again, and restart Chrome if it asks. On a computer
where two users install mirrormark, only the last one to run `install`
reaches its host (see [Ids, labels and the key](#ids-labels-and-the-key)).

### Installing and binding

- **`not bound: <profile>`.** The profile did not answer within three
  tries. Check that you restarted Chrome after the first `install` and that
  mirrormark is enabled in `chrome://extensions` in that profile, then run
  `mirrormark install --profile "<profile>"`. A profile opened for the first
  time since the install often needs the second or third try; `install`
  makes them.
- **You removed mirrormark from a profile.** Chrome remembers a **Remove**
  for good in that profile and does not install it there again, even after
  `uninstall` and `install`. (Turning it off is undone by turning it back
  on.) To use mirrormark in that profile again, run `mirrormark unpack
  ~/mirrormark-extension`, load that folder with Load unpacked in that
  profile's `chrome://extensions`, and run `mirrormark install --profile
  "<profile>"`. Run the same `unpack` again after each upgrade and reload
  it.
- **`install` stops at a database file it cannot read.** That file may be
  the profile's own, so `install` does not make another. Fix the file or
  move it out of `~/.local/share/mirrormark/profiles/`.

### A union reports `db-mismatch`

The guard refused the round, and refuses every round until the cause is
gone; nothing in that union syncs meanwhile. The usual causes:

- **You deleted more than half of the union one bookmark at a time, or
  emptied the member folder.** The other profiles still hold the bookmarks.
  Put them back with Undo (Ctrl+Z) in the bookmark manager, or by resetting
  the record as below, then move them into `_deleted`, or into one folder
  that you then delete.
- **Most of a union went to `_deleted` and was emptied while this profile
  was closed.** From its record, the change looks like a broken file. Reset
  the record.
- **The profile syncs as the wrong database**, shown in the popup. Run
  `mirrormark install --profile "<profile>"` to bind it to its own.

To reset a profile's record, so that it joins the union again as a new
member:

1. Close Chrome in that profile.
2. Move its database file aside, for example
   `mv ~/.local/share/mirrormark/profiles/chrome-work.yaml ~/chrome-work.yaml.old`.
3. Run `mirrormark install --profile "<profile>"` (restart Chrome first if it
   asks). It makes a new database file and binds the profile. If the id in
   its `bound:` line differs from the old one, put the new id in place of
   the old one in `config.yaml`.

A new member's first round deletes nothing, so bookmarks that profile still
holds and the others deleted come back into the union. Delete them again.

## Development

Everything builds and tests in a Docker image; nothing is installed on your
machine except Docker.

    bin/dev run make             # format check, vet, unit tests, build
    bin/dev run make check-race  # the unit tests under the race detector
    bin/dev run make check-e2e   # end-to-end tests in real browsers
    bin/dev run make check-e2e RUN=TestInstallAndBind  # one of them
    bin/dev shell                # a shell in the dev container

The end-to-end tests run real browsers from the dev image under Xvfb:
Google Chrome, driven over the DevTools protocol, and Firefox Developer
Edition, driven over WebDriver BiDi in a D-Bus session, which carries
`install`'s bind page to a running Firefox. Each test installs mirrormark
into two browser instances with `mirrormark install` and binds them. They
run one at a time, because they share Chrome's system directories, which
only the dev image lets your user write. Besides installing and binding,
they share a folder between the two instances and check that each kind of
edit, a delete, restore and purge, a conflict, the first sync of two filled
folders and a closed browser's catch-up reach both, with no warning in the
host's log. Each edit must reach the other running browser within 3
seconds; the tests log how long each took. The sync tests run three times:
between two Chromes, between two Firefoxes, and between a Firefox and a
Chrome, as subtests named `chrome-chrome`, `firefox-firefox` and
`firefox-chrome` (for example `RUN=TestEditsPropagate/firefox-chrome`).
Run `bin/dev image build` first after the `Dockerfile` changes.

The union fuzz tests in `internal/merge` and `internal/round` run 300 seeds;
run more with, for example,
`bin/dev run go test ./internal/round -args -mirrormark.fuzz=20000`.

The binary lands in `build/mirrormark`.

The Chrome extension in `extension/` is plain JavaScript with no build step;
the binary embeds it. Its version is written only in
`extension/manifest.json`. Raise it in every release that changes the
extension: Chrome installs a new copy only when the version changes.

## Licence

MPL-2.0. See `LICENSE`.
