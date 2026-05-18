#!/bin/sh
# Hook-only command: laps wrapup
# Forwards to rally progress --complete or --handoff based on state

STATE_FILE=".rally/run-state.json"
HANDOFF_STATE=0
if [ -f "$STATE_FILE" ]; then
    HANDOFF_STATE=$(sed -n 's/.*"handoff_state"[[:space:]]*:[[:space:]]*\([0-9]\).*/\1/p' "$STATE_FILE" | head -1)
    [ -z "$HANDOFF_STATE" ] && HANDOFF_STATE=0
fi

if [ "$HANDOFF_STATE" = "1" ]; then
    sed -i 's/"handoff_state"[[:space:]]*:[[:space:]]*1/"handoff_state": 0/' "$STATE_FILE" 2>/dev/null || true
    if ! rally progress --handoff "$@"; then
        exit $?
    fi
else
    if ! rally progress --complete "$@"; then
        exit $?
    fi
fi
echo "Progress recorded."
