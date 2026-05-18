#!/bin/sh
# After-hook for laps done
# $1 is the lap id

rally progress --record-lap "$1"
echo "Marked done. Wrap up this run before exiting:"
echo '  laps wrapup --summary "<one-line summary>" --followup "<next task>"'
