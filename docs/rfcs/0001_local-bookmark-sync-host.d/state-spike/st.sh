#!/bin/bash
# st.sh UDD PROFILE: what the profile's two preference files say about
# extension $ID, one line each, with the file's modification time.
for f in Preferences "Secure Preferences"; do
  p="$1/$2/$f"
  if [ ! -f "$p" ]; then echo "$f: no file"; continue; fi
  echo "$f ($(stat -c %y "$p" | cut -c12-19)): $(jq -c --arg id "$ID" '
    (.extensions.settings[$id] // null) as $e
    | {entry: (if $e then ($e | {state, disable_reasons, location, from_webstore, creation_flags, was_installed_by_default, version: .manifest.version, path, keys: (if (keys | length) < 5 then keys else (keys | length) end)}) else null end),
       external_uninstalls: .extensions.external_uninstalls}
    | with_entries(select(.value != null))' "$p")"
done
