#!/bin/bash
# Q3b: per-user host manifest dirs: in a non-default data dir, in the
# default one (seen by a non-default data dir?), and with the default dir.
. /spike/lib.sh
mkdir -p /opt/brave.com/brave/extensions; extjson >/opt/brave.com/brave/extensions/$ID.json
DEF=/home/u/.config/BraveSoftware/Brave-Browser
bindit() { # udd db
  local a=(); [ -n "$1" ] && a=(--user-data-dir=$1)
  U $BRAVE --no-sandbox --no-first-run "${a[@]}" --enable-logging=stderr --v=1 --profile-directory=Default "chrome-extension://$ID/bind.html#$2" >>/tmp/b-$2.out 2>&1 &
  sleep 5
  U $BRAVE --no-sandbox --no-first-run "${a[@]}" --profile-directory=Default "chrome-extension://$ID/bind.html#$2" >>/tmp/brave.out 2>&1
  sleep 6
  echo "== $2: $(grep -c "\"db\": \"$2\"" /tmp/host.log) hello(s); $(bound ${1:-$DEF} Default $2)"
  grep -i -E 'native|messaging' /tmp/b-$2.out | grep -v -i 'dbus' | cut -c1-250 | head -5
  stop
}
U mkdir -p /tmp/a/NativeMessagingHosts; hostjson >/tmp/a/NativeMessagingHosts/com.github.chriscz.mirrormark.json
U ls -la /tmp/a/NativeMessagingHosts
bindit /tmp/a a-own-dir
rm /tmp/a/NativeMessagingHosts/*
U mkdir -p $DEF/NativeMessagingHosts; hostjson >$DEF/NativeMessagingHosts/com.github.chriscz.mirrormark.json
bindit /tmp/b b-default-dir-seen-from-other
bindit "" c-default-dir
