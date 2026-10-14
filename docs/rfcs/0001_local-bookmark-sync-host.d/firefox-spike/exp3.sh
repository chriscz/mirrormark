#!/bin/bash
# keep-alive: the host stays silent; does Firefox suspend the MV3 event page and close the port?
. /spike/lib.sh; mkxpi
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json
echo '[]' > ~/cmds.json; touch ~/silent
$FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-basic.js "$P/user.js"
runff "$P" 175
grep -E 'START|SENDING|reply|EOF|hello' ~/host.log | cut -c1-120
