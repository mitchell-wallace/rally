#!/bin/sh
# Hook-only command: laps handoff
# Sets handoff state and directs agent to wrapup

rally progress --set-handoff
echo "Handoff signaled. Before exiting, call:"
echo '  laps wrapup --summary "<why blocked>" --followup "<unblocker task>"'
echo "Each followup will be created as a new lap at the head of the queue."
