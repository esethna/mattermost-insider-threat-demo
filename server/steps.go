package main

import (
	"fmt"
	"net/http"
	"strings"
)

// completeStep checks off the open checklist items in the channel's demo run whose command starts
// with prefix. SOAR results are attachment-only posts, so the playbook's keyword triggers can't
// see them.
func (p *Plugin) completeStep(channelID, userID, prefix string) {
	runID, appErr := p.API.KVGet("run_channel_" + channelID)
	if appErr != nil || runID == nil {
		return
	}
	var run struct {
		Checklists []struct {
			Items []struct {
				Command string `json:"command"`
				State   string `json:"state"`
			} `json:"items"`
		} `json:"checklists"`
	}
	if err := p.playbooks(userID, http.MethodGet, "/runs/"+string(runID), nil, &run); err != nil {
		p.API.LogWarn("complete step: get run", "error", err.Error())
		return
	}
	for c, checklist := range run.Checklists {
		for i, item := range checklist.Items {
			if item.State == "closed" || !strings.HasPrefix(item.Command, prefix) {
				continue
			}
			path := fmt.Sprintf("/runs/%s/checklists/%d/item/%d/state", runID, c, i)
			if err := p.playbooks(userID, http.MethodPut, path, map[string]string{"new_state": "closed"}, nil); err != nil {
				p.API.LogWarn("complete step", "error", err.Error())
			}
		}
	}
}
