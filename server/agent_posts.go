package main

import (
	"regexp"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Cohere wraps cited facts in <co>…</co: 2:[0]> tags. Agents renders them on its own replies, but
// text an agent writes into a post through a tool keeps them, so they show up raw.
var cohereCitationRe = regexp.MustCompile(`</?co\b[^>]*>`)

// MessageWillBePosted tidies posts an agent writes through a tool. The returned post is merged into
// the original over RPC, so fields can be changed here but not cleared.
func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	botID, _ := post.GetProp("ai_generated_by").(string)
	if botID == "" {
		return nil, ""
	}
	changed := false
	if cohereCitationRe.MatchString(post.Message) {
		post.Message = cohereCitationRe.ReplaceAllString(post.Message, "")
		changed = true
	}
	if p.postAsAgent(post, botID) {
		changed = true
	}
	if !changed {
		return nil, ""
	}
	return post, ""
}

// postAsAgent makes a top-level post an agent writes in a demo run channel come from the agent,
// rather than the person who asked it.
func (p *Plugin) postAsAgent(post *model.Post, botID string) bool {
	if post.UserId == botID || post.RootId != "" {
		return false
	}
	if runID, appErr := p.API.KVGet("run_channel_" + post.ChannelId); appErr != nil || runID == nil {
		return false
	}
	if bot, appErr := p.API.GetUser(botID); appErr != nil || !bot.IsBot {
		return false
	}
	if _, appErr := p.API.GetChannelMember(post.ChannelId, botID); appErr != nil {
		return false
	}
	post.UserId = botID
	return true
}
