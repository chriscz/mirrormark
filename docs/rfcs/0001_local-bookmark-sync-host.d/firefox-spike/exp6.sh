#!/bin/bash
. /spike/lib.sh
echo "=== Dev Edition: plain first launch"; timeout 8 /opt/devedition/firefox --headless >/dev/null 2>&1; sleep 1
cat ~/.config/mozilla/firefox/profiles.ini; echo "--- installs.ini"; cat ~/.config/mozilla/firefox/installs.ini
echo "=== then release: plain first launch"; timeout 8 /opt/release/firefox --headless >/dev/null 2>&1; sleep 1
cat ~/.config/mozilla/firefox/profiles.ini; echo "--- installs.ini"; cat ~/.config/mozilla/firefox/installs.ini
ls ~/.config/mozilla/firefox ~/.mozilla 2>&1
echo "=== legacy: with ~/.mozilla/firefox existing"
rm -rf ~/.config/mozilla; mkdir -p ~/.mozilla/firefox; timeout 8 /opt/release/firefox --headless >/dev/null 2>&1; sleep 1
ls ~/.mozilla/firefox ~/.config/mozilla 2>&1 | head
