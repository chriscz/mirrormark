#!/bin/bash
# One run for browser $B, as root inside spike-firejail (started by run.sh).
. /spike/lib.sh

echo "## layout"
ls -l /usr/local/bin/brave-browser /usr/local/bin/google-chrome; ls /usr/local/bin | tr '\n' ' '; echo
U which -a brave-browser google-chrome firefox librewolf
for c in /usr/bin/brave-browser /usr/bin/google-chrome /opt/firefox-devedition/firefox /opt/librewolf/librewolf; do
  echo "# firejail $c: $(U firejail --debug $c --version 2>&1 | sed -n 's/^Reading profile //p' | grep -E '\.(profile|local)$' | grep -vE '/(disable|whitelist|allow)-' | tr '\n' ' ')"
done
mkdir -p /home/u/xdg/firejail; echo '# marker' >/home/u/xdg/firejail/brave.local; chown -R u: /home/u/xdg
echo "# XDG_CONFIG_HOME=/home/u/xdg: $(U env XDG_CONFIG_HOME=/home/u/xdg firejail --debug /usr/bin/brave-browser --version 2>&1 | grep -c 'Reading profile /home/u/xdg') reads of /home/u/xdg/firejail/*"
rm -rf /home/u/xdg

U $MM serve >/tmp/serve.out 2>&1 & sleep 1

case $B in
brave|chrome)
  if [ $B = brave ]; then CMD=brave-browser COMM=brave LOCAL=brave; else CMD=google-chrome COMM=chrome LOCAL=google-chrome; fi
  start() { U $CMD "$@" >>/tmp/browser.out 2>&1 & sleep 20; stopb; }
  ready() { echo "## install --profile Default"; U $MM install --browser $B --profile Default 2>&1 | grep -vE '^  (status|log|stop):|^Set up|^Could not run|^Keep this|serve$'; }
  start about:blank
  U $MM install --browser $B --profile Default >/dev/null 2>&1   # writes the files
  echo "## external file: $(ls /usr/share/google-chrome/extensions /opt/brave.com/brave/extensions)"
  echo "## no rule"; start; ready
  # Only the CRX, not the data directory with the databases and tokens.
  CRX='whitelist ${HOME}/.local/share/mirrormark/mirrormark.crx'
  if [ $B = chrome ]; then
    local_ "$CRX"; start; ready
    local_ "$CRX" 'whitelist /usr/share/google-chrome/extensions'
  else local_ "$CRX"; fi
  start; ready
  # What the jailed browser's profile shows of the data directory.
  P=$([ $B = brave ] && echo brave-browser || echo google-chrome)
  echo "## in the jail: ls data dir: $(U firejail --quiet --profile=$P ls /home/u/.local/share/mirrormark 2>&1 | tr '\n' ' ')"
  echo "## in the jail: cat profiles/*.yaml: $(U firejail --quiet --profile=$P sh -c 'cat /home/u/.local/share/mirrormark/profiles/*.yaml' 2>&1 | head -2 | tr '\n' ' ')"
  echo "## in the jail: write next to the CRX: $(U firejail --quiet --profile=$P sh -c 'touch /home/u/.local/share/mirrormark/x && echo wrote || echo refused' 2>&1 | tail -1)"
  echo "## outside: $(ls /home/u/.local/share/mirrormark | tr '\n' ' ')"
  echo "## the browser the bind opened: $(pgrep -u u -a firejail | head -1 | cut -c1-120)"
  sleep 2; stopb
  ;;
firefox|librewolf)
  if [ $B = firefox ]; then BIN=/opt/firefox-devedition/firefox COMM=firefox-bin LOCAL=firefox ROOT=.mozilla/firefox
  else BIN=/opt/librewolf/librewolf COMM=librewolf LOCAL=librewolf ROOT=.librewolf; fi
  # The legacy root, which firejail's profile shows. A new install of
  # Developer Edition 159 uses it when the directory exists; LibreWolf 158
  # needs a profiles.ini there, or it uses ~/.config/librewolf, which the
  # profile hides (its profile is then lost when the jail ends).
  mkdir -p /home/u/$ROOT
  if [ $B = librewolf ]; then mkdir -p /home/u/$ROOT/spike.default
    printf '[General]\nStartWithLastProfile=1\nVersion=2\n\n[Profile0]\nName=default\nIsRelative=1\nPath=spike.default\nDefault=1\n' >/home/u/$ROOT/profiles.ini; fi
  chown -R u: /home/u
  startg() { U firejail $BIN "$@" >>/tmp/browser.out 2>&1 & sleep 20; }
  startg; echo "## first run: $(ps -u u -o comm= | sort -u | tr "\n" " "); root: $(ls /home/u/$ROOT | tr "\n" " ")"; stopb
  echo "## listing"; U $MM install --browser $B 2>&1 | grep -v '^mirrormark: warning'
  P=$(U $MM install --browser $B 2>/dev/null | awk -F'  +' '/not installed/ {print $3; exit}')
  ready() { echo "## install --profile $P"; U $MM install --browser $B --profile "$P" 2>&1 | grep -vE '^  (status|log|stop):|^Set up|^Could not run|^Keep this|serve$|signatures|^(otherwise|user.js|any unsigned|to 14|without asking|mirrormark uninstall)'; }
  ready   # writes the files
  echo "## jailed $B restarted, left running for the bind"; startg; ready
  echo "## running: $(pgrep -u u -a firejail | head -1 | cut -c1-120)"; stopb
  echo "## jailed $B closed: the bind starts $BIN outside the jail"; ready; sleep 2; stopb
  ;;
esac
echo "## server log"; grep -E 'hello|bound|refus|connect' /tmp/.local/state/mirrormark/log /home/u/.local/state/mirrormark/log 2>/dev/null | tail -5 | cut -c1-200
