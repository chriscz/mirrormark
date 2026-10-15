#!/bin/bash
# run.sh NAME [VAR=value...]: one exp.sh run in a throwaway spike-lt container,
# output in $OUT/NAME. Needs the server binary in $BIN/server and the
# Chromium signing key in $BIN/key.pem (see the doc).
here=$(cd "$(dirname "$0")" && pwd)
name=$1; shift
o=${OUT:?}/$name; rm -rf "$o"; mkdir -p "$o"
env=(); for kv in "$@"; do env+=(-e "$kv"); done
docker run --rm --name "lt-$name" --cap-add NET_ADMIN "${env[@]}" -v "$here":/spike:ro -v "$BIN/server":/bin-spike/server:ro \
  -v "$BIN":/spike-key:ro -v "$o":/out spike-lt bash /spike/exp.sh
