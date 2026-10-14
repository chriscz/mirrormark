#!/bin/bash
# Q2 (existing and new profiles), Q4 (binding, cold and running), Q3 (port
# keeps the worker alive), Q5 (bookmark tree), Q6 (CDP pipe), Q7 (uninstall).
. /spike/lib.sh
P=/tmp/p
hellos() { grep -c "\"db\": \"$1\"" /tmp/host.log; }
try() { # profile db: open bind.html once, report whether it bound within 4 s
  U $BRAVE --no-sandbox --no-first-run --user-data-dir=$P --profile-directory="$1" "chrome-extension://$ID/bind.html#$2" >>/tmp/brave.out 2>&1 &
  sleep 4; echo "   open $1#$2: $(bound $P "$1" $2), hellos after 4 s: $(hellos $2)"
  sleep 4; echo "     hellos after 8 s: $(hellos $2)"
}
echo "== 1. two profiles exist before install"
run $P 8; run $P 8 --profile-directory="Profile 1"
mkdir -p /opt/brave.com/brave/extensions; extjson >/opt/brave.com/brave/extensions/$ID.json
mkdir -p /etc/opt/chrome/native-messaging-hosts; hostjson >/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json
echo "== 2. bind Default, Brave cold"
try Default d1; try Default d2
echo "== 3. bind Profile 1 with Brave running in Default"
try "Profile 1" p1; try "Profile 1" p2
echo "== 4. rebind Default, Brave running, extension already there"
try Default d3
stop
echo "== 5. bind Profile 1 cold, extension already there"
try "Profile 1" p3
stop
echo "== 6. a new profile after install"
run $P 10 --profile-directory="Profile 2"
for pr in Default "Profile 1" "Profile 2"; do echo "   $pr: $(extstate $P "$pr" | tr '\n' ' ')"; done
echo "   host argv: $(grep START /tmp/host.log | head -1 | cut -d' ' -f5-)"
echo "== 7. CDP over a pipe, Default"
(echo "ID = \"$ID\""; cat /spike/tree.py) >/tmp/tree.py; chmod a+r /tmp/tree.py
: >/tmp/host.log
U python3 /spike/cdp.py 6 /tmp/tree.py -- $BRAVE --no-sandbox --no-first-run --user-data-dir=$P --profile-directory=Default about:blank
echo "   host log:"; cut -c1-200 /tmp/host.log
echo "== 8. uninstall: delete the JSON, restart"
rm /opt/brave.com/brave/extensions/$ID.json
run $P 10; run $P 10 --profile-directory="Profile 1"
for pr in Default "Profile 1"; do echo "   $pr: $(extstate $P "$pr" | tr '\n' ' ')"; done
grep -h -o '"external_uninstalls":[^]]*]' $P/Default/Preferences $P/Default/Secure\ Preferences 2>/dev/null
echo "== 9. Default again"
run $P 15
echo "   Default: $(extstate $P Default | tr '\n' ' ')"
