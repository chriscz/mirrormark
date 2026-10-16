# mirrormark

mirrormark keeps chosen bookmark folders the same across browser profiles on
one computer. It is a browser extension plus one Go program, and it stores
everything in YAML files you can read. No account and no network: the
extension talks only to the program, on 127.0.0.1.

Status: early. It works with Google Chrome, Brave, Firefox Developer
Edition and LibreWolf on Linux.

## Install (Google Chrome on Linux)

You need the `mirrormark` binary (`make` builds `build/mirrormark`). Copy it
somewhere stable such as `~/.local/bin`, because the service runs it from
there: the service names the path `install` ran it from, with symlinks
resolved. If you move or rename the binary, run `mirrormark install` again.

1. Run `mirrormark install` as yourself, not with `sudo`, to see the
   profiles in `~/.config/google-chrome`, their numbers and their state. It
   changes nothing. Chrome may be open or closed. Then run it with
   `--profile ID` or `--profile #` for each profile to bind, such as
   `mirrormark install --profile Default --profile "Profile 1"`. An ID is
   the profile's directory name (`Default`, `"Profile 1"`; a Firefox
   profile's folder name), and `#` is its number in the list. The numbers
   follow the IDs in order (`Profile 2` before `Profile 10`), and stay the
   same while no profile is added or removed. `--user-data-dir DIR` reads
   another Chrome data directory. Each `install` below takes the same
   `--profile` options; without them it only lists. A bound profile syncs
   nothing until `config.yaml` names it.
   `install` also sets up `mirrormark serve` as a systemd user service
   (`~/.config/systemd/user/mirrormark.service`), which runs as you and
   starts when you log in, and starts or restarts it. It says which, and
   prints the commands to check the service, read its log and stop it.
   Without systemd, it prints the command to keep running.
2. Restart Chrome when it asks: close every window, and check that no
   `chrome` process is left (`pgrep -x chrome`), because Chrome may keep
   running in the background. Chrome reads the extension's file only when
   it starts.
3. Run `mirrormark install --profile ...` again. It first checks that
   `mirrormark serve` is running, waiting up to 5 seconds for the service
   to start; if it is not, it says how to start it, opens nothing and exits
   with status 1. It then checks each named profile's files. In each
   profile where mirrormark is installed and turned on, it opens a tab once,
   waits up to 30 seconds for mirrormark to report in, and prints
   `bound: <profile> -> <id>`. For any other profile it prints
   `not ready: <profile>: ...`, which says what to do, or
   `not bound: <profile>: ...`. Chrome records the install in the profile up
   to half a minute after it starts, so a `not ready` just after a restart
   means: wait, and run it again.
4. Export your bookmarks from Chrome (Bookmark manager, Export bookmarks) so
   you have a copy, then edit `~/.config/mirrormark/config.yaml` to share
   folders (see below). Changes appear in the other profiles within a few
   seconds.

### What `sudo` does

Chrome installs a local extension only if a file in a root-owned directory
names it, so `install` asks for your password once. It runs
`sudo install -D -m 0644 /dev/stdin <target>` and writes one small JSON
file's contents to that command's input; no mirrormark code runs as root.
The target is `/usr/share/google-chrome/extensions/<extension id>.json`,
naming `~/.local/share/mirrormark/mirrormark.crx` and its version. Chrome
installs the extension from it, without a prompt, into every profile of
every Chrome on this computer. Profiles you don't bind leave it idle.

Before this step, `install` has written the key, `extension/` and
`mirrormark.crx` in `~/.local/share/mirrormark/`. If `sudo` fails, `install`
stops: it makes no database files and no `config.yaml`. Run `install` again
when `sudo` works. When the file is already up to date, `install` skips
this step.

### Ids, labels and the key

Each bound profile gets a database id: the `<id>` in the
`bound: <profile> -> <id>` line that `install` prints, and the name of its
file, `~/.local/share/mirrormark/profiles/<id>.yaml`. `config.yaml` names
profiles by these ids. The id comes from the profile's name in Chrome: a
profile named "Your Chrome" gets `chrome-your-chrome`, and a second profile
of that name gets `chrome-your-chrome-2` (then `-3` and so on). Once it has
bound every profile named, `install` prints a `config.yaml` like the one
below for the ids it just bound, ready to paste. To share a folder between
those two:

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

### Upgrade, uninstall, unpack

Upgrading: replace the binary and run `mirrormark install --profile ...`
again for the profiles you bound, then restart Chrome if it asks. After
upgrading from a build before `mirrormark serve`, the first `install` asks
for a restart, and the second binds each profile again, so that it gets
its token. A native messaging host manifest that such a build left
(`/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json`,
or `~/.mozilla/native-messaging-hosts/` for Firefox) does nothing now, and
`uninstall` removes it. If the new build has the same extension version,
`install` leaves the system file alone and Chrome keeps its copy of the
extension, even if the extension's files changed (see
[Development](#development)). `install` restarts the service on every run,
so after upgrading the binary it runs the new one.

`mirrormark uninstall` removes Chrome's system file (with `sudo rm` if
needed), the CRX and the unpacked extension, and an old host manifest if it
names this install's extension; one that names another install's (a
different key) stays. While Brave still uses them, the CRX and the key stay
(see [Install
(Brave on Linux)](#install-brave-on-linux)). Chrome removes the extension
from every profile when it next starts. Your bookmarks stay in Chrome, and
your config, databases, backups, key and log stay where they are; it says
so. It stops and removes the service once no other browser uses
mirrormark, and otherwise says which browser still does.
`mirrormark uninstall --purge` also deletes Chrome's databases, their
backups and the key. It deletes the config and log too, unless another
browser's database is left: its profiles still sync through them, and its
`uninstall` needs those databases to find its profiles. If a
`mirrormark serve` you started yourself still runs, the last purge keeps
its `serve.lock` and says to stop it.

`mirrormark unpack [DIR]` writes the extension, with the same id as the
installed one, to `DIR` or to `~/.local/share/mirrormark/extension/`, for
Load unpacked in `chrome://extensions` (Developer mode). It still needs
the server and `install`'s binding to sync. For Load unpacked, give a
folder of your own, such as `~/mirrormark-extension`: `install` deletes and
rewrites `~/.local/share/mirrormark/extension/`, and `uninstall` deletes it.

`mirrormark serve` runs the server the extensions connect to, on
127.0.0.1 only, and logs to `~/.local/state/mirrormark/log`. One runs per user;
a second one stops at once. `install` runs it as the systemd user service
`mirrormark.service` (`systemctl --user status mirrormark`). Once the
service runs, stop any `mirrormark serve` you started by hand: while that
one holds `serve.lock`, the service's server exits at once, and systemd
retries it quietly and then leaves the service failed. The extension
connects to it over a WebSocket on 127.0.0.1, on the first of the ports
61959, 63018 and 63454 whose server proves it holds the profile's token.

## Install (Brave on Linux)

Brave installs as Chrome does (see [Install (Google Chrome on
Linux)](#install-google-chrome-on-linux)), with `--browser brave`:

1. Run `mirrormark install --browser brave` to see the profiles in
   `~/.config/BraveSoftware/Brave-Browser` (or `$XDG_CONFIG_HOME/…`), their
   numbers and their state, then run it with `--profile ID` or
   `--profile #` for each profile to bind; `--profile` and
   `--user-data-dir` work as for Chrome. With `sudo` it
   writes `/opt/brave.com/brave/extensions/<extension id>.json`, naming the
   same CRX as Chrome's, so Chrome and Brave share one extension id.
2. Restart Brave (`pgrep -x brave` shows none left), then run
   `mirrormark install --browser brave --profile ...` again to bind the
   profiles. As for Chrome, it opens a tab once only in profiles where
   mirrormark is installed and turned on, and prints `not ready: ...` with
   what to do for the others. Brave too records the install up to half a
   minute after it starts. Profile ids start with `brave-`, labels with
   `brave/`.

Brave's extensions page may show a Safety Check card saying it can't verify
where mirrormark comes from; mirrormark stays on. Brave reopens your last
session when it starts, so a bind tab left open may open again
and bind the profile again to the same database; that is harmless.

After upgrading mirrormark, run `mirrormark install --browser brave` as well
as `mirrormark install` if you use both, each with its `--profile` options.
`mirrormark uninstall --browser brave` removes Brave's file. It keeps the CRX
while Chrome still has mirrormark installed, and
`--purge` keeps the key while Chrome has it installed or a Chrome profile's
database is left; the same goes for Chrome while Brave has it.

Only Brave stable from Brave's own package is supported. Brave Beta and
Nightly, and Brave from Flatpak or Snap, are untested and not supported.

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
   `sudo`. It lists the profiles in `$XDG_CONFIG_HOME/mozilla/firefox`
   (`~/.config/mozilla/firefox` if that is unset) when that holds a
   `profiles.ini`, else in `~/.mozilla/firefox`, with their numbers and
   state, and changes nothing. Then run it with `--profile ID` or
   `--profile #` for each profile to bind, as for Chrome; the ID is the
   profile's folder name, such as `x1y2z3ab.dev-edition-default`.
   `--user-data-dir DIR` reads the profiles in `DIR` instead. It refuses,
   before writing anything, if a named profile has never run or was last
   run by a Firefox other than Developer Edition.
   `install` also sets up `mirrormark serve` as a systemd user service
   (`~/.config/systemd/user/mirrormark.service`) and starts it. Without
   systemd, it prints the command to keep running.
2. Restart Firefox in those profiles if it is running (close every window).
3. Run `mirrormark install --browser firefox --profile ...` again. It first
   checks each named profile's files. In each profile where Firefox has
   installed mirrormark and it is turned on, it opens a tab once (starting
   Firefox there if it is not running), waits up to 30 seconds for mirrormark
   to report in, and prints `bound: <profile> -> firefox-<name>`. For any
   other profile it prints `not ready: <profile>: ...`, which says what to
   do, or `not bound: <profile>: ...`. Firefox installs the add-on when it
   starts in the profile, so start it there once after step 2 if you have
   not.
4. Export your bookmarks from Firefox (Bookmarks, Manage bookmarks, Import
   and Backup), then share folders in `config.yaml` as for Chrome (see
   [Ids, labels and the key](#ids-labels-and-the-key) and
   [config.yaml](#configyaml)). Separators stay where you put them;
   mirrormark never syncs them.

### Upgrade and uninstall (Firefox)

Upgrading: replace or move the binary, run
`mirrormark install --browser firefox --profile ...` again for the
profiles you bound, then restart Firefox in those profiles. After upgrading
from a build before `mirrormark serve`, the first `install` asks for a
restart, and the second binds each profile again, so that it gets its
token.

`mirrormark uninstall --browser firefox` removes the XPI from each profile
whose database file says `browser: firefox`, and an old host manifest if
it names mirrormark. In each such profile's `user.js` it sets
`xpinstall.signatures.required` back to `true` and
`extensions.autoDisableScopes` back to `15`. It changes every line that
sets them to mirrormark's values, including lines you wrote yourself; that
only turns the checks back on. If no database names a Firefox profile, it
changes no `user.js` and says so. It leaves `extensions.startupScanScopes`
at 1, so Firefox notices the XPI is gone. It removes the service as for
Chrome, once no other browser uses mirrormark. Your bookmarks stay in Firefox,
and your config, databases, backups and log stay where they are.
`--purge` deletes the Firefox profiles' databases and their backups, and
the config and log too unless another browser's database is left.
Installing again turns the two lines back, without adding new ones.

## Install (LibreWolf on Linux)

LibreWolf installs as Firefox Developer Edition does (see [Install (Firefox
Developer Edition on Linux)](#install-firefox-developer-edition-on-linux)),
with `--browser librewolf`, and the same three prefs in each profile's
`user.js`; read "Before you start" there first.

1. Run `mirrormark install --browser librewolf` to list the profiles in
   `$XDG_CONFIG_HOME/librewolf/librewolf` (`~/.config/librewolf/librewolf`)
   when that holds a `profiles.ini`, else in `~/.librewolf`, then run it
   with `--profile ID` or `--profile #` for each profile to bind. LibreWolf
   makes a second profile, `default`, that it never uses; it lists as
   `never run`, so leave it out.
2. Restart LibreWolf in those profiles if it is running, then run
   `mirrormark install --browser librewolf --profile ...` again to bind
   them. Profile ids start with `librewolf-`, labels with `librewolf/`.

`mirrormark uninstall --browser librewolf` works as for Firefox, and keeps
the service while a Developer Edition profile still has mirrormark, and the
other way round.

Only LibreWolf's official Linux tarball has been tried. LibreWolf from its
Debian/Ubuntu repository, Flatpak, AppImage or the AUR is untested. If you
set LibreWolf to clear more than its defaults when it closes, the profile may
lose its binding at each restart; the popup then says it is not bound.

## Files

| File | What it is |
| --- | --- |
| `~/.config/mirrormark/config.yaml` | Your unions. You write it. |
| `~/.local/share/mirrormark/profiles/<id>.yaml` | One database per bound profile. You may edit its `profile:` block, except `origin` and `token`, which `install` writes when it binds the profile; mirrormark writes the rest. |
| `~/.local/share/mirrormark/serve.lock` | Held by the running `mirrormark serve`, so one runs per user. |
| `~/.local/share/mirrormark/backups/<id>/` | One copy of each database a day, the newest 30. For reading only. |
| `~/.local/share/mirrormark/extension.pem` | The key that fixes the extension id (mode 0600). |
| `~/.local/share/mirrormark/mirrormark.crx`, `extension/` | The packed and unpacked extension, written by `install`. |
| `<profile>/extensions/mirrormark@chriscz.github.io.xpi` | The add-on, in each Developer Edition or LibreWolf profile, written by `install --browser firefox` or `librewolf`. |
| `<profile>/user.js` | The three prefs `install --browser firefox` or `librewolf` sets in each profile (see [Before you start](#install-firefox-developer-edition-on-linux)); `uninstall` sets two of them back. |
| `~/.config/systemd/user/mirrormark.service` | The systemd user service that runs `mirrormark serve`, written by `install`; `uninstall` removes it with the last browser. |
| `~/.local/state/mirrormark/log` | The server's log. Over 1 MiB, it moves to `log.1`, replacing the one before, when the server starts. |

The token travels in the bind page's URL, which is a command-line argument
of the browser process. On Linux, other local users can read it with `ps`
or `/proc/<pid>/cmdline` unless `/proc` is mounted with `hidepid=2`.

`$XDG_CONFIG_HOME`, `$XDG_DATA_HOME` and `$XDG_STATE_HOME` move these when set;
`mirrormark help` lists them where they are for you.
If you set them in your shell, set them for systemd too, in a file in
`~/.config/environment.d/`, so the service finds the same files.
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

Everything the server does, including skipped unions, conflicts and failed
changes, goes to `~/.local/state/mirrormark/log`, every time it happens.
Each change to `config.yaml` it loads shows there once, as a
`loaded config.yaml` line with the number of unions.
Connections it turns away, and those that never prove which profile they
are, take at most a line a minute, with how many there were.

A change the browser refuses stops that profile from saving its round. The
server retries every 5 seconds until the change goes through, and a changed
file starts a round sooner. The same goes for a database file the server
cannot write.

### What the popup shows

The extension's popup is the mirrormark button in Chrome's toolbar. It
shows:

- the database the profile syncs as, such as `Syncs as chrome-work.`, or
  that the profile is not bound;
- why the server stopped syncing the profile, if it did, such as
  `db-locked: …`, or that mirrormark is not running;
- each problem the last round found, as `<code>: <message>`, such as
  `union-skipped: …` or `db-mismatch: …`. A problem stays until a round no
  longer finds it, so nothing below the binding means the last round synced
  cleanly.

Some errors concern the connection itself. Until a server has proven it
is yours, the popup shows only fixed texts, never words the server sent,
and names the port. When one port fails and another answers with a known
error, the popup shows the known error.

- **`not bound yet: …`.** The profile was bound by a build before
  `mirrormark serve` and has no token. Run `mirrormark install --profile
  <profile>` for that profile again (with `--browser` for a browser other
  than Chrome).
- **`bad-token: … (port N)`.** The server does not accept this profile's
  token or origin, as when the database was bound again from another
  profile. Run `install --profile` for that profile again, as above.
- **`db-missing: … (port N)`.** The server has no database file for the
  profile. Run `install --profile` for that profile again.
- **`the program on port N could not prove it is your mirrormark`**, or
  **`unexpected answer on port N`.** Another program holds that port,
  perhaps another user's. The extension tries the next port, where your
  server listens if the first was taken. If sync does not resume, find the
  program with `ss -ltnp`.

### The popup says mirrormark is not running

The extension could not reach `mirrormark serve` on any of its ports. Start
it with `systemctl --user start mirrormark`, or run `mirrormark serve`. If
it does not stay up, `systemctl --user status mirrormark` and the log,
`~/.local/state/mirrormark/log`, say why. The extension tries again every
30 seconds, and syncs once the server is back.

### Installing and binding

- **The token.** The bind page also stores the profile's token, a secret
  that `install` writes into the profile's database file. Each time the
  extension connects, the server proves it holds the token, then the
  extension does; neither sends the token itself. Running `install` for a
  bound profile keeps its token.
- **`not ready: <profile>`.** The profile's files say mirrormark is not
  installed there, is turned off, or is another build, so `install` opened
  nothing there. The line says what to do and which command to run then.
- **`not bound: <profile>`.** The profile's files say mirrormark is ready,
  but it did not report in within 30 seconds of the tab opening. Look at the
  tab the browser opened, then run the command the line gives.
- **"This page has been blocked by Chrome"** (or by Brave) on a
  `chrome-extension://` page means the extension is not loaded in that
  profile, not that the browser blocks mirrormark. Run `mirrormark install`
  to see the profile's state, and `mirrormark install --profile <profile>`
  to follow its `not ready` line.
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
Google Chrome and Brave, driven over the DevTools protocol, and Firefox
Developer Edition and LibreWolf, driven over WebDriver BiDi in a D-Bus
session, which carries `install`'s bind page to a running Firefox. Each test installs mirrormark
into two browser instances with `mirrormark install` and binds them, with
its own `mirrormark serve` running in place of the systemd service, which
the dev image cannot run. They
run one at a time, because they share the Chromium browsers' system directories, which
only the dev image lets your user write. Besides installing and binding
in each of the four browsers, they share a folder between the two instances and check that each kind of
edit, a delete, restore and purge, a conflict, the first sync of two filled
folders and a closed browser's catch-up reach both, with no warning in the
server's log. One test stops the server, puts a program that cannot prove
itself on the first port, starts the server again on the second, and
checks that the extension skips that program and syncs again. Each edit
must reach the other running browser within 3
seconds; the tests log how long each took. The sync tests run four times:
between two Chromes, between two Firefoxes, between a Firefox and a Chrome,
and between Brave and LibreWolf, as subtests named `chrome-chrome`,
`firefox-firefox`, `firefox-chrome` and `brave-librewolf` (for example
`RUN=TestEditsPropagate/brave-librewolf`). A failing test keeps the
server's log and each browser's output in `build/e2e-logs/<test>/`.
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
