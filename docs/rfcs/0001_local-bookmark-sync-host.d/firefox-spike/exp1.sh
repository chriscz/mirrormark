#!/bin/bash
. /spike/lib.sh; mkxpi
mkdir -p $NMDIR; echo "$HOSTJSON" > $NMDIR/com.github.chriscz.mirrormark.json
echo '[{"cmd":"ping"},{"cmd":"storage"}]' > ~/cmds.json
$FF --headless -CreateProfile alpha >/dev/null 2>&1
P=$(profdir alpha); echo "profile: $P"
echo "--- first start without addon (create prefs)"; runff "$P" 8
echo "--- drop xpi + user.js ($USERJS)"
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"
printf '%b' "$USERJS" > "$P/user.js"; cat "$P/user.js"
runff "$P" 12
addonstate "$P"; echo "--- host.log"; cat ~/host.log 2>/dev/null | cut -c1-400
grep -v Sandbox /tmp/ff.out | head -30
