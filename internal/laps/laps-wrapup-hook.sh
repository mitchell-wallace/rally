#!/bin/sh
# Hook-only command: laps wrapup
# Forwards to rally progress --complete or --handoff based on state

# Audit trail: record this hook firing.
AUDIT_FILE=".rally/state/hook-audit.jsonl"
mkdir -p "$(dirname "$AUDIT_FILE")" 2>/dev/null || true
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
printf '{"ts":"%s","hook":"laps-wrapup","args":"%s","pid":%d}\n' "$TS" "$*" "$$" >> "$AUDIT_FILE" 2>/dev/null || true

STATE_FILE=".rally/state/run-state.json"
HANDOFF_STATE=0
if [ -f "$STATE_FILE" ]; then
    HANDOFF_STATE=$(sed -n 's/.*"handoff_state"[[:space:]]*:[[:space:]]*\([0-9]\).*/\1/p' "$STATE_FILE" | head -1)
    [ -z "$HANDOFF_STATE" ] && HANDOFF_STATE=0
fi

if [ "$HANDOFF_STATE" = "1" ]; then
    # Portable in-place edit: BSD sed -i requires a suffix arg, GNU sed does
    # not, so neither "sed -i 's/.../.../'" form works on both. Rewrite via a
    # temp file instead, keeping the reset best-effort.
    sed 's/"handoff_state"[[:space:]]*:[[:space:]]*1/"handoff_state": 0/' "$STATE_FILE" > "$STATE_FILE.tmp" 2>/dev/null && mv "$STATE_FILE.tmp" "$STATE_FILE" 2>/dev/null || rm -f "$STATE_FILE.tmp" 2>/dev/null
    if ! rally progress --handoff "$@"; then
        exit $?
    fi
else
    if ! rally progress --complete "$@"; then
        exit $?
    fi
fi
echo "Progress recorded."
