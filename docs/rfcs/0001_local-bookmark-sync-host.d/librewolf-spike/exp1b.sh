#!/bin/bash
# Q1: which legacy directory makes LibreWolf keep the old ~/.librewolf root
. /spike/lib.sh
for v in "mkdir -p ~/.librewolf" "mkdir -p ~/.librewolf/librewolf" "mkdir -p ~/.librewolf; printf '[General]\nStartWithLastProfile=1\n' > ~/.librewolf/profiles.ini"; do
  rm -rf ~/.librewolf ~/.config/librewolf; eval "$v"
  timeout 8 $LW --headless >/dev/null 2>&1; echo "== $v -> $(find ~ -name prefs.js | sed 's|/[^/]*/prefs.js||' | sort -u)"
done
