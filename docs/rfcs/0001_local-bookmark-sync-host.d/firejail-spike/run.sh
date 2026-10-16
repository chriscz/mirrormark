#!/bin/bash
# [EXP=name] run.sh B [VAR=value...]: one exp.sh (or EXP.sh) run for browser B (brave, chrome,
# firefox or librewolf) in a throwaway spike-firejail container. Needs a
# mirrormark binary in $MM (bin/dev run make build); output goes to stdout.
#
# Firejail refuses to sandbox inside a PID namespace without kernel threads
# ("an existing sandbox was detected") unless container=docker is set. It
# then needs SYS_ADMIN for its mounts, and Docker's default seccomp and
# AppArmor profiles block those mounts; --privileged is not needed.
here=$(cd "$(dirname "$0")" && pwd)
b=$1; shift
env=(-e container=docker -e B="$b"); for kv in "$@"; do env+=(-e "$kv"); done
docker run --rm "${env[@]}" --cap-add SYS_ADMIN --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined -v "$here":/spike:ro -v "$MM":/spike-bin/mirrormark:ro \
  spike-firejail bash /spike/${EXP:-exp}.sh
