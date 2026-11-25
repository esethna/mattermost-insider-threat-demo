package main

import (
	"regexp"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Cohere wraps cited facts in <co>…</co: 2:[0]> tags. Agents renders them on its own replies, but
// text an agent writes into a post through a tool keeps them, so they show up raw.
var cohereCitationRe = regexp.MustCompile(`</?co\b[^>]*>`)

func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	if post.GetProp("ai_generated_by") == nil || !cohereCitationRe.MatchString(post.Message) {
		return nil, ""
	}
	post.Message = cohereCitationRe.ReplaceAllString(post.Message, "")
	return post, ""
}
