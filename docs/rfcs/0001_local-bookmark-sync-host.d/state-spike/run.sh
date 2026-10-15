#!/bin/bash
# Run one experiment in a throwaway container, as the other spikes do:
#   MM=<mirrormark binary> run.sh IMAGE EXP [env...]
# e.g. run.sh spike-brave chromium; run.sh spike-chrome chromium
# BRAVE=google-chrome COMM=chrome EXTDIR=/opt/google/chrome/extensions;
# run.sh spike-librewolf gecko; run.sh spike-firefox gecko
# LW=/opt/firefox-devedition/firefox. Screenshots go to $OUT.
here=$(cd "$(dirname "$0")" && pwd)
img=$1 exp=$2; shift 2
envs=(); for e in "$@"; do envs+=(-e "$e"); done
mkdir -p "${OUT:-/tmp/state-spike-out}"
docker run --rm -u root "${envs[@]}" -v "$here/../brave-spike":/bs:ro -v "$here":/ss:ro \
  -v "$here/../librewolf-spike":/lws:ro -v "$here/../firefox-spike":/ff:ro \
  -v "$here/../../../../extension":/real:ro -v "$MM":/spike-bin/mirrormark:ro \
  -v "${OUT:-/tmp/state-spike-out}":/out "$img" \
  bash -c "mkdir /spike && cp /bs/* /spike/ && cp /ss/* /spike/ && cp /spike-bin/mirrormark /spike/ && chmod a+rx /spike/* && /spike/$exp.sh"
