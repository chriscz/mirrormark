#!/bin/bash
# Q1: default data dir, env overrides, profile list in Local State.
. /spike/lib.sh
run "" 8
echo "== default:"; ls -d /home/u/.config/*/* 2>/dev/null
U mkdir -p /tmp/xdg /tmp/cch
runuser -u u -- env HOME=/home/u DISPLAY=:99 XDG_CONFIG_HOME=/tmp/xdg $BRAVE --no-sandbox --no-first-run >/dev/null 2>&1 & sleep 8; stop
echo "== XDG_CONFIG_HOME=/tmp/xdg:"; find /tmp/xdg -maxdepth 3 -name 'Local State'
runuser -u u -- env HOME=/home/u DISPLAY=:99 CHROME_CONFIG_HOME=/tmp/cch $BRAVE --no-sandbox --no-first-run >/dev/null 2>&1 & sleep 8; stop
echo "== CHROME_CONFIG_HOME=/tmp/cch:"; find /tmp/cch -maxdepth 3 -name 'Local State'
run "" 8 --profile-directory="Profile 1"
echo "== Local State info_cache:"
jq -c '.profile.info_cache | to_entries[] | {dir: .key, name: .value.name}' "/home/u/.config/BraveSoftware/Brave-Browser/Local State"
ls /home/u/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts; echo "== $(brave-browser --version)"
