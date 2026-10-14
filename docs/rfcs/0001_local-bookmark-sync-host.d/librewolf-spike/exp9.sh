#!/bin/bash
# What today's `mirrormark install --browser firefox` does with LibreWolf.
# Expects build/mirrormark at /mm/mirrormark.
. /spike/lib.sh
timeout 8 $LW --headless >/dev/null 2>&1; sleep 1
echo "=== default root"; /mm/mirrormark install --browser firefox </dev/null; echo "exit $?"
echo "=== --user-data-dir ~/.config/librewolf/librewolf"; /mm/mirrormark install --browser firefox --user-data-dir ~/.config/librewolf/librewolf </dev/null; echo "exit $?"
ls /opt/librewolf/firefox 2>&1
