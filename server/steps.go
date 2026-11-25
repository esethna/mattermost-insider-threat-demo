package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// watchRuns checks off a demo run's checklist items once their command is run from the checklist.
// Playbooks has no hook for that, so this polls each open demo run for new command run times.
func (p *Plugin) watchRuns(stop <-chan struct{}) {
	lastRun := map[string]int64{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.completeRunSteps(lastRun)
		}
	}
}

func (p *Plugin) completeRunSteps(lastRun map[string]int64) {
	for page := 0; ; page++ {
		keys, appErr := p.API.KVList(page, 100)
		if appErr != nil || len(keys) == 0 {
			return
		}
		for _, key := range keys {
			if runID, ok := strings.CutPrefix(key, "run_owner_"); ok {
				p.completeRunStepsFor(runID, lastRun)
			}
		}
	}
}

func (p *Plugin) completeRunStepsFor(runID string, lastRun map[string]int64) {
	owner, appErr := p.API.KVGet("run_owner_" + runID)
	if appErr != nil || owner == nil {
		return
	}
	var run struct {
		EndAt      int64 `json:"end_at"`
		Checklists []struct {
			Items []struct {
				State          string `json:"state"`
				CommandLastRun int64  `json:"command_last_run"`
			} `json:"items"`
		} `json:"checklists"`
	}
	if err := p.playbooks(string(owner), http.MethodGet, "/runs/"+runID, nil, &run); err != nil || run.EndAt != 0 {
		return
	}
	for c, checklist := range run.Checklists {
		for i, item := range checklist.Items {
			key := fmt.Sprintf("%s/%d/%d", runID, c, i)
			if item.CommandLastRun <= lastRun[key] {
				continue
			}
			lastRun[key] = item.CommandLastRun
			if item.State == "closed" || item.State == "skipped" {
				continue
			}
			path := fmt.Sprintf("/runs/%s/checklists/%d/item/%d/state", runID, c, i)
			if err := p.playbooks(string(owner), http.MethodPut, path, map[string]string{"new_state": "closed"}, nil); err != nil {
				p.API.LogWarn("complete step", "error", err.Error())
			}
		}
	}
}
