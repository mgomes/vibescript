#!/bin/bash
# usage: ab.sh file.vibe — prints DIVERGE or SAME with outputs
S=/private/tmp/claude-501/-Users-mgomes-Documents-Work-moonbase-vibescript/66de2598-b1d3-4bb4-b459-28b8d8a02984/scratchpad/vibescript-review
b=$($S/vibes-base run "$1" 2>&1); brc=$?
n=$($S/vibes-branch run "$1" 2>&1); nrc=$?
if [ "$b" == "$n" ] && [ $brc -eq $nrc ]; then
  echo "SAME($brc): $(echo "$b" | head -2 | tr '\n' ' ')"
else
  echo "DIVERGE base($brc) vs branch($nrc)"
  echo "  BASE:   $(echo "$b" | head -3 | tr '\n' '|')"
  echo "  BRANCH: $(echo "$n" | head -3 | tr '\n' '|')"
fi
