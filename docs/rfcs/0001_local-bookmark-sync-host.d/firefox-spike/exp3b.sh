#!/bin/bash
. /spike/lib.sh; mkxpi
for d in ~/.config/mozilla/native-messaging-hosts ~/.mozilla/native-messaging-hosts; do
  rm -rf ~/.config/mozilla ~/.mozilla; : > ~/host.log; echo '[]' > ~/cmds.json
  $FF --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha)
  mkdir -p $d; echo "$HOSTJSON" > $d/com.github.chriscz.mirrormark.json
  mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-scan.js "$P/user.js"
  runff "$P" 8; echo "$d (profile $P): host starts=$(grep -c START ~/host.log)"
done
