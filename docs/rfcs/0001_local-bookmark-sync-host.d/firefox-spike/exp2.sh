#!/bin/bash
# mirrormark's real extension on Firefox: (a) manifest + gecko id only, (b) + background.scripts
. /spike/lib.sh
mkdir -p ~/.mozilla/native-messaging-hosts; echo "$HOSTJSON" > ~/.mozilla/native-messaging-hosts/com.github.chriscz.mirrormark.json
for v in a b; do
  rm -rf /tmp/r; cp -r /spike/real /tmp/r
  if [ $v = a ]; then jq '.browser_specific_settings={gecko:{id:"mirrormark@chriscz.github.io"}}' /spike/real/manifest.json > /tmp/r/manifest.json
  else jq '.browser_specific_settings={gecko:{id:"mirrormark@chriscz.github.io"}} | .background.scripts=["background.js"]' /spike/real/manifest.json > /tmp/r/manifest.json; fi
  (cd /tmp/r; rm -f /tmp/r.xpi; zip -qr /tmp/r.xpi .)
  rm -rf ~/.config/mozilla; : > ~/host.log
  $FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
  mkdir -p "$P/extensions"; cp /tmp/r.xpi "$P/extensions/$ID.xpi"; cp /spike/user-basic.js "$P/user.js"
  MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1 $FF --headless --profile "$P" --remote-debugging-port 9222 --remote-allow-system-access >/tmp/ff.out 2>&1 &
  sleep 6
  echo "=== variant $v"
  /spike/bidi/bidi 9222 chrome 'Services.console.getMessageArray().map(m=>m.message||(m.sourceName+": "+m.errorMessage)).filter(s=>/mirrormark|moz-extension|manifest|background/i.test(s)).map(s=>s.slice(0,300))' \
   '(()=>{const p=WebExtensionPolicy.getByID("mirrormark@chriscz.github.io"); return p && {bg: p.extension?.backgroundState, warnings: p.extension?.warnings}})()'
  grep -c START ~/host.log
  kill %1; wait
done
