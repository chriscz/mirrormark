# Sourced by exp.sh (and by hand when debugging), as root in spike-firejail.
# The browser runs as user u under firejail, the way firecfg sets it up;
# mirrormark install and serve run as u outside the jail.
set -u
MM=/spike-bin/mirrormark
firecfg >/dev/null 2>&1; echo u >>/etc/firejail/firejail.users
Xvfb :99 -screen 0 1280x800x24 >/dev/null 2>&1 & sleep 1
# Chrome and Brave need --no-sandbox in Docker. firecfg's symlink in
# /usr/local/bin runs the next brave-browser on PATH in the jail: these.
mkdir -p /spike-real
for c in brave-browser google-chrome; do
  printf '#!/bin/sh\nexec /usr/bin/%s --no-sandbox --no-first-run "$@"\n' $c >/spike-real/$c; chmod 755 /spike-real/$c
done
DBUS=$(runuser -u u -- dbus-daemon --session --fork --print-address)
U() { runuser -u u -- env HOME=/home/u DISPLAY=:99 container=docker PATH=/usr/local/bin:/spike-real:/usr/bin:/bin \
  DBUS_SESSION_BUS_ADDRESS=$DBUS "$@"; }
local_() { mkdir -p /home/u/.config/firejail; printf '%s\n' "$@" >/home/u/.config/firejail/$LOCAL.local; chown -R u: /home/u/.config; echo "## $LOCAL.local: $*"; }
stopb() {
  for p in $(pgrep -u u -x "$COMM"); do grep -qa -- --type= /proc/$p/cmdline 2>/dev/null || kill -TERM $p 2>/dev/null; done
  for _ in $(seq 30); do pgrep -u u -x "$COMM" >/dev/null || return 0; sleep 0.5; done; echo "(still running)"
}

