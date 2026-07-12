#!/bin/sh
# crew-chief-scheduler — watchdog that revives the crew-chief if no claude
# session is alive, on a fixed cadence until a cutoff time, then exits.
#
# v2 (2026-07-10): sessions are now INTERACTIVE (no -p) and run inside tmux so
# they have a TTY, keep running past turn end, and the user can attach
# (`tmux attach -t crew-chief`) or drive them via claude.ai remote control.
# Because interactive sessions do not exit on their own, each fire first
# checks for a live claude process and skips if one exists — the in-session
# cron timer owns the cadence while a session is alive; this loop is only the
# dead-man's revival path.
#
# Source of truth lives in the rally repo (docs/orchestration/); the running
# copy is installed at ~/.local/bin/crew-chief-scheduler.sh so git checkouts
# can't yank the script out from under a live loop. Re-install after editing.
#
# No cron/systemd exists in this container, so this is a detached setsid loop:
#   setsid nohup ~/.local/bin/crew-chief-scheduler.sh >/dev/null 2>&1 &
#
# Env overrides (used by the self-test; defaults are the production values):
#   CC_INTERVAL  seconds between fire checks       (default 18000 = 5h)
#   CC_CUTOFF    epoch after which no fire happens (default 2026-07-13 10:00 UTC,
#                i.e. Monday 2026-07-13 8:00pm AEST — extended 2026-07-12 night
#                from the original 9am cutoff per Mitchell: finish the work
#                queue, he's unavailable during the workday)
#   CC_PROMPT    prompt for the session            (default "Crew chief, get to work")
#   CC_STATE     state/log directory               (default ~/.local/state/crew-chief)
#   CC_FIRST_DELAY  seconds before the first check (default CC_INTERVAL)

INTERVAL="${CC_INTERVAL:-18000}"
CUTOFF="${CC_CUTOFF:-1783936800}"
PROMPT="${CC_PROMPT:-Crew chief, get to work}"
STATE_DIR="${CC_STATE:-$HOME/.local/state/crew-chief}"
FIRST_DELAY="${CC_FIRST_DELAY:-$INTERVAL}"
REPO="${CC_REPO:-$HOME/group-1/rally}"
TMUX_SESSION="crew-chief"

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
log "scheduler v2 started pid=$$ interval=${INTERVAL}s cutoff=$CUTOFF first_delay=${FIRST_DELAY}s"

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
    if pgrep -x claude >/dev/null 2>&1; then
        log "skip: claude session already alive"
    else
        stamp=$(date -u '+%Y%m%dT%H%M%SZ')
        log "reviving crew-chief in tmux ($stamp)"
        tmux kill-session -t "$TMUX_SESSION" 2>/dev/null
        tmux new-session -d -s "$TMUX_SESSION" -c "$REPO" \
            "claude --dangerously-skip-permissions --model claude-fable-5 '$PROMPT'" \
            && log "revival $stamp launched" \
            || log "revival $stamp FAILED to launch"
    fi
    next_fire=$(( next_fire + INTERVAL ))
    now=$(date +%s)
    while [ "$next_fire" -le "$now" ]; do
        next_fire=$(( next_fire + INTERVAL ))
    done
done

rm -f "$PIDFILE"
log "scheduler stopped pid=$$"
