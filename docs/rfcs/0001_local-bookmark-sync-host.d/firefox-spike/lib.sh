FF=${FF:-/opt/devedition/firefox}
ID=mirrormark@chriscz.github.io
HOSTJSON='{"name":"com.github.chriscz.mirrormark","description":"spike","path":"/spike/host.py","type":"stdio","allowed_extensions":["mirrormark@chriscz.github.io"]}'
mkxpi() { (cd /spike/ext && rm -f /tmp/spike.xpi && zip -qr /tmp/spike.xpi .); }
profdir() { # name -> dir
  local ini; ini=$(ls ~/.config/mozilla/firefox/profiles.ini ~/.mozilla/firefox/profiles.ini 2>/dev/null | head -1)
  local base; base=$(dirname "$ini")
  awk -v n="$1" -F= '/^\[/{name="";path=""} $1=="Name"{name=$2} $1=="Path"{path=$2; if(name==n) print path} ' "$ini" | head -1 | sed "s|^|$base/|"
}
runff() { # profile-dir seconds [args...]
  local d=$1 s=$2; shift 2
  timeout -s TERM $s $FF --headless --profile "$d" "$@" >/tmp/ff.out 2>&1
  sleep 1
}
addonstate() { jq -c --arg id $ID '.addons[] | select(.id==$id) | {id,active,userDisabled,appDisabled,signedState,location,seen,foreignInstall,softDisabled,embedderDisabled}' "$1/extensions.json" 2>/dev/null || echo "no extensions.json/addon"; }
