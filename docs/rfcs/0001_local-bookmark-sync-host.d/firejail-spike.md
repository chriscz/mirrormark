# Spike: mirrormark with browsers under firejail

Run on 2026-10-16 in Docker, on the dev image (`mirrormark-dev`: Google
Chrome 155.0.8059.79, Brave 1.97.57, Firefox Developer Edition 159.0b2,
LibreWolf 158.0-1) plus Ubuntu 24.04's `firejail` and `firejail-profiles`
0.9.72 and `xdg-dbus-proxy`. The browsers ran under firejail as an ordinary
user, set up the way `firecfg` sets up a desktop; `mirrormark install` and
`mirrormark serve` (the real binary, built from the repo) ran as that user
outside the jail. Nothing was installed on the host machine. Scripts and
image: [firejail-spike/](firejail-spike/), run with
`MM=build/mirrormark firejail-spike/run.sh brave|chrome|firefox|librewolf`.

The question came from a desktop: Brave started through firecfg's
`/usr/local/bin/brave-browser` never installed mirrormark, and adding
`whitelist ${HOME}/.local/share/mirrormark` to
`~/.config/firejail/brave.local` fixed it.

## Result

| Browser | Under firejail, without a rule | Rule that fixes it | `.local` file |
| --- | --- | --- | --- |
| Brave | not installed: the CRX in `~/.local/share/mirrormark` is hidden | `whitelist ${HOME}/.local/share/mirrormark/mirrormark.crx` | `brave.local` |
| Chrome | not installed: the CRX and `/usr/share/google-chrome/extensions` are hidden | that line **and** `whitelist /usr/share/google-chrome/extensions`; the first alone is not enough | `google-chrome.local` |
| Developer Edition | installs, binds, connects | none | (`firefox.local`) |
| LibreWolf | installs, binds, connects | none | (`librewolf.local`) |

- The user's rule, `whitelist ${HOME}/.local/share/mirrormark`, works too,
  but shows the jailed browser the whole data directory: the databases,
  with every profile's token, and `serve.lock`. Whitelisting the CRX file
  alone works as well, and then the jail shows only `mirrormark.crx` in
  that directory: `profiles/*.yaml` does not exist there, and a file written
  beside the CRX stays in the jail's tmpfs. The jailed browser can still
  write the CRX itself; a changed CRX no longer matches the key, so the
  browser would refuse it rather than run it.
- With the rule, `install --profile Default` bound Brave and Chrome. The bind
  page was opened by the jailed browser (`firejail /spike-real/brave-browser
  --profile-directory=Default chrome-extension://…`), and the server logged
  `connected` from it, so a jailed extension reaches `ws://127.0.0.1`.
- Brave's external JSON in `/opt/brave.com/brave/extensions` is visible in
  the jail; Chrome's in `/usr/share/google-chrome/extensions` is not, because
  `chromium-common.profile` includes `whitelist-usr-share-common.inc`, which
  shows only listed parts of `/usr/share`.
- Firefox and LibreWolf keep the XPI in the profile, which their firejail
  profiles show. Both bound with the jailed browser running (install starts
  the binary outside the jail; the jailed one took the page) and with it
  closed.
- Firejail reads `.local` files only from `~/.config/firejail` and
  `/etc/firejail`. With `XDG_CONFIG_HOME=/home/u/xdg`, a
  `/home/u/xdg/firejail/brave.local` was not read (`--debug` lists no read
  of it); the binary names only `%s/.config/firejail`.

## Layout and profile names

- `firecfg` makes `/usr/local/bin/brave-browser`, `brave-browser-stable`,
  `google-chrome` and `google-chrome-stable` symlinks to `/usr/bin/firejail`.
  `which -a brave-browser` lists `/usr/local/bin` first, as on the desktop.
  It makes none for Developer Edition or LibreWolf, which come as tarballs
  in `/opt` and are not on `PATH`.
- Firejail run as a symlink takes the program's name from `argv[0]`, finds
  the next program of that name on `PATH` (not itself) and loads the profile
  of that name (`run_symlink.c`).
- Profiles read: `brave-browser` → `brave-browser.profile` (includes
  `brave-browser.local`) → `brave.profile` (`brave.local`) →
  `chromium-common.profile`. `google-chrome` → `google-chrome.profile`
  (`google-chrome.local`); `google-chrome-stable.profile` includes it.
  `/opt/firefox-devedition/firefox` → `firefox.profile`; there is a
  `firefox-developer-edition.profile`, but only a command of that name loads
  it. `/opt/librewolf/librewolf` → `librewolf.profile`. `brave.local` and
  `google-chrome.local` are the files every command of those browsers reads.

## Where the data directory may be

With `XDG_DATA_HOME` moving mirrormark's data out of the home directory, the
Chromium profiles show `/srv`, `/opt` and `/tmp` paths as they are, hide
`/var/tmp`, and blacklist `/mnt` and `/media` (where `whitelist` does not
help; it needs `noblacklist`). Only the home directory case was tried with
real browsers.

## Running firejail in Docker

Firejail runs any program unsandboxed when it finds no kernel thread among
PIDs 1 to 10, as in a container ("an existing sandbox was detected"), unless
`container=docker` (or `lxc`, …) is set (`no_sandbox.c`). With it set, it
needs `--cap-add SYS_ADMIN --security-opt seccomp=unconfined
--security-opt apparmor=unconfined` for its mounts; without any one of the
three it fails (`clone: Operation not permitted`, or `Permission denied`
mounting `/run/firejail/mnt`). `--privileged` is not needed. Chrome and
Brave still need `--no-sandbox`, which a wrapper on `PATH` after
`/usr/local/bin` adds.

## Not verified

- Other firejail versions and distributions' profiles; newer profiles may
  whitelist more.
- Data directories outside the home directory with real browsers.
- Fresh installs of Firefox 147 and later and of LibreWolf use
  `~/.config/mozilla` and `~/.config/librewolf`, which the 0.9.72 profiles
  hide, so the jailed browser loses its profile when it exits. The spike
  used the legacy roots; this is firejail's problem, not mirrormark's.
- Wayland and a real desktop D-Bus.

## The scripts

- `Dockerfile`: the dev image plus firejail, its profiles and
  `xdg-dbus-proxy`.
- `run.sh B`: one run for browser B in a throwaway container with the
  privileges above. `EXP=name` runs `name.sh` instead of `exp.sh`.
- `lib.sh`: runs `firecfg`, starts Xvfb and a session bus, and defines `U`
  (run as the user inside the container), `stopb` and `local_` (write the
  `.local` file).
- `exp.sh`: prints the layout and the profiles firejail reads, then installs
  and binds B under firejail, without a rule and with each rule, and lists
  what the jail shows of the data directory. The first run whitelisted the
  data directory; the CRX-only rule replaced it after review.
- `dbg.sh`: Developer Edition step by step, kept for debugging.
