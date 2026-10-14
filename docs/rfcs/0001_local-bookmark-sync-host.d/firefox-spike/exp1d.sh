#!/bin/bash
# install while Firefox is running in that profile, then one restart
. /spike/lib.sh; mkxpi
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json; echo '[]' > ~/cmds.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
$FF --headless --profile "$P" >/dev/null 2>&1 & sleep 6
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-scan.js "$P/user.js"
sleep 4; echo "while running: host starts=$(grep -c START ~/host.log 2>/dev/null)"
kill %1; wait; sleep 1
runff "$P" 8; echo "after one restart: host starts=$(grep -c START ~/host.log)"; addonstate "$P"
