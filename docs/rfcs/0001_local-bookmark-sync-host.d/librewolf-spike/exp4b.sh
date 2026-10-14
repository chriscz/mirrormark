#!/bin/bash
# Q4: XDG root: is the add-on active, and what does the console say about the host?
. /spike/lib.sh; mkprobe; echo '[{"cmd":"ping"}]' > ~/cmds.json
$LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha); echo "profile $P"
nm ~/.mozilla/native-messaging-hosts
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"
MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 $LW --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/tmp/lw.out 2>&1 &
sleep 10
bidi 9222 chrome 'Services.dirsvc.get("XREUserNativeManifests", Ci.nsIFile).path' 'Services.dirsvc.get("UserAppDataDir"||"", Ci.nsIFile)?.path ?? null' \
 'Services.console.getMessageArray().map(m=>m.message||(m.sourceName+": "+m.errorMessage)).filter(s=>/native|mirrormark|moz-extension/i.test(s)).map(s=>s.slice(0,250))' 2>&1
kill %1; wait; addonstate "$P"; echo "host starts=$(grep -c START ~/host.log 2>/dev/null)"
