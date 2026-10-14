# mirrormark

mirrormark keeps chosen bookmark folders the same across browser profiles on
one computer. It is a browser extension plus one Go program, and it stores
everything in YAML files you can read. No server, no account, no network.

Status: early. It works with Google Chrome on Linux only.

## Install (Google Chrome on Linux)

You need the `mirrormark` binary (`make` builds `build/mirrormark`). Copy it
somewhere stable such as `~/.local/bin`, because Chrome runs it from there.

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

If `sudo` fails, `install` stops and changes nothing else. When the files
are already up to date, `install` skips this step.

### Ids, labels and the key

Each bound profile gets a database id from its Chrome name, such as
`chrome-work`, and a label such as `chrome/work` (used in conflict copies).
A taken id gets `-2`, `-3` and so on. Edit the label in
`~/.local/share/mirrormark/profiles/<id>.yaml` if you like.

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
restart Chrome if it asks.

`mirrormark uninstall` removes the two system files (with `sudo rm` if
needed), the CRX and the unpacked extension. Chrome removes the extension
from every profile when it next starts. Your bookmarks stay in Chrome, and
your config, databases, backups, key and log stay where they are; it says
so. `mirrormark uninstall --purge` deletes them too.

`mirrormark unpack [DIR]` writes the extension, with the same id as the
installed one, to `DIR` or to `~/.local/share/mirrormark/extension/`, for
Load unpacked in `chrome://extensions` (Developer mode). It still needs
`install`'s host manifest to reach the host.

## Files

| File | What it is |
| --- | --- |
| `~/.config/mirrormark/config.yaml` | Your unions. You write it. |
| `~/.local/share/mirrormark/profiles/<id>.yaml` | One database per bound profile. You may edit its `profile:` block; mirrormark writes the rest. |
| `~/.local/share/mirrormark/backups/<id>/` | One copy of each database a day, the newest 30. For reading only. |
| `~/.local/share/mirrormark/extension.pem` | The key that fixes the extension id (mode 0600). |
| `~/.local/share/mirrormark/mirrormark.crx`, `extension/` | The packed and unpacked extension, written by `install`. |
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

A path starts with `toolbar`, `other` or `mobile`, or with the title Chrome
shows for that folder, such as `Bookmarks bar`. Write `\/` for a slash inside
a folder name. One profile's folders may not be inside each other.

## How syncing behaves

- A union holds everything any member holds, at the same relative paths.
- Deleting a bookmark in a union moves it to a `_deleted` folder inside the
  union, in every profile. Restore it by moving it out. Delete it from
  `_deleted` to remove it everywhere.
- A round that would delete or purge more than half of a union (of more than
  five bookmarks and folders) syncs nothing for that union, because a browser
  that has not loaded its bookmarks or a damaged file is far likelier. Empty a
  large `_deleted` in parts.
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

The extension's popup (the mirrormark button in Chrome's toolbar) shows
which database the profile syncs as, why the host stopped if it did, and
every problem the last round found, such as a skipped union. A problem
stays in the popup until a round no longer finds it. Everything the host
does, including skipped unions, conflicts and failed changes, goes to
`~/.local/state/mirrormark/log`; a warning that repeats every round is
logged once, then how often it repeated when it stops.

A change the browser refuses stops that profile from saving its round. The
host retries at the next poll three times, then every 5 seconds three
times, then every 30 seconds, until the change goes through; a changed
file starts a round sooner. A round that succeeds starts the count over. The same goes for a
database file the host cannot write.

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
  on.) To use mirrormark in that profile again, run `mirrormark unpack`,
  load `~/.local/share/mirrormark/extension/` with Load unpacked in that
  profile's `chrome://extensions`, and run `mirrormark install --profile
  "<profile>"`. Run `unpack` again after each upgrade and reload it.
- **`install` stops at a database file it cannot read.** That file may be
  the profile's own, so `install` does not make another. Fix the file or
  move it out of `~/.local/share/mirrormark/profiles/`.

### A profile refuses a union after a large delete elsewhere

If most of a union went to `_deleted` and was emptied while one profile was
closed, that profile reports `db-mismatch` every round when it opens: from
its record, the change looks like a broken file. To let it join again as a
new member, reset its record:

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
    bin/dev run make check-e2e   # end-to-end tests in real Chrome
    bin/dev run make check-e2e RUN=TestInstallAndBind  # one of them
    bin/dev shell                # a shell in the dev container

The end-to-end tests start two Google Chrome instances under Xvfb in the
dev image, install mirrormark into them with `mirrormark install` and drive
them over the DevTools protocol. They run one at a time, because they share
Chrome's system directories, which only the dev image lets your user write.
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
