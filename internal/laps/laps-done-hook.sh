#!/bin/sh
# After-hook for laps done
# $1 is the lap id

# Audit trail: record this hook firing so a missed hook is obvious in `.rally/hook-audit.jsonl`.
AUDIT_FILE=".rally/hook-audit.jsonl"
mkdir -p "$(dirname "$AUDIT_FILE")" 2>/dev/null || true
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
printf '{"ts":"%s","hook":"laps-done","lap_id":"%s","pid":%d}\n' "$TS" "$1" "$$" >> "$AUDIT_FILE" 2>/dev/null || true

rally progress --record-lap "$1"
echo "Marked done. Wrap up this run before exiting:"
echo '  laps wrapup --summary "<one-line summary>" --followup "<next task>"'
