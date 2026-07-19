#!/bin/sh
# resume-circuit.sh — one-command recovery after a reboot/resize of this box.
# Brings the Moved-by-the-Word crew-chief circuit back to business as usual:
#   1. clears the scheduler pidfile if its pid is dead (post-reboot pids are
#      stale and may even be recycled by unrelated processes)
#   2. relaunches the crew-chief watchdog with the current production env
#      (Opus 4.8 chief, opus-chief tmux session, 5h cadence) — its first fire
#      (~60s) starts an interactive chief in tmux, visible in remote control
#   3. prints what it found/did and how to attach
# Idempotent: safe to run when things are already up — a live scheduler or a
# live opus-chief tmux session is detected and left alone.
# Does NOT restore one-shot helpers (successor-launch, fable-noon-backup):
# those are time-scoped and re-armed manually when needed.
# Update the CC_* defaults when the campaign changes. Source of truth:
# rally/docs/orchestration/; installed copy: ~/.local/bin/resume-circuit.sh
# (2026-07-19, Fable chief, per Mitchell's pre-resize request.)

STATE_DIR="$HOME/.local/state/crew-chief"
PIDFILE="$STATE_DIR/scheduler.pid"

echo "== circuit resume $(date '+%a %Y-%m-%d %H:%M %Z') =="

scheduler_alive=""
if [ -f "$PIDFILE" ]; then
    pid=$(cat "$PIDFILE")
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null \
       && ps -p "$pid" -o cmd= 2>/dev/null | grep -q crew-chief-scheduler; then
        scheduler_alive="$pid"
    fi
fi

if [ -n "$scheduler_alive" ]; then
    echo "watchdog scheduler already running (pid $scheduler_alive) — leaving it alone"
else
    rm -f "$PIDFILE"
    setsid nohup env \
        CC_MODEL="${CC_MODEL:-claude-opus-4-8}" \
        MAX_THINKING_TOKENS="${MAX_THINKING_TOKENS:-31999}" \
        CC_CUTOFF="${CC_CUTOFF:-$(date -ud '2026-07-27 23:00' +%s)}" \
        CC_INTERVAL="${CC_INTERVAL:-18000}" \
        CC_FIRST_DELAY="${CC_FIRST_DELAY:-60}" \
        CC_REPO="${CC_REPO:-$HOME/group-1}" \
        CC_TMUX_SESSION="${CC_TMUX_SESSION:-opus-chief}" \
        CC_PROMPT="${CC_PROMPT:-Crew chief (Opus 4.8), resume the Moved-by-the-Word circuit after a machine restart. FIRST read /home/mitchell/group-1/Prayer-app/docs/orchestration/crew-chief.md (Standing Orders, Work state, Continuation) and your memory directory index, then check Linear for comments from Mitchell (New human message label) and check git status in Prayer-app: a reboot may have killed an in-flight lap, leaving a dirty tree — review before dispatching anything. Scars travel with rules: never background a lap; verify dispatches by log file; review the diff, not the report; re-run gates before accepting.}" \
        "$HOME/.local/bin/crew-chief-scheduler.sh" >/dev/null 2>&1 &
    sleep 2
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "watchdog scheduler relaunched (pid $(cat "$PIDFILE")); first chief fire in ~60s"
    else
        echo "ERROR: scheduler did not come up — check $STATE_DIR/scheduler.log"
    fi
fi

echo "-- tmux sessions --"
tmux ls 2>/dev/null || echo "(none yet — chief appears within ~60s of scheduler start)"
echo "-- last scheduler log lines --"
tail -3 "$STATE_DIR/scheduler.log" 2>/dev/null
echo "attach with: tmux attach -t opus-chief   (or via claude.ai remote control)"
