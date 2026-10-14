# Mounts: /spike (this dir), /ff (../firefox-spike: host.py, ext/, bidi/),
# /real (mirrormark's extension/). Run as user u in spike-librewolf.
LW=${LW:-/opt/librewolf/librewolf}
ID=mirrormark@chriscz.github.io
HOSTJSON='{"name":"com.github.chriscz.mirrormark","description":"spike","path":"/ff/host.py","type":"stdio","allowed_extensions":["mirrormark@chriscz.github.io"]}'
# The real extension zipped as internal/firefox XPI() does: the six embedded
# files at the zip's root, nothing else.
mkxpi() { (cd /real && rm -f /tmp/real.xpi && zip -qX /tmp/real.xpi manifest.json background.js bind.html bind.js popup.html popup.js); }
mkprobe() { (cd /ff/ext && rm -f /tmp/spike.xpi && zip -qr /tmp/spike.xpi .); }
bidi() { [ -x /tmp/bidi ] || (cd /ff/bidi && GOCACHE=/tmp/gocache go build -o /tmp/bidi .); /tmp/bidi "$@"; }
inifile() { ls ~/.config/librewolf/librewolf/profiles.ini ~/.librewolf/profiles.ini 2>/dev/null | head -1; }
profdir() { # name -> dir
  local ini base; ini=$(inifile); base=$(dirname "$ini")
  awk -v n="$1" -F= '/^\[/{name="";path=""} $1=="Name"{name=$2} $1=="Path"{path=$2; if(name==n) print path} ' "$ini" | head -1 | sed "s|^|$base/|"
}
runlw() { # profile-dir seconds [args...]
  local d=$1 s=$2; shift 2
  timeout -s TERM $s $LW --headless --profile "$d" "$@" >/tmp/lw.out 2>&1
  sleep 1
}
addonstate() { jq -c --arg id $ID '.addons[] | select(.id==$id) | {active,userDisabled,appDisabled,signedState,location,seen}' "$1/extensions.json" 2>/dev/null || echo "no extensions.json/addon"; }
nm() { mkdir -p "$1"; echo "$HOSTJSON" > "$1/com.github.chriscz.mirrormark.json"; }
