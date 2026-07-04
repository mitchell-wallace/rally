#!/bin/sh
# crew-chief-scheduler — spawn a fresh headless crew-chief session on a fixed
# cadence until a cutoff time, then exit.
#
# Source of truth lives in the rally repo (docs/orchestration/); the running
# copy is installed at ~/.local/bin/crew-chief-scheduler.sh so git checkouts
# can't yank the script out from under a live loop. Re-install after editing.
#
# No cron/systemd exists in this container, so this is a detached setsid loop:
#   setsid nohup ~/.local/bin/crew-chief-scheduler.sh >/dev/null 2>&1 &
#
# Env overrides (used by the self-test; defaults are the production values):
#   CC_INTERVAL  seconds between fire starts        (default 18000 = 5h)
#   CC_CUTOFF    epoch after which no fire happens  (default 2026-07-08 07:00 UTC,
#                i.e. 5pm July 8 AEST)
#   CC_PROMPT    prompt for the session             (default "Crew chief, get to work")
#   CC_STATE     state/log directory                (default ~/.local/state/crew-chief)
#   CC_FIRST_DELAY  seconds before the first fire   (default CC_INTERVAL)

INTERVAL="${CC_INTERVAL:-18000}"
CUTOFF="${CC_CUTOFF:-1783494000}"
PROMPT="${CC_PROMPT:-Crew chief, get to work}"
STATE_DIR="${CC_STATE:-$HOME/.local/state/crew-chief}"
FIRST_DELAY="${CC_FIRST_DELAY:-$INTERVAL}"
REPO="/workspace/rally"

mkdir -p "$STATE_DIR"
LOG="$STATE_DIR/scheduler.log"
PIDFILE="$STATE_DIR/scheduler.pid"

log() { echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ') $*" >> "$LOG"; }

# Refuse to run twice: a stale pidfile is fine, a live one means we exit.
if [ -f "$PIDFILE" ]; then
    oldpid=$(cat "$PIDFILE")
    if [ -n "$oldpid" ] && kill -0 "$oldpid" 2>/dev/null; then
        log "refusing to start: scheduler already running (pid $oldpid)"
        exit 1
    fi
fi
echo $$ > "$PIDFILE"
log "scheduler started pid=$$ interval=${INTERVAL}s cutoff=$CUTOFF first_delay=${FIRST_DELAY}s"

next_fire=$(( $(date +%s) + FIRST_DELAY ))

while :; do
    now=$(date +%s)
    if [ "$now" -lt "$next_fire" ]; then
        sleep $(( next_fire - now ))
    fi
    now=$(date +%s)
    if [ "$now" -gt "$CUTOFF" ]; then
        log "cutoff reached, exiting"
        break
    fi
    stamp=$(date -u '+%Y%m%dT%H%M%SZ')
    log "firing session ($stamp)"
    ( cd "$REPO" && claude --dangerously-skip-permissions --model "claude-fable-5" \
        -p "$PROMPT" ) >> "$STATE_DIR/session-$stamp.log" 2>&1
    log "session $stamp exited code=$?"
    # Next fire keeps the original cadence; skip any fires the session overran.
    next_fire=$(( next_fire + INTERVAL ))
    now=$(date +%s)
    while [ "$next_fire" -le "$now" ]; do
        next_fire=$(( next_fire + INTERVAL ))
    done
done

rm -f "$PIDFILE"
log "scheduler stopped pid=$$"
