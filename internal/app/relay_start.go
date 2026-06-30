package app

import (
	"fmt"

	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
)

type ResumeInfo struct {
	HasUnfinished       bool
	RelayID             int
	CompletedIterations int
	TargetIterations    int
	AgentMix            string
}

func InspectResume(workspaceDir string) (ResumeInfo, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return ResumeInfo{}, fmt.Errorf("load store: %w", err)
	}

	r, found, err := relay.ResumeRelay(s)
	if err != nil {
		return ResumeInfo{}, err
	}
	if !found {
		return ResumeInfo{}, nil
	}

	return ResumeInfo{
		HasUnfinished:       true,
		RelayID:             r.ID,
		CompletedIterations: r.CompletedIterations,
		TargetIterations:    r.TargetIterations,
		AgentMix:            r.AgentMix,
	}, nil
}
