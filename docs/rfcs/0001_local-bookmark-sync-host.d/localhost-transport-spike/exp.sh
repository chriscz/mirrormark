#!/bin/bash
# One run, as root inside spike-lt (started by run.sh). Environment:
#   B        chrome | brave | firefox | librewolf
#   HP       host_permissions: none | http ("http://127.0.0.1/*") | ws
#            ("ws://127.0.0.1/*") | both
#   PERIODIC minutes for a periodic alarm, 0 = none
#   PING     server ping interval (Go duration), 0 = silent
#   DUR      seconds to run; RESTART seconds at which the server is killed
#            (restarted 15 s later), 0 = never
#   WS       WebSocket base URL for the extension (default ws://127.0.0.1:8765)
#   FFPREFS  extra user.js lines for Firefox and LibreWolf
#   CSP      if set, the manifest's content_security_policy.extension_pages;
#            self = "script-src 'self'"
#   KA       keep-alive attempt on each server message: storage | api (see bg-body.js)
#   MOZLOG   MOZ_LOG modules for Firefox and LibreWolf, logged to /out/moz*
#   PAGE     with PAGES=1, open only this one: pub | pubtls | ip | lo
#   CHFLAGS  extra Chrome/Brave flags
#   PAGES    1 = also open the web page from 203.0.113.5:8001 (needs
#            --cap-add NET_ADMIN), <container ip>:8001 and 127.0.0.1:8001
set -u
{ # read the whole script before running it, so editing it cannot break a run
: "${WS:=ws://127.0.0.1:8765}" "${FFPREFS:=}" "${MOZLOG:=}" "${CSP:=}" "${KA:=}" "${HP:=none}" "${PERIODIC:=0}" "${PING:=0}" "${DUR:=60}" "${RESTART:=0}" "${PAGES:=1}" "${PAGE:=}" "${CHFLAGS:=}"
export DISPLAY=:99
Xvfb :99 -screen 0 1280x900x24 >/dev/null 2>&1 & sleep 1
U() { runuser -u u -- env HOME=/home/u DISPLAY=:99 "$@"; }
O=/out; mkdir -p $O; chmod 777 $O
T0=$(date +%s); el() { echo $(( $(date +%s) - T0 )); }
note() { echo "{\"t\":\"$(date +%T.000)\",\"kind\":\"script\",\"el\":$(el),\"msg\":\"$*\"}" >> $O/server.log; }
shot() { xwd -root -silent | convert xwd:- png:$O/shot-$1.png 2>/dev/null; }
IP=$(hostname -I | awk '{print $1}')
srv() { /bin-spike/server -tls 0.0.0.0:8443 -ping "$PING" -log $O/server.log >>$O/server.out 2>&1 & SRV=$!; }

# The extension: manifest per run, CFG prepended to the background.
[ "$CSP" = self ] && CSP="script-src 'self'"
case $HP in none) HPJ='[]';; http) HPJ='["http://127.0.0.1/*"]';; ws) HPJ='["ws://127.0.0.1/*"]';;
  both) HPJ='["http://127.0.0.1/*","ws://127.0.0.1/*"]';; esac
E=/tmp/ext; rm -rf $E; mkdir -p $E
echo "const CFG = {periodic: $PERIODIC, ws: \"$WS\", ka: \"$KA\"};" | cat - /spike/ext/bg-body.js > $E/bg.js
jq -n --argjson hp "$HPJ" --arg csp "$CSP" --arg ka "$KA" '{manifest_version:3,name:"lfprobe",version:"1",
  permissions:(["alarms"] + (if $ka == "storage" then ["storage"] else [] end)),host_permissions:$hp,
  background:{service_worker:"bg.js",scripts:["bg.js"]},
  browser_specific_settings:{gecko:{id:"lfprobe@spike",strict_min_version:"128.0"}}}
  + (if $csp != "" then {content_security_policy:{extension_pages:$csp}} else {} end)' > $E/manifest.json
chmod -R a+rX $E
# A "public" address (TEST-NET-3) on lo, when the container has NET_ADMIN.
PUB=; [ "$PAGES" = 1 ] && { PUB=203.0.113.5; ip addr add $PUB/32 dev lo 2>/dev/null || PUB=; }
URLS=(); [ "$PAGES" = 1 ] && URLS=(${PUB:+"http://$PUB:8001/page.html"} "http://$IP:8001/page.html" "http://127.0.0.1:8001/page.html")
case $PAGE in pub) URLS=("http://$PUB:8001/page.html");; pubtls) URLS=("https://$PUB:8443/page.html");; ip) URLS=("http://$IP:8001/page.html");; lo) URLS=("http://127.0.0.1:8001/page.html");; esac
srv; sleep 0.5; note "ip=$IP pub=$PUB WS=$WS CSP=$CSP HP=$HP PERIODIC=$PERIODIC PING=$PING KA=$KA"

case $B in
chrome|brave)
  if [ $B = chrome ]; then BIN=google-chrome; XD=/opt/google/chrome/extensions; COMM=chrome
  else BIN=brave-browser; XD=/opt/brave.com/brave/extensions; COMM=brave; fi
  cp /spike-key/key.pem /tmp/key.pem
  $BIN --headless=new --no-sandbox --pack-extension=$E --pack-extension-key=/tmp/key.pem >/dev/null 2>&1
  ID=$(openssl rsa -in /tmp/key.pem -pubout -outform DER 2>/dev/null | sha256sum | head -c32 | tr 0-9a-f a-p)
  mkdir -p $XD; cp /tmp/ext.crx /opt/lfprobe.crx; chmod a+r /opt/lfprobe.crx
  echo '{"external_crx":"/opt/lfprobe.crx","external_version":"1"}' > $XD/$ID.json
  note "id=$ID"
  U $BIN --no-sandbox --no-first-run --user-data-dir=/home/u/udd --remote-debugging-port=9222 \
    --enable-logging=stderr --v=0 $CHFLAGS "${URLS[@]}" >$O/browser.out 2>&1 &
  targets() { curl -s localhost:9222/json/list | jq -c --arg el "$(el)" '{el:$el, t:[.[]|select(.type!="page" or (.url|test("page.html")))|{type,url:.url[0:60]}]}' >> $O/targets.log; }
  ;;
firefox|librewolf)
  if [ $B = firefox ]; then BIN=/opt/firefox-devedition/firefox; COMM=firefox
  else BIN=/opt/librewolf/librewolf; COMM=librewolf; fi
  P=/home/u/p; mkdir -p $P/extensions
  (cd $E && zip -qr $P/extensions/lfprobe@spike.xpi .)
  printf 'user_pref("xpinstall.signatures.required", false);\nuser_pref("extensions.autoDisableScopes", 0);\nuser_pref("browser.shell.checkDefaultBrowser", false);\nuser_pref("devtools.console.stdout.chrome", true);\nuser_pref("devtools.console.stdout.content", true);\n%s\n' "$FFPREFS" > $P/user.js
  chown -R u: $P
  U env ${MOZLOG:+MOZ_LOG=$MOZLOG MOZ_LOG_FILE=$O/moz} dbus-launch --exit-with-session $BIN --profile $P --no-remote "${URLS[@]}" >$O/browser.out 2>&1 &
  targets() { :; }
  ;;
esac

restarted=0
while [ $(el) -lt $DUR ]; do
  sleep 5
  e=$(el)
  [ $((e % 30)) -lt 5 ] && targets
  [ $e -ge 15 ] && [ ! -f $O/shot-15.png ] && shot 15
  if [ "$RESTART" -gt 0 ] && [ $e -ge $RESTART ] && [ $restarted = 0 ]; then
    note "killing server"; kill $SRV; wait $SRV 2>/dev/null; restarted=1
  fi
  if [ $restarted = 1 ] && [ $e -ge $((RESTART + 15)) ]; then note "restarting server"; srv; restarted=2; fi
done
shot end; targets
case $B in firefox|librewolf) grep -o '"lfprobe@spike[^,]*' /home/u/p/prefs.js | head -1 > $O/uuid.txt
  jq -c '.addons[]|select(.id=="lfprobe@spike")|{active,userDisabled,appDisabled,signedState,location,userPermissions,optionalPermissions}' /home/u/p/extensions.json > $O/addon.txt 2>&1;;
esac
pkill -TERM -u u -x $COMM; pkill -TERM -u u -f firefox; sleep 3
note "done"
}; exit
