#!/bin/bash
# Where Chrome or Brave records an externally installed (JSON) CRX, and
# when it writes it: not installed, installed and enabled, disabled and
# removed by the user. BRAVE and COMM pick the browser (see lib.sh),
# EXTDIR its external extensions directory.
id u >/dev/null 2>&1 || useradd -m u
. /spike/lib.sh
export ID
EXTDIR=${EXTDIR:-/opt/brave.com/brave/extensions}
TAG=$(basename $BRAVE)
P=/tmp/p
mkdir -p /tmp/out; chmod 777 /tmp/out
st() { echo "   $1, $2:"; /spike/st.sh $P "$1" | sed 's/^/     /'; }
start() { U $BRAVE --no-sandbox --no-first-run --user-data-dir=$P --profile-directory="$1" about:blank >>/tmp/brave.out 2>&1 & }
watch() { # profile seconds: print the files each time they change, every 0.5 s
  local last="" now t0=$(date +%s.%N)
  for i in $(seq $(($2 * 2))); do
    sleep 0.5; now=$(/spike/st.sh $P "$1")
    [ "$now" != "$last" ] && { printf '   +%.1fs\n' "$(echo "$(date +%s.%N) - $t0" | bc)"; echo "$now" | sed 's/^/     /'; last=$now; }
  done
}
cdp() { # profile acts: drive the profile over CDP, see mgmt.py
  (echo "ID = '$ID'; UDD = '$P'; PROF = '$1'; TAG = '$TAG'; ACTS = $2"; cat /spike/mgmt.py) >/tmp/s.py; chmod a+r /tmp/s.py
  U timeout 120 python3 /spike/cdp.py 3 /tmp/s.py -- $BRAVE --no-sandbox --no-first-run --user-data-dir=$P --profile-directory="$1" about:blank
}
command -v bc >/dev/null || { apt-get update -qq >/dev/null && apt-get install -y -qq bc >/dev/null; }
echo "== $($BRAVE --version)"
echo "== 1. Default and Profile 1 have run; nothing installed"
run $P 8 --profile-directory=Default; run $P 8 --profile-directory="Profile 1"
st Default "before install"
echo "== 2. the JSON appears while the browser runs in Default"
start Default; sleep 8
mkdir -p $EXTDIR; extjson >$EXTDIR/$ID.json
watch Default 40
stop; st Default "after a clean close"
echo "== 3. start Default again, cold: when do its files show the install?"
start Default; watch Default 30
echo "== 4. open Profile 1 in the browser running Default"
start "Profile 1"; watch "Profile 1" 30
stop; st "Profile 1" "after a clean close"
echo "== 5. Default: disable, bind, enable"
cdp Default '["disable", "wait", "bind", "enable", "wait"]'
st Default "after close"
echo "== 6. Profile 1: remove, bind"
cdp "Profile 1" '["remove", "wait", "bind"]'
st "Profile 1" "after close"
echo "== 7. Profile 1 again after the remove"
run $P 12 --profile-directory="Profile 1"; st "Profile 1" "after another start"
echo "== 8. a profile created after install, never started: Profile 2"
ls $P | grep Profile; st "Profile 2" "never started"
cp /tmp/out/* /out/ 2>/dev/null
