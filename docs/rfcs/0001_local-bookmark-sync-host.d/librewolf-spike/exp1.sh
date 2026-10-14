#!/bin/bash
# Q1: profile root, binary, compatibility.ini, channel; a plain first launch
. /spike/lib.sh
timeout 8 $LW --headless >/dev/null 2>&1; sleep 1
echo "--- dirs"; find ~ -maxdepth 4 -name profiles.ini -o -maxdepth 4 -name installs.ini | sort
I=$(inifile); cat "$I"; echo "--- installs.ini"; cat "$(dirname $I)/installs.ini"
P=$(ls -d "$(dirname $I)"/*/ | head -1); echo "--- $P/compatibility.ini"; cat "$P/compatibility.ini"
echo "--- XDG_CONFIG_HOME=/home/u/xdg"; rm -rf ~/.librewolf ~/.config/librewolf
XDG_CONFIG_HOME=/home/u/xdg timeout 8 $LW --headless >/dev/null 2>&1; find ~ -name profiles.ini
echo "--- legacy ~/.librewolf existing"; rm -rf ~/xdg ~/.config/librewolf; mkdir -p ~/.librewolf
timeout 8 $LW --headless >/dev/null 2>&1; find ~ -name profiles.ini
