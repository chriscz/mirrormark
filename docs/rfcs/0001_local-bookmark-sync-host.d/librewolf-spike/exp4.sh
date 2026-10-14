#!/bin/bash
# Q4: which per-user host-manifest directory LibreWolf reads, host argv.
# Probe extension (connects at once), mirrormark's user.js.
. /spike/lib.sh; mkprobe; echo '[{"cmd":"ping"}]' > ~/cmds.json
for d in ~/.mozilla/native-messaging-hosts ~/.librewolf/native-messaging-hosts ~/.config/librewolf/native-messaging-hosts ~/.config/mozilla/native-messaging-hosts ~/.config/librewolf/librewolf/native-messaging-hosts; do
  rm -rf ~/.mozilla ~/.librewolf ~/.config; : > ~/host.log
  $LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha); nm $d
  mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"
  runlw "$P" 15; echo "$d: host starts=$(grep -c START ~/host.log) $(addonstate "$P")"
done
rm -rf ~/.librewolf ~/.config; nm ~/.librewolf/native-messaging-hosts; mkdir -p ~/.librewolf; printf '[General]\nStartWithLastProfile=1\n' > ~/.librewolf/profiles.ini
$LW --headless -CreateProfile alpha >/dev/null 2>&1; P=$(profdir alpha); echo "legacy root, profile $P"
mkdir -p "$P/extensions"; cp /tmp/spike.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"; : > ~/host.log
runlw "$P" 15; echo "~/.librewolf/native-messaging-hosts with legacy root: host starts=$(grep -c START ~/host.log)"
grep -E 'START|RECV' ~/host.log | cut -c1-300 | head -4
