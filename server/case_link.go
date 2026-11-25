package main

import (
	"net/http"
	"regexp"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const caseField = "Elastic case"

var caseLinkRe = regexp.MustCompile(`https://[^\s()<>\[\]]+/app/security/cases/[0-9a-f-]{36}`)

func (p *Plugin) MessageHasBeenPosted(_ *plugin.Context, post *model.Post) {
	p.recordCaseLink(post)
	p.noteAgentAnswer(post)
}

// MessageHasBeenUpdated is needed because agent replies stream in as edits to an initially empty post.
func (p *Plugin) MessageHasBeenUpdated(_ *plugin.Context, post, _ *model.Post) {
	p.recordCaseLink(post)
	p.noteAgentAnswer(post)
	p.recordCall(post)
}

// recordCaseLink copies the first Elastic case link an agent posts in a demo run channel into the
// run's Elastic case attribute.
func (p *Plugin) recordCaseLink(post *model.Post) {
	if post.UserId == p.botID {
		return
	}
	link := caseLinkRe.FindString(post.Message)
	if link == "" {
		return
	}
	runID, appErr := p.API.KVGet("run_channel_" + post.ChannelId)
	if appErr != nil || runID == nil {
		return
	}
	owner, appErr := p.API.KVGet("run_owner_" + string(runID))
	if appErr != nil || owner == nil {
		return
	}
	if author, appErr := p.API.GetUser(post.UserId); appErr != nil || !author.IsBot {
		return
	}
	key := "run_case_" + string(runID)
	if prev, _ := p.API.KVGet(key); string(prev) == link {
		return
	}

	var fields []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := p.playbooks(string(owner), http.MethodGet, "/runs/"+string(runID)+"/property_fields", nil, &fields); err != nil {
		p.API.LogWarn("case link: property fields", "error", err.Error())
		return
	}
	for _, f := range fields {
		if f.Name != caseField {
			continue
		}
		if err := p.playbooks(string(owner), http.MethodPut, "/runs/"+string(runID)+"/property_fields/"+f.ID+"/value", map[string]string{"value": link}, nil); err != nil {
			p.API.LogWarn("case link: set attribute", "error", err.Error())
			return
		}
		_ = p.API.KVSet(key, []byte(link))
		return
	}
}
