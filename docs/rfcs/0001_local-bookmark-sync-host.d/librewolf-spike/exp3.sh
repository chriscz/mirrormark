#!/bin/bash
# Q2: the drop-in without BiDi (its recommended prefs set autoDisableScopes 0).
# Real XPI, three starts each; ADS = autoDisableScopes, SIG = signatures pref
# (empty: line left out), FRESH=1: no start before the drop.
. /spike/lib.sh; mkxpi; nm ~/.mozilla/native-messaging-hosts; echo '[]' > ~/cmds.json
for c in "14 false 1" "14 false 0" "15 false 1" "15 false 0" "14 - 0" "14 true 0"; do
  set -- $c; ADS=$1 SIG=$2 FRESH=$3
  rm -rf ~/.config/librewolf
  $LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
  [ $FRESH = 0 ] && runlw "$P" 6
  mkdir -p "$P/extensions"; cp /tmp/real.xpi "$P/extensions/$ID.xpi"
  { [ $SIG != - ] && echo "user_pref(\"xpinstall.signatures.required\", $SIG);"
    echo "user_pref(\"extensions.autoDisableScopes\", $ADS);"; echo 'user_pref("extensions.startupScanScopes", 1);'; } > "$P/user.js"
  echo "== autoDisableScopes=$ADS signatures.required=$SIG fresh=$FRESH"
  for i in 1 2; do runlw "$P" 12; echo "start $i: $(addonstate "$P")"; done
done
