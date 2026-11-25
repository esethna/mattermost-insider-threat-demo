package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	soarUsername  = "soar"
	soarActionURL = "/plugins/com.esethna.insider-threat-demo/soar/action"
)

type soarRun struct {
	ID, User, Host, Custody, Hash string
}

type soarAction struct {
	running string
	color   string
	title   string
	author  string
	fields  func(r soarRun) []*model.MessageAttachmentField
	buttons []soarButton
}

type soarButton struct {
	id, name, style string
	reply           func(r soarRun) string
}

var soarActions = map[string]soarAction{
	"restrict": {
		running: "Restricting data movement",
		color:   "#E8912D",
		title:   ":lock: Data movement restricted",
		author:  "SOAR · Endpoint containment",
		fields: func(r soarRun) []*model.MessageAttachmentField {
			return []*model.MessageAttachmentField{
				{Title: "User", Value: r.User, Short: true},
				{Title: "Host", Value: r.Host, Short: true},
				{Title: "Blocked", Value: "Printing, removable media, file transfers"},
				{Title: "Account and badge", Value: "Active (unchanged)", Short: true},
				{Title: "Auto-revert", Value: "72h unless extended", Short: true},
			}
		},
		buttons: []soarButton{
			{id: "runlog", name: "View run log", reply: func(r soarRun) string {
				return fmt.Sprintf("**%s run log**\n```\n+0.0s  EDR    lock USB mass storage      %s  ok\n+0.4s  DLP    hold print queue            %s  ok\n+0.9s  Proxy  block uploads to web/cloud  %s  ok\n+1.3s  EDR    tag host: insider-watch     %s  ok\n+1.8s  done   4/4 steps, 0 errors\n```", r.ID, r.Host, r.User, r.User, r.Host)
			}},
			{id: "rollback", name: "Roll back", style: "danger", reply: func(r soarRun) string {
				return fmt.Sprintf(":no_entry: Rollback of %s is locked to the incident commander. Ask @marcus.bell to release it.", r.ID)
			}},
		},
	},
	"collect": {
		running: "Preserving evidence",
		color:   "#5B5FC7",
		title:   ":file_cabinet: Evidence preserved",
		author:  "SOAR · Digital forensics",
		fields: func(r soarRun) []*model.MessageAttachmentField {
			return []*model.MessageAttachmentField{
				{Title: "User", Value: r.User, Short: true},
				{Title: "Host", Value: r.Host, Short: true},
				{Title: "Collected", Value: "Print job, spool file, endpoint logs (72h)"},
				{Title: "Custody", Value: "Legal hold " + r.Custody, Short: true},
				{Title: "SHA-256", Value: "`" + r.Hash[:12] + "…`", Short: true},
			}
		},
		buttons: []soarButton{
			{id: "record", name: "Open evidence record", style: "primary", reply: func(r soarRun) string {
				return fmt.Sprintf("**Evidence record EV-%s** (legal hold %s)\n```\nSource     %s (%s)\nItems      print job, spool file, endpoint logs (72h)\nSHA-256    %s\nStored     evidence vault, write-once\nCustody    collected and sealed by SOAR (%s)\n```", r.Custody[3:], r.Custody, r.Host, r.User, r.Hash, r.ID)
			}},
			{id: "release", name: "Request hold release", reply: func(r soarRun) string {
				return fmt.Sprintf(":lock: Legal hold %s can only be released by Legal. Your request has been sent for review, and the hold stays in place until they approve it.", r.Custody)
			}},
		},
	},
}

// ensureSoarBot reuses a dataset's soar bot if there is one, like ensureBot.
func (p *Plugin) ensureSoarBot() (string, error) {
	if u, appErr := p.API.GetUserByUsername(soarUsername); appErr == nil && u.IsBot {
		return u.Id, nil
	}
	id, appErr := p.API.EnsureBotUser(&model.Bot{
		Username:    soarUsername,
		DisplayName: "SOAR",
		Description: "Security orchestration and response actions",
	})
	if appErr != nil {
		return "", fmt.Errorf("ensure soar bot: %w", appErr)
	}
	if bundle, err := p.API.GetBundlePath(); err == nil {
		if img, err := os.ReadFile(filepath.Join(bundle, "assets", "soar.png")); err == nil {
			_ = p.API.SetProfileImage(id, img)
		}
	}
	return id, nil
}

func (p *Plugin) executeSoar(args *model.CommandArgs) *model.CommandResponse {
	fields := strings.Fields(args.Command)
	usage := "Usage: `/soar restrict|collect <user> <host>`"
	if len(fields) != 4 {
		return ephemeral(usage)
	}
	kind := fields[1]
	action, ok := soarActions[kind]
	if !ok {
		return ephemeral(usage)
	}
	hash := make([]byte, 32)
	_, _ = rand.Read(hash)
	run := soarRun{
		ID:      fmt.Sprintf("SOAR-%06d", mrand.Intn(1000000)),
		User:    fields[2],
		Host:    fields[3],
		Custody: fmt.Sprintf("LH-%04d", mrand.Intn(10000)),
		Hash:    hex.EncodeToString(hash),
	}

	p.ensureMember(args.ChannelId, p.soarID)
	post := &model.Post{UserId: p.soarID, ChannelId: args.ChannelId}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{{
		Color:      "#8A8F98",
		AuthorName: action.author,
		Title:      fmt.Sprintf(":hourglass_flowing_sand: %s…", action.running),
		Text:       fmt.Sprintf("%s on %s · run %s", run.User, run.Host, run.ID),
	}})
	post, appErr := p.API.CreatePost(post)
	if appErr != nil {
		return ephemeral("SOAR couldn't post here: " + appErr.Message)
	}

	go p.completeStep(args.ChannelId, args.UserId, "/soar "+kind)

	requester := p.username(args.UserId)
	go func() {
		time.Sleep(2 * time.Second)
		actions := make([]*model.PostAction, 0, len(action.buttons))
		for _, b := range action.buttons {
			actions = append(actions, &model.PostAction{
				Id:    b.id,
				Name:  b.name,
				Style: b.style,
				Type:  model.PostActionTypeButton,
				Integration: &model.PostActionIntegration{
					URL: soarActionURL,
					Context: map[string]any{
						"kind": kind, "button": b.id, "id": run.ID, "user": run.User,
						"host": run.Host, "custody": run.Custody, "hash": run.Hash,
					},
				},
			})
		}
		model.ParseMessageAttachment(post, []*model.MessageAttachment{{
			Color:      action.color,
			AuthorName: action.author,
			Title:      action.title,
			Text:       fmt.Sprintf("Completed in 1.8s · run %s", run.ID),
			Fields:     action.fields(run),
			Actions:    actions,
			Footer:     fmt.Sprintf("Requested by @%s", requester),
		}})
		if _, appErr := p.API.UpdatePost(post); appErr != nil {
			p.API.LogWarn("soar result", "error", appErr.Error())
		}
	}()
	return &model.CommandResponse{}
}

func (p *Plugin) handleSoarAction(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Mattermost-User-ID") == "" {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	var req model.PostActionIntegrationRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	str := func(k string) string { s, _ := req.Context[k].(string); return s }
	run := soarRun{ID: str("id"), User: str("user"), Host: str("host"), Custody: str("custody"), Hash: str("hash")}
	text := "Unknown SOAR action."
	if len(run.Hash) == 64 {
		for _, b := range soarActions[str("kind")].buttons {
			if b.id == str("button") {
				text = b.reply(run)
			}
		}
	}
	writeJSON(w, model.PostActionIntegrationResponse{EphemeralText: text})
}

func (p *Plugin) username(userID string) string {
	if u, appErr := p.API.GetUser(userID); appErr == nil {
		return u.Username
	}
	return userID
}
