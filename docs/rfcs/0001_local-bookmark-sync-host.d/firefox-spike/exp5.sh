#!/bin/bash
. /spike/lib.sh; mkxpi
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json
echo '[{"cmd":"bmtest"}]' > ~/cmds.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-basic.js "$P/user.js"
runff "$P" 15
grep -m1 'RECV {"type": "reply"' ~/host.log | sed 's/^.*RECV //' > /tmp/r.json; jq '.r' /tmp/r.json
echo "--- events"; grep '"type": "event"' ~/host.log | sed 's/^.*RECV //' | jq -c '[.e, .a]' | cut -c1-300
