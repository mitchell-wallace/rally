package messages

import "testing"

func TestFoldTracksLifecycle(t *testing.T) {
	t.Parallel()

	targetBatchID := 7
	targetSessionID := 12
	state := Fold([]Event{
		{EventID: 1, MessageID: 10, Scope: ScopeBatch, EventType: EventMessageCreated, CreatedAt: "2026-03-24T00:00:00Z", Body: "first", TargetBatchID: &targetBatchID},
		{EventID: 2, MessageID: 10, Scope: ScopeBatch, EventType: EventMessageUpdated, UpdatedAt: "2026-03-24T00:01:00Z", Body: "updated", TargetBatchID: &targetBatchID},
		{EventID: 3, MessageID: 10, Scope: ScopeBatch, EventType: EventMessageConsumed, ConsumedAt: "2026-03-24T00:02:00Z", ApplyBatchID: &targetBatchID},
		{EventID: 4, MessageID: 11, Scope: ScopeSession, EventType: EventMessageCreated, CreatedAt: "2026-03-24T00:00:00Z", Body: "session"},
		{EventID: 5, MessageID: 11, Scope: ScopeSession, EventType: EventMessageCanceled, UpdatedAt: "2026-03-24T00:03:00Z", TargetSessionID: &targetSessionID},
	})

	if got := state[10]; got == nil || got.Body != "updated" || !got.Consumed || got.ApplyBatchID == nil || *got.ApplyBatchID != 7 {
		t.Fatalf("unexpected folded batch message: %#v", got)
	}
	if got := state[11]; got == nil || !got.Canceled {
		t.Fatalf("expected canceled session message, got %#v", got)
	}
}
