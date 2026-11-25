package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// watchRuns checks off a demo run's checklist items once the command run from the checklist has
// finished its work. Playbooks has no hook for runs, so this polls each open demo run for new
// command run times.
func (p *Plugin) watchRuns(stop <-chan struct{}) {
	handled := map[string]int64{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.completeRunSteps(handled)
		}
	}
}

// stepKind groups a checklist command by what has to happen before it counts as done.
func stepKind(command string) string {
	f := strings.Fields(command)
	switch {
	case len(f) == 0:
		return ""
	case f[0] == "/soar" && len(f) > 1:
		return "/soar " + f[1]
	case f[0] == "/echo" && strings.Contains(command, "status update"):
		return statusUpdateKind
	case f[0] == "/echo" || f[0] == "/cohere" || f[0] == "/soar" || f[0] == "/call":
		return f[0]
	}
	return ""
}

// stepDone records that the work behind a kind of step finished in a channel.
func (p *Plugin) stepDone(channelID, kind string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.doneAt == nil {
		p.doneAt = map[string]int64{}
	}
	p.doneAt[channelID+"|"+kind] = model.GetMillis()
}

func (p *Plugin) stepDoneAt(channelID, kind string) int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.doneAt[channelID+"|"+kind]
}

// A step asking the agent to draft the run's status update is done once the update is posted.
const (
	statusUpdateKind     = "status update"
	statusUpdatePostType = "custom_run_update"
)

// noteAgentAnswer treats an agent's answer posted at the top of a demo run channel, or a thread
// reply from a bot linking to it, as the end of an /echo step.
func (p *Plugin) noteAgentAnswer(post *model.Post) {
	if post.Type == statusUpdatePostType {
		if runID, appErr := p.API.KVGet("run_channel_" + post.ChannelId); appErr == nil && runID != nil {
			p.stepDone(post.ChannelId, statusUpdateKind)
		}
		return
	}
	posted := post.RootId == "" && post.GetProp("ai_generated_by") != nil
	linked := post.RootId != "" && strings.Contains(post.Message, "http")
	if !posted && !linked {
		return
	}
	if runID, appErr := p.API.KVGet("run_channel_" + post.ChannelId); appErr != nil || runID == nil {
		return
	}
	if linked {
		if author, appErr := p.API.GetUser(post.UserId); appErr != nil || !author.IsBot {
			return
		}
	}
	p.stepDone(post.ChannelId, "/echo")
}

func (p *Plugin) completeRunSteps(handled map[string]int64) {
	for page := 0; ; page++ {
		keys, appErr := p.API.KVList(page, 100)
		if appErr != nil || len(keys) == 0 {
			return
		}
		for _, key := range keys {
			if runID, ok := strings.CutPrefix(key, "run_owner_"); ok {
				p.completeRunStepsFor(runID, handled)
			}
		}
	}
}

func (p *Plugin) completeRunStepsFor(runID string, handled map[string]int64) {
	owner, appErr := p.API.KVGet("run_owner_" + runID)
	if appErr != nil || owner == nil {
		return
	}
	var run struct {
		ChannelID  string `json:"channel_id"`
		EndAt      int64  `json:"end_at"`
		Checklists []struct {
			Items []struct {
				Command        string `json:"command"`
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
			if item.CommandLastRun <= handled[key] {
				continue
			}
			if item.State == "closed" || item.State == "skipped" {
				handled[key] = item.CommandLastRun
				continue
			}
			if kind := stepKind(item.Command); kind != "" && p.stepDoneAt(run.ChannelID, kind) < item.CommandLastRun {
				continue
			}
			handled[key] = item.CommandLastRun
			path := fmt.Sprintf("/runs/%s/checklists/%d/item/%d/state", runID, c, i)
			if err := p.playbooks(string(owner), http.MethodPut, path, map[string]string{"new_state": "closed"}, nil); err != nil {
				p.API.LogWarn("complete step", "error", err.Error())
			}
		}
	}
}
