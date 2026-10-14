#!/bin/bash
# Run one experiment in a throwaway container:
#   run.sh IMAGE EXP [docker args...]   e.g. run.sh spike-brave exp2
# Needs a mirrormark binary in $MM (built from the repo with
# CGO_ENABLED=0 go build ./cmd/mirrormark) and writes screenshots to $OUT.
here=$(cd "$(dirname "$0")" && pwd)
img=$1 exp=$2; shift 2
mkdir -p "${OUT:-/tmp/brave-spike-out}"
docker run --rm -u root "$@" -v "$here":/spike-src:ro -v "$MM":/spike-bin/mirrormark:ro \
  -v "${OUT:-/tmp/brave-spike-out}":/out "$img" \
  bash -c "mkdir /spike && cp /spike-src/* /spike/ && cp /spike-bin/mirrormark /spike/ && /spike/$exp.sh"
