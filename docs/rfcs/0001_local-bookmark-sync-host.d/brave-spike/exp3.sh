#!/bin/bash
# Q3: which native host manifest directories does Brave read? One candidate
# at a time, fresh data dir each, extension from the external JSON; the
# profile is bound by opening bind.html, so the extension connects.
. /spike/lib.sh
mkdir -p /opt/brave.com/brave/extensions; extjson >/opt/brave.com/brave/extensions/$ID.json
i=0
for dir in /etc/opt/chrome/native-messaging-hosts /etc/chromium/native-messaging-hosts \
    /etc/brave/native-messaging-hosts /etc/opt/brave/native-messaging-hosts /etc/opt/brave.com/native-messaging-hosts \
    /etc/BraveSoftware/native-messaging-hosts /etc/opt/BraveSoftware/native-messaging-hosts USERDIR; do
  i=$((i+1)); udd=/tmp/nm$i
  [ $dir = USERDIR ] && { dir=$udd/NativeMessagingHosts; U mkdir -p $dir; }
  mkdir -p $dir; hostjson >$dir/com.github.chriscz.mirrormark.json; chmod a+r $dir/*
  U $BRAVE --no-sandbox --no-first-run --user-data-dir=$udd --profile-directory=Default "chrome-extension://$ID/bind.html#nm$i" >>/tmp/brave.out 2>&1 &
  sleep 5   # the first open can beat the extension's install; open it again
  U $BRAVE --no-sandbox --no-first-run --user-data-dir=$udd --profile-directory=Default "chrome-extension://$ID/bind.html#nm$i" >>/tmp/brave.out 2>&1
  sleep 6
  echo "== $dir: $(grep -c "\"db\": \"nm$i\"" /tmp/host.log) hello(s); $(bound $udd Default nm$i); $(extstate $udd Default | tail -1)"
  stop
  rm $dir/com.github.chriscz.mirrormark.json
done
echo "== host log (first lines)"; grep -E 'START|hello' /tmp/host.log | cut -c1-300 | head -6
