package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	cohereAgent      = "cohere-north"
	cohereMaxContext = 60000
)

// executeCohere asks the agent through the Agents plugin bridge and posts the answer as a
// top-level post from the agent's bot, since @-mentions always get a threaded reply.
func (p *Plugin) executeCohere(args *model.CommandArgs) *model.CommandResponse {
	prompt := strings.TrimSpace(strings.TrimPrefix(args.Command, "/cohere"))
	if prompt == "" {
		return ephemeral("Usage: `/cohere <request>`")
	}
	agent, appErr := p.API.GetUserByUsername(cohereAgent)
	if appErr != nil || !agent.IsBot {
		return ephemeral("The " + cohereAgent + " agent isn't set up on this server.")
	}
	transcript := p.channelTranscript(args.ChannelId)

	p.ensureMember(args.ChannelId, agent.Id)
	post, appErr := p.API.CreatePost(&model.Post{UserId: agent.Id, ChannelId: args.ChannelId, Message: "_Drafting…_"})
	if appErr != nil {
		return ephemeral("Couldn't post here: " + appErr.Message)
	}

	go func() {
		answer, err := p.complete(agent.Id, args.UserId, args.ChannelId, transcript, prompt)
		if err != nil {
			p.API.LogWarn("cohere completion", "error", err.Error())
			answer = "Sorry, I couldn't draft that right now. Try again in a moment."
		}
		post.Message = answer
		if _, appErr := p.API.UpdatePost(post); appErr != nil {
			p.API.LogWarn("cohere post", "error", appErr.Error())
		}
		p.stepDone(args.ChannelId, "/cohere")
	}()
	return &model.CommandResponse{}
}

// channelTranscript renders the channel, thread replies included, oldest first. The bridge
// runs without tools, so the agent can't read the channel itself.
func (p *Plugin) channelTranscript(channelID string) string {
	list, appErr := p.API.GetPostsForChannel(channelID, 0, 200)
	if appErr != nil {
		return ""
	}
	names := map[string]string{}
	var lines []string
	for i := len(list.Order) - 1; i >= 0; i-- {
		post := list.Posts[list.Order[i]]
		text := strings.TrimSpace(post.Message)
		for _, a := range post.Attachments() {
			parts := []string{a.Pretext, a.Title, a.Text}
			for _, f := range a.Fields {
				parts = append(parts, fmt.Sprintf("%s: %v", f.Title, f.Value))
			}
			text = strings.TrimSpace(text + "\n" + strings.Join(nonEmpty(parts), "\n"))
		}
		if text == "" {
			continue
		}
		name, ok := names[post.UserId]
		if !ok {
			name = p.username(post.UserId)
			names[post.UserId] = name
		}
		when := time.UnixMilli(post.CreateAt).UTC().Format("Jan 2 15:04 UTC")
		reply := ""
		if post.RootId != "" {
			reply = " (thread reply)"
		}
		lines = append(lines, fmt.Sprintf("[%s] @%s%s: %s", when, name, reply, text))
	}
	out := strings.Join(lines, "\n\n")
	if len(out) > cohereMaxContext {
		out = out[len(out)-cohereMaxContext:]
	}
	return out
}

func (p *Plugin) complete(agentID, userID, channelID, transcript, prompt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"user_id":    userID,
		"channel_id": channelID,
		"posts": []map[string]string{
			{"role": "system", "message": "You are posting a new top-level message in this incident channel for everyone in it, requested by @" + p.username(userID) + ". Don't address the requester, greet or sign the message. @mention people only when the request asks you to. Everything said in the channel so far, oldest first:\n\n" + transcript},
			{"role": "user", "message": prompt},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, "/mattermost-ai/bridge/v1/completion/agent/"+agentID+"/nostream", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp := p.API.PluginHTTP(req)
	if resp == nil {
		return "", fmt.Errorf("agents plugin unreachable")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("agents bridge %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Completion string `json:"completion"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || strings.TrimSpace(out.Completion) == "" {
		return "", fmt.Errorf("empty completion")
	}
	return out.Completion, nil
}

func nonEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
