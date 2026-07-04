package tuicore

import (
	"testing"
	"time"
)

func TestOutingFeedLifecycleOverDemoScript(t *testing.T) {
	var feed OutingFeed
	for _, step := range DemoScript() {
		feed.Apply(step.Event)
	}

	items := feed.Items()
	if len(items) != 4 {
		t.Fatalf("len(items) = %d, want 4", len(items))
	}
	if got := feed.LiveIndex(); got != -1 {
		t.Fatalf("LiveIndex() = %d, want -1", got)
	}
	meta := feed.Meta()
	if !meta.Completed {
		t.Fatalf("meta.Completed = false, want true")
	}
	if meta.RelayID != 42 || meta.Passed != 3 || meta.Failed != 1 || meta.Cancelled != 0 {
		t.Fatalf("meta = %+v, want relay 42 with 3 passed 1 failed 0 cancelled", meta)
	}
	if meta.TotalDuration != time.Hour+35*time.Second {
		t.Fatalf("TotalDuration = %v, want 1h0m35s", meta.TotalDuration)
	}
	if items[2].Outcome != OutcomeFailed || items[2].FailReason != "usage limit" {
		t.Fatalf("third item = %+v, want failed usage-limit outing", items[2])
	}
}

func TestOutingFeedSeedPrependsHistory(t *testing.T) {
	var feed OutingFeed
	feed.Seed([]FeedItem{{OutingIndex: 1, Title: "seeded", Outcome: OutcomePassed}})
	feed.Apply(DemoScript()[1].Event)

	items := feed.Items()
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Title != "seeded" {
		t.Fatalf("first item title = %q, want seeded", items[0].Title)
	}
	if got := feed.LiveIndex(); got != 1 {
		t.Fatalf("LiveIndex() = %d, want 1", got)
	}
}

func TestOutingFeedEnrich(t *testing.T) {
	var feed OutingFeed
	feed.Seed([]FeedItem{{OutingIndex: 7, Title: "seeded", Outcome: OutcomePassed}})
	feed.Enrich(7, "summary", "verification", []string{"follow up"})

	items := feed.Items()
	if items[0].Summary != "summary" || items[0].Classification != "verification" {
		t.Fatalf("item enrichment = %+v", items[0])
	}
	if len(items[0].Followups) != 1 || items[0].Followups[0] != "follow up" {
		t.Fatalf("followups = %#v, want one follow up", items[0].Followups)
	}
}
