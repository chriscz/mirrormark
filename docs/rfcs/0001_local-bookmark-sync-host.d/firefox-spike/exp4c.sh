#!/bin/bash
# headless + DBus: does a second invocation with a URL reach the running headless instance?
. /spike/lib.sh; mkxpi
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json; echo '[]' > ~/cmds.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-scan.js "$P/user.js"
$FF --headless --profile "$P" >/dev/null 2>&1 & sleep 6
U=$(grep -o "mirrormark@chriscz.github.io[^,}]*" "$P/prefs.js" | grep -oE '[0-9a-f-]{36}')
timeout 20 $FF --headless --profile "$P" "moz-extension://$U/bind.html#headless-db" >/tmp/o 2>&1; echo "second invocation exit $?"
cat /tmp/o | grep -v Sandbox | head -5; sleep 3; grep -o '"why": "rebind", "db": "[^"]*"' ~/host.log
kill %1; wait
