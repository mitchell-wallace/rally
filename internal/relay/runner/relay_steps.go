package runner

import (
	"context"
	"fmt"
	"io"
	"time"

	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func (r *Runner) startOrResumeRelay() (*store.RelayRecord, *routeRuntime, io.WriteCloser, error) {
	// Clear any stale run-state from a previous interrupted relay.
	_, _ = r.maybeWriteStubAndClearState("")

	relay, resumed, err := relaycore.ResumeRelay(r.store)
	if err != nil {
		return nil, nil, nil, err
	}

	routeRuntime := (*routeRuntime)(nil)
	selectionLabel := ""
	if resumed {
		if r.cfg.OverwriteMixOnResume {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
			if err != nil {
				return nil, nil, nil, err
			}
			relay.AgentMix = selectionLabel
			if err := r.store.UpdateRelay(*relay); err != nil {
				return nil, nil, nil, err
			}
		} else {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromStoredLabel(r.cfg, relay.AgentMix)
			if err != nil {
				return nil, nil, nil, err
			}
		}
	} else {
		routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
		if err != nil {
			return nil, nil, nil, err
		}
		relay, err = relaycore.CreateRelay(r.store, r.cfg.TargetIterations, selectionLabel)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	routeRuntime.store = r.store

	for _, w := range routeRuntime.Warnings() {
		r.eventSink().Emit(context.Background(), runtimeevent.RouteWarning{Message: w})
	}

	log, err := openRelayLog(r.cfg.DataDir, r.cfg.WorkspaceDir, relay.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	r.log = log

	fmt.Fprintf(log, "relay %d started (target %d iterations, mix: %s)\n", relay.ID, relay.TargetIterations, relay.AgentMix)
	r.relayStart = time.Now()
	// Data-only lifecycle marker: no operator-facing print (a terminal sink
	// no-ops it); the literal "Relay complete." line stays owned by app.StartRelay.
	r.eventSink().Emit(context.Background(), runtimeevent.RelayStarted{
		RelayID:          relay.ID,
		TargetIterations: relay.TargetIterations,
		AgentMix:         relay.AgentMix,
	})

	return relay, routeRuntime, log, nil
}

func (r *Runner) startRelaySpan(ctx context.Context, relay *store.RelayRecord) (context.Context, telemetry.Span, telemetry.RallyContext) {
	// Model the relay as a trace transaction; runs and tries are child spans.
	rc := r.rallyContext(relay)
	ctx, relaySpan := r.tel().StartSpan(ctx, "relay", fmt.Sprintf("relay-%d", relay.ID))
	relayTags := telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: rc.Repo, RepoName: rc.RepoName})
	applyRallyContext(relaySpan, relayTags, rc)
	return ctx, relaySpan, rc
}

func (r *Runner) startRunSpan(ctx context.Context, relay *store.RelayRecord, runID int, task runTask, selection routeSelection, rc telemetry.RallyContext) (context.Context, telemetry.Span) {
	runTags := telemetry.Tags(telemetry.EventInfo{
		RelayID:  relay.ID,
		RunID:    runID,
		Role:     task.promptAssignee(),
		Harness:  selection.Agent.Harness,
		Model:    selection.Agent.Model,
		Repo:     rc.Repo,
		RepoName: rc.RepoName,
		LapID:    task.LapID,
	})
	runCtx, runSpan := r.tel().StartSpan(ctx, "run", fmt.Sprintf("relay-%d-run-%d", relay.ID, runID))
	applyTags(runSpan, runTags)
	return runCtx, runSpan
}

func (r *Runner) consumeRelayScopedMessage(relay *store.RelayRecord) (*store.MessageRecord, error) {
	// Consume oldest eligible relay-scoped message at relay start
	var relayMsg *store.MessageRecord
	relayPending := r.store.EligibleRelayScopedMessages(relay.ID)
	if len(relayPending) > 0 {
		msg := relayPending[0]
		// Record consumption at consume time (Task 6)
		if msg.ConsumedByRelayID == nil {
			msg.ConsumedByRelayID = &relay.ID
			if err := r.store.UpdateMessage(msg); err != nil {
				return nil, err
			}
			// Append to ConsumedMessageIDs immediately
			relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, msg.ID)
			if err := r.store.UpdateRelay(*relay); err != nil {
				return nil, err
			}
		}
		relayMsg = &msg
	}
	return relayMsg, nil
}

func (r *Runner) consumeRunScopedMessage(runID int) (*store.MessageRecord, error) {
	// Consume run-scoped message at start of each run
	// First check if there's an already-consumed message from a failed run
	var consumedMsg *store.MessageRecord
	if existingMsg := r.store.ConsumedOutingScopedMessageForOuting(runID); existingMsg != nil {
		// Reuse the message from the failed run
		consumedMsg = existingMsg
	} else {
		// Consume a new message
		pending := r.store.PendingMessages()
		for _, p := range pending {
			if p.Scope != "relay" && p.ConsumedByOutingID == nil {
				msg := p
				msg.ConsumedByOutingID = &runID
				if err := r.store.UpdateMessage(msg); err != nil {
					return nil, err
				}
				consumedMsg = &msg
				break
			}
		}
	}
	return consumedMsg, nil
}
