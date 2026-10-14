#!/bin/bash
# Q4: keep-alive. The host stays silent and pings at 45, 100 and 160 s; does
# LibreWolf suspend the event page and close the port?
. /spike/lib.sh; mkprobe; nm ~/.mozilla/native-messaging-hosts; echo '[]' > ~/cmds.json; touch ~/silent
$LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"
runlw "$P" 180
grep -E 'START|SENDING|reply|EOF' ~/host.log | cut -c1-120
