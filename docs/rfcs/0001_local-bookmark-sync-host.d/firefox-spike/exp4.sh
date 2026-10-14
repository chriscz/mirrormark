#!/bin/bash
# binding by URL. Xvfb, real (non-headless) Firefox so remoting works.
. /spike/lib.sh; mkxpi
Xvfb :99 -screen 0 1280x800x24 >/dev/null 2>&1 & export DISPLAY=:99; sleep 1
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json
echo '[{"cmd":"storage"}]' > ~/cmds.json
UUID=11111111-2222-4333-8444-555555555555
for n in alpha beta; do $FF -CreateProfile $n >/dev/null 2>&1; P=$(profdir $n)
  mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-basic.js "$P/user.js"; done
PA=$(profdir alpha); PB=$(profdir beta)
# alpha: pin the uuid via user.js; beta: let Firefox choose and read it from prefs.js
echo "user_pref(\"extensions.webextensions.uuids\", \"{\\\"$ID\\\":\\\"$UUID\\\"}\");" >> "$PA/user.js"
cat "$PA/user.js"
echo "=== cold start of alpha with bind URL ($FFARGS)"
: > ~/host.log
$FF $FFARGS --profile "$PA" about:blank "moz-extension://$UUID/bind.html#chrome-alpha" >/tmp/ffa.out 2>&1 &
sleep 12
grep -E 'why|reply' ~/host.log | cut -c1-200
echo "=== start beta (no URL), read uuid from prefs.js while running"
: > ~/host.log
$FF $FFARGS --profile "$PB" >/tmp/ffb.out 2>&1 &
sleep 10
grep -o 'extensions.webextensions.uuids.*' "$PB/prefs.js" | cut -c1-300
BU=$(grep -o "mirrormark@chriscz.github.io[^,}]*" "$PB/prefs.js" | grep -oE '[0-9a-f-]{36}'); echo "beta uuid from prefs.js: $BU"
echo "=== open bind URL in running beta ($BU)"
: > ~/host.log
timeout 20 $FF $FFARGS --profile "$PB" "moz-extension://$BU/bind.html#chrome-beta" >/tmp/ffb2.out 2>&1; echo "remote invocation exit $?"
sleep 5
grep -E 'why|reply|PARENT FDS' ~/host.log | cut -c1-300
cat /tmp/ffb2.out | grep -v Sandbox | head
echo "=== also open bind URL in running alpha again"
: > ~/host.log
timeout 20 $FF $FFARGS --profile "$PA" "moz-extension://$UUID/bind.html#chrome-alpha2" >/dev/null 2>&1; echo "exit $?"; sleep 5
grep -E 'why|reply|PARENT FDS|ALLENV' ~/host.log | cut -c1-600
pkill -TERM firefox; sleep 3
echo "=== prefs.js uuids after shutdown (alpha):"; grep -o 'extensions.webextensions.uuids.*' "$PA/prefs.js" | cut -c1-400
