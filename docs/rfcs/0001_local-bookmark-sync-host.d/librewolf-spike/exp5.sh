#!/bin/bash
# Q5, Q8: binding as internal/firefox Bind does it (cold, and by remoting into
# a running instance), the D-Bus remoting name, and whether the binding in
# storage.local survives a clean restart with LibreWolf's sanitize-on-shutdown.
# Run under: dbus-run-session -- bash /spike/exp5.sh
. /spike/lib.sh; mkxpi; nm ~/.mozilla/native-messaging-hosts; echo '[]' > ~/cmds.json
Xvfb :99 -screen 0 1280x800x24 >/dev/null 2>&1 & export DISPLAY=:99; sleep 1
for n in alpha beta; do $LW -CreateProfile $n >/dev/null 2>&1; P=$(profdir $n)
  mkdir -p "$P/extensions"; cp /tmp/real.xpi "$P/extensions/$ID.xpi"; cp /spike/user-mm.js "$P/user.js"; done
PA=$(profdir alpha); PB=$(profdir beta)
uuid() { grep -o "mirrormark@chriscz.github.io[^,}]*" "$1/prefs.js" 2>/dev/null | grep -oE '[0-9a-f-]{36}'; }
hello() { grep -o '"type": "hello", "db": "[^"]*", "browser": "[^"]*"' ~/host.log | sort | uniq -c; }
echo "=== alpha: start (about:blank), wait for the UUID as Bind does"
$LW --profile "$PA" --remote-debugging-port 9222 about:blank >/tmp/lwa.out 2>&1 & APID=$!
for i in $(seq 1 75); do U=$(uuid "$PA"); [ -n "$U" ] && break; sleep 0.2; done; echo "alpha uuid after ${i}x0.2s: $U"
echo "--- D-Bus names"; dbus-send --session --print-reply --dest=org.freedesktop.DBus /org/freedesktop/DBus org.freedesktop.DBus.ListNames | grep -o '"[^"]*"' | grep -v '"org.freedesktop.DBus"\|"\:'
echo "=== alpha: remoting the bind URL into the running instance"
: > ~/host.log; timeout 20 $LW --profile "$PA" about:blank "moz-extension://$U/bind.html#db-alpha" >/tmp/lwa2.out 2>&1; echo "second invocation exit $?"
sleep 5; hello; grep -v -i sandbox /tmp/lwa2.out | head -3
echo "--- alpha still running: $(kill -0 $APID 2>/dev/null && echo yes || echo no)"
bidi 9222 content 'navigator.userAgent' 2>&1 | head -2
echo "=== beta: first start to install, quit, then cold start with the bind URL"
timeout -s TERM 10 $LW --profile "$PB" about:blank >/dev/null 2>&1; sleep 1; UB=$(uuid "$PB"); echo "beta uuid: $UB"
: > ~/host.log; $LW --profile "$PB" about:blank "moz-extension://$UB/bind.html#db-beta" >/tmp/lwb.out 2>&1 & BPID=$!
sleep 10; hello
echo "=== clean shutdown of both (SIGTERM), then restart each with no URL"
kill -TERM $APID $BPID; wait $APID $BPID 2>/dev/null; sleep 1
grep -o 'privacy.sanitize.pending[^;]*' "$PA/prefs.js" | cut -c1-200
: > ~/host.log
for P in "$PA" "$PB"; do timeout -s TERM 12 $LW --profile "$P" about:blank >/dev/null 2>&1; done; sleep 1
echo "--- hello after restart (binding read from storage.local):"; hello
echo "--- second restart:"; : > ~/host.log; timeout -s TERM 12 $LW --profile "$PA" >/dev/null 2>&1; hello
