#!/bin/bash
# Where Firefox Developer Edition or LibreWolf records the add-on mirrormark
# drops into a profile, and when it writes it: XPI written but not started,
# installed and enabled, disabled and removed by the user, and refused for
# want of a signature. B is the browser binary.
[ "$(id -u)" = 0 ] && exec runuser -u u -- env HOME=/home/u B="${B:-/opt/librewolf/librewolf}" "$0"
set -u
ID=mirrormark@chriscz.github.io
(cd /real && rm -f /tmp/real.xpi && zip -qX /tmp/real.xpi manifest.json background.js bind.html bind.js popup.html popup.js)
st() { # dir: the add-on's entry in extensions.json, its UUID in prefs.js, the XPI
  local d=$1 e u
  e=$(jq -c --arg id $ID '[.addons[] | select(.id==$id) | {active, userDisabled, appDisabled, softDisabled, signedState, location, version, visible}] | .[0]' "$d/extensions.json" 2>/dev/null || echo "no extensions.json")
  u=$(grep -o 'mirrormark@chriscz.github.io\\\\\?":\\\\\?"[0-9a-f-]*' "$d/prefs.js" 2>/dev/null | sed 's/.*"//' )
  echo "extensions.json ($(stat -c %y "$d/extensions.json" 2>/dev/null | cut -c12-19)): $e | uuid: ${u:-none} | xpi: $([ -f "$d/extensions/$ID.xpi" ] && echo yes || echo no)"
}
watch() { # dir seconds: st each time it changes, every 0.5 s
  local last="" now t0=$(date +%s%N)
  for i in $(seq $(($2 * 2))); do
    sleep 0.5; now=$(st "$1")
    [ "$now" != "$last" ] && { echo "   +$(( ($(date +%s%N) - t0) / 100000000 ))ds $now"; last=$now; }
  done
}
stop() { pkill -TERM -f -- "--profile $1" ; for _ in $(seq 30); do pgrep -f -- "--profile $1" >/dev/null || return 0; sleep 0.5; done; echo "   (still running)"; }
start() { $B --headless --profile "$1" "${@:2}" >>/tmp/b.out 2>&1 & }
mm() { MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 python3 /spike/marionette.py "$1"; }
prefs() { # dir sig: the user.js install writes, with signatures.required = sig
  printf 'user_pref("xpinstall.signatures.required", %s);\nuser_pref("extensions.autoDisableScopes", 14);\nuser_pref("extensions.startupScanScopes", 1);\nuser_pref("remote.prefs.recommended", false);\n' $2 > "$1/user.js"
}
echo "== $($B --version)"
$B --headless -CreateProfile "alpha /tmp/alpha" >/dev/null 2>&1; P=/tmp/alpha
echo "== 1. the profile has run once; nothing installed"
start $P; sleep 8; stop $P; echo "   $(st $P)"
echo "== 2. XPI and user.js written, not started since"
mkdir -p $P/extensions; cp /tmp/real.xpi $P/extensions/$ID.xpi; prefs $P false
echo "   $(st $P)"
echo "== 3. start: when do the files show the install?"
start $P; watch $P 20; stop $P; echo "   after a clean close: $(st $P)"
echo "== 4. disable, enable, remove, as in about:addons (with Marionette)"
MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 start $P --marionette -remote-allow-system-access; sleep 3
for a in "disable" "enable" "uninstall"; do
  echo "-- $a"
  mm "AddonManager.getAddonByID('$ID').then(a => a.$a()).then(() => AddonManager.getAddonByID('$ID')).then(a => resolve(a ? {active: a.isActive, userDisabled: a.userDisabled} : 'gone'), e => resolve(String(e)))"
  watch $P 6
done
stop $P; echo "   after a clean close: $(st $P)"
echo "== 5. start again after the remove"
start $P; sleep 10; stop $P; echo "   $(st $P)"
echo "== 6. XPI written again, signatures required (as after uninstall's reset)"
cp /tmp/real.xpi $P/extensions/$ID.xpi; prefs $P true
start $P; watch $P 15; stop $P; echo "   after a clean close: $(st $P)"
echo "== 7. a profile that never ran"
$B --headless -CreateProfile "beta /tmp/beta" >/dev/null 2>&1; ls /tmp/beta; echo "   $(st /tmp/beta)"
