#!/bin/bash
# Q6: the Firefox spike's bookmark probe, in LibreWolf and in Developer
# Edition from the same image, so the results can be diffed.
. /spike/lib.sh; mkprobe; nm ~/.mozilla/native-messaging-hosts
for B in /opt/librewolf/librewolf /opt/devedition/firefox; do
  rm -rf ~/.config/librewolf ~/.config/mozilla ~/.mozilla/firefox; : > ~/host.log; echo '[{"cmd":"bmtest"}]' > ~/cmds.json
  $B --headless -CreateProfile alpha >/dev/null 2>&1
  I=$(ls ~/.config/librewolf/librewolf/profiles.ini ~/.config/mozilla/firefox/profiles.ini 2>/dev/null); P=$(dirname $I)/$(awk -F= '$1=="Path"{print $2}' $I | head -1)
  mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"
  LW=$B runlw "$P" 15
  grep -m1 'RECV {"type": "reply"' ~/host.log | sed 's/^.*RECV //' | jq '.r' > /tmp/bm.$(basename $B).json
  grep '"type": "event"' ~/host.log | sed 's/^.*RECV //' | jq -c '[.e, .a]' | cut -c1-200 > /tmp/ev.$(basename $B).txt
done
echo "=== LibreWolf result"; cat /tmp/bm.librewolf.json | head -150
echo "=== diff Developer Edition -> LibreWolf (ids masked)"
for f in bm.firefox.json bm.librewolf.json ev.firefox.txt ev.librewolf.txt; do sed -E 's/"[A-Za-z0-9_-]{12}"/"<id>"/g; s/[0-9]{13}/<time>/g' /tmp/$f > /tmp/m.$f; done
diff /tmp/m.bm.firefox.json /tmp/m.bm.librewolf.json && echo "probe results identical"
diff /tmp/m.ev.firefox.txt /tmp/m.ev.librewolf.txt && echo "events identical"
