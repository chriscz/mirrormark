# mirrormark

Keep your bookmark folders in sync across Chrome, Brave, Firefox Developer
Edition and LibreWolf profiles. Locally: no cloud, no account.

- **Local only.** A small program on your computer does the syncing. The
  browsers talk to it on 127.0.0.1 and nowhere else.
- **Files you can read.** Each profile's bookmarks live in a plain YAML
  file, with a daily backup.
- **Conflicts are kept, not lost.** If two profiles change the same
  bookmark, you get both copies.
- **Deletes can be undone.** A deleted bookmark moves to a `_deleted`
  folder in every profile. Move it out to restore it.
- **Across browser engines.** A Chrome profile and a Firefox profile can
  share the same folder.

You choose which folders to share. Everything else in each profile stays
as it is.

## Quick start

1. Download the archive for your machine (Linux amd64 or arm64) from the
   [releases](https://github.com/chriscz/mirrormark/releases), and
   [verify it](RELEASING.md#verify-a-download). Put `mirrormark` somewhere
   it can stay, such as `~/.local/bin`: the background service runs it from
   there.

2. List a browser's profiles. This changes nothing.

       mirrormark install --browser chrome

   `--browser` is `chrome` (the default), `brave`, `firefox` or
   `librewolf`.

3. Install into the profiles you want, by ID or by the number in the list:

       mirrormark install --browser chrome --profile Default --profile "Profile 1"

   This also starts `mirrormark serve` as a systemd user service. Chrome and
   Brave ask for your password once (see [below](#supported-browsers)).

4. Restart the browser when asked. Close every window, and check that it is
   really gone (for Chrome, `pgrep -x chrome` prints nothing). Then run the
   same command again. It binds each profile and prints
   `bound: <profile> -> <id>`. A `not ready` line says what to do; just
   after a restart it usually means: wait a few seconds and run it again.

5. Export your bookmarks once, so you have a copy. Then say which folders
   to share in `~/.config/mirrormark/config.yaml`. `install` prints one
   ready to paste:

   ```yaml
   format: 1
   unions:
     work:
       chrome-personal: toolbar/Work
       firefox-work: toolbar/Work
   ```

   Each union names, for each profile id, the folder that profile shares.
   mirrormark picks up the change within seconds, with no restart, and
   from then on edits sync within seconds.

`mirrormark help` prints the commands, the `config.yaml` format and where
your files are. The [guide](docs/guide.md) walks through each browser in
detail.

## Supported browsers

mirrormark runs on Linux.

| Browser | What it needs |
| --- | --- |
| Google Chrome | `sudo` once. Chrome installs a local extension only if a root-owned file names it, so `install` copies one small JSON file into place. No mirrormark code runs as root. ([details](docs/guide.md#what-sudo-does)) |
| Brave (stable, from Brave's package) | The same as Chrome. ([details](docs/guide.md#installing-in-brave)) |
| Firefox Developer Edition | No `sudo`. `install` puts the add-on in the profile and sets three prefs in its `user.js`, so it accepts the unsigned add-on. That profile then accepts any unsigned add-on. ([details](docs/guide.md#installing-in-firefox-developer-edition)) |
| LibreWolf (official tarball) | The same as Firefox Developer Edition. ([details](docs/guide.md#installing-in-librewolf)) |

Release Firefox is not supported. It installs only add-ons that Mozilla
signed, and mirrormark is not signed.

## How syncing behaves

- A shared folder holds everything any profile put in it, at the same
  places.
- Edits reach the other open profiles in a second or two. A closed
  profile catches up when its browser opens.
- Deleting moves a bookmark to `_deleted` inside the shared folder, in
  every profile. Delete it from `_deleted` to remove it everywhere.
- When two profiles change the same bookmark differently, you get both,
  titled for example `GitHub (chrome/personal)` and `GitHub (chrome/work)`.
- The order inside a folder stays local, and so do separators.
- If a round looks like a broken bookmark tree rather than your edits,
  such as an empty folder or a delete of more than half the shared folder,
  mirrormark stops syncing that folder and the popup says `db-mismatch`.
  To delete a lot at once, move it into `_deleted` first. See
  [db-mismatch](docs/guide.md#a-union-reports-db-mismatch).

More in [Syncing in detail](docs/guide.md#syncing-in-detail).

## Firejail

A Chrome or Brave started through firejail cannot see mirrormark's
extension until its firejail `.local` file allows it. `install` notices
firejail, prints the lines to add and says where; it never edits the file
itself. For Brave, add this to `~/.config/firejail/brave.local`:

```
whitelist ${HOME}/.local/share/mirrormark/mirrormark.crx
```

For Chrome, add that line and `whitelist /usr/share/google-chrome/extensions`
to `~/.config/firejail/google-chrome.local`. Then quit the browser
completely and start it again. Firefox Developer Edition and LibreWolf need
nothing. More in the [guide](docs/guide.md#firejail).

## Uninstall

    mirrormark uninstall --browser chrome           # or brave, firefox, librewolf
    mirrormark uninstall --browser chrome --purge   # also delete its databases

Your bookmarks stay in the browser. Without `--purge`, your config,
databases and backups stay too. For Firefox Developer Edition and
LibreWolf, `uninstall` turns the add-on signature check back on. Details
in the [guide](docs/guide.md#uninstalling).

## Safety and privacy

- **What is stored where.** Your config is in `~/.config/mirrormark/`. The
  databases and their backups are in `~/.local/share/mirrormark/`, readable
  only by you; they hold your bookmarks in plain text. The log is in
  `~/.local/state/mirrormark/log`. Nothing leaves your computer.
  `mirrormark help` shows the paths, and the guide has the [full
  list](docs/guide.md#files).
- **A single-user computer.** mirrormark trusts programs that run as you.
  It does not defend against other users on the same computer.
- **Localhost, with a challenge per profile.** The server listens on
  127.0.0.1 only. Each profile has its own secret token; on each
  connection the server and the extension prove to each other that they
  hold it, without sending it.

## Status and limitations

mirrormark is early. It is tested on Linux with the four browsers above,
with end-to-end tests in real browsers.

- Linux only.
- Not in the Chrome Web Store or on addons.mozilla.org.
- Release Firefox, Brave Beta and Nightly, and browsers from Flatpak or
  Snap are not supported.
- Under firejail, only firejail 0.9.72 has been tried.

## Building from source

Everything builds and tests in Docker; you need nothing else.

    bin/dev run make             # format check, vet, unit tests, build
    bin/dev run make check-race  # the unit tests under the race detector
    bin/dev run make check-e2e   # end-to-end tests in real browsers
    bin/dev run make check-e2e RUN=TestInstallAndBind  # one of them
    bin/dev run make dist        # release archives and SHA256SUMS in build/dist/
    bin/dev shell                # a shell in the dev container

The binary lands in `build/mirrormark`. [RELEASING.md](RELEASING.md) says
how releases are made, and the guide covers
[development](docs/guide.md#development).

## Licence

MPL-2.0. See [LICENSE](LICENSE).
