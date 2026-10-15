#!/bin/bash
# sum.sh DIR: one line per event from DIR/server.log.
jq -r '[.t, .kind, (.conn // "" | tostring),
  (if .kind=="log" or .kind=="page-report" then (.body|fromjson? // .body|tostring) else "" end | tostring | .[0:260]),
  (if .origin then "origin="+.origin else "" end), (.path // ""), (.msg // ""), (.why // ""), (if .after_s then "after_s=\(.after_s)" else "" end)]
  | map(select(. != "")) | join("  ")' "$1/server.log"
