#!/bin/bash
# Firefox under firejail, step by step: run.sh firefox EXP=dbg
. /spike/lib.sh
COMM=firefox-bin
mkdir -p /home/u/.mozilla/firefox; chown -R u: /home/u
U firejail /opt/firefox-devedition/firefox > /tmp/b1.out 2>&1 & sleep 20; stopb
tail -5 /tmp/b1.out; ls /home/u/.mozilla/firefox
P=$(ls -d /home/u/.mozilla/firefox/*.dev-edition-default)
U $MM install --browser firefox --profile $(basename $P) >/dev/null 2>&1
ls -la $P/extensions; cat $P/user.js
U firejail /opt/firefox-devedition/firefox > /tmp/b2.out 2>&1 & sleep 25
tail -20 /tmp/b2.out
U firejail --ls=$(pgrep -u u -x firejail|head -1) $P/extensions 2>&1 | head
pgrep -u u -a firefox | head -3
stopb
jq -c '.addons[]|{id,active,location,path}' $P/extensions.json 2>&1 | head
