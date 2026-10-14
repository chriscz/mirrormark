#!/bin/bash
# single start after dropping the xpi: does the background run in that same start?
. /spike/lib.sh; mkxpi
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json
echo '[{"cmd":"ping"}]' > ~/cmds.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1
P=$(profdir alpha)
[ -z "$FRESH" ] && runff "$P" 6
[ -n "$RMCACHE" ] && rm -fv "$P/addonStartup.json.lz4" $RMEXTRA
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/${USERJSFILE:-user-basic.js} "$P/user.js"
for i in 1 2 3; do
 echo "== start $i"; : > ~/host.log
 runff "$P" ${WAIT:-15}
 addonstate "$P"; grep -c RECV ~/host.log; grep -o '"why": "[^"]*"' ~/host.log
done
