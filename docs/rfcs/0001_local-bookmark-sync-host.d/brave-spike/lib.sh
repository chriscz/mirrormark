# Sourced by the exp*.sh scripts; they run as root in spike-brave (exp6
# and exp7 also in spike-chrome, for comparison), started by run.sh.
set -u
export DISPLAY=:99
pgrep Xvfb >/dev/null || { Xvfb :99 -screen 0 1280x800x24 >/dev/null 2>&1 & sleep 1; }
U() { runuser -u u -- env HOME=/home/u DISPLAY=:99 "$@"; }
BRAVE=${BRAVE:-brave-browser}
COMM=${COMM:-brave} # the browser's process name: brave, or chrome
# stop: SIGTERM the browser process (not its children), wait for it to go.
stop() {
  for p in $(pgrep -u u -x "$COMM"); do grep -qa -- --type= /proc/$p/cmdline 2>/dev/null || kill -TERM $p 2>/dev/null; done
  for _ in $(seq 30); do pgrep -u u -x "$COMM" >/dev/null || return 0; sleep 0.5; done
  echo "   (browser still running after 15 s)"
}
# The real extension, packed by mirrormark itself with its per-install key.
if [ ! -f /home/u/.local/share/mirrormark/mirrormark.crx ]; then
  U /spike/mirrormark unpack >/tmp/unpack.out
fi
ID=$(sed -n 's/^Extension ID: //p' /tmp/unpack.out)
CRX=/home/u/.local/share/mirrormark/mirrormark.crx
VER=$(jq -r .version /home/u/.local/share/mirrormark/extension/manifest.json)
chmod -R a+rX /home/u/.local/share/mirrormark
touch /tmp/host.log /tmp/brave.out; chmod 666 /tmp/host.log /tmp/brave.out
extjson() { echo "{\"external_crx\":\"$CRX\",\"external_version\":\"$VER\"}"; }
hostjson() { echo "{\"name\":\"com.github.chriscz.mirrormark\",\"description\":\"spike\",\"path\":\"/spike/host.py\",\"type\":\"stdio\",\"allowed_origins\":[\"chrome-extension://$ID/\"]}"; }
# run UDD SECONDS [args]: start Brave as u, let it run, stop it with SIGTERM.
run() {
  local udd=$1 s=$2; shift 2
  local a=(); [ -n "$udd" ] && a=(--user-data-dir="$udd")
  U $BRAVE --no-sandbox --no-first-run "${a[@]}" "$@" >>/tmp/brave.out 2>&1 &
  sleep "$s"; stop
}
# extstate UDD PROFILE: the extension's entry in the profile's prefs.
extstate() {
  for f in "$1/$2/Preferences" "$1/$2/Secure Preferences"; do
    [ -f "$f" ] && jq -c --arg id "$ID" '.extensions.settings[$id] // empty | {location, state, disable_reasons, creation_flags, from_webstore, was_installed_by_default, install_time: (.first_install_time // .install_time)}' "$f"
  done
  [ -d "$1/$2/Extensions/$ID" ] && echo "unpacked in $2/Extensions/$ID: $(ls "$1/$2/Extensions/$ID")" || echo "not in $2/Extensions"
}
# bound UDD PROFILE DB: did bind.html store DB in the profile's extension storage?
bound() { grep -qa "$3" "$1/$2/Local Extension Settings/$ID/"*.log 2>/dev/null && echo "bound $3" || echo "NOT bound $3"; }
