package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const playbooksAPI = "/playbooks/api/v0"

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/start":
		p.handleStart(w, r)
	case "/api/v1/alert":
		p.callback(w, r, p.handleAlert)
	case "/api/v1/run":
		p.callback(w, r, p.handleRun)
	case "/api/v1/message":
		p.callback(w, r, p.handleMessage)
	case "/soar/action":
		p.handleSoarAction(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *Plugin) handleStart(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")
	if userID == "" {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	var in struct {
		TeamID    string `json:"team_id"`
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil || in.TeamID == "" {
		http.Error(w, "team_id required", http.StatusBadRequest)
		return
	}
	if _, appErr := p.API.GetTeamMember(in.TeamID, userID); appErr != nil {
		http.Error(w, "not on this team", http.StatusForbidden)
		return
	}
	inChannel := false
	if in.ChannelID != "" {
		_, appErr := p.API.GetChannelMember(in.ChannelID, userID)
		inChannel = appErr == nil
	}
	var first string
	p.start(userID, in.TeamID, func(msg string) {
		if first == "" {
			first = msg
		}
		p.API.LogInfo("demo start", "user_id", userID, "result", msg)
		if inChannel {
			p.API.SendEphemeralPost(userID, &model.Post{UserId: p.botID, ChannelId: in.ChannelID, Message: msg})
		}
	})
	writeJSON(w, map[string]string{"message": first})
}

// callback checks the shared secret the Elastic workflow sends, then decodes the JSON body.
func (p *Plugin) callback(w http.ResponseWriter, r *http.Request, h func(json.RawMessage) (any, error)) {
	secret := p.getConfig().CallbackSecret
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(secret) < 24 || !ok || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	out, err := h(body)
	if err != nil {
		p.API.LogWarn("demo callback failed", "path", r.URL.Path, "error", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, out)
}

func (p *Plugin) handleAlert(body json.RawMessage) (any, error) {
	var in struct {
		Team       string         `json:"team"`
		Channel    string         `json:"channel"`
		Attachment map[string]any `json:"attachment"`
		Priority   string         `json:"priority"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	if in.Channel != "soc-alerts" {
		return nil, fmt.Errorf("alerts can only go to soc-alerts")
	}
	ch, appErr := p.API.GetChannelByNameForTeamName(in.Team, in.Channel, false)
	if appErr != nil {
		return nil, fmt.Errorf("channel %s/%s: %s", in.Team, in.Channel, appErr.Message)
	}
	p.ensureMember(ch.Id, p.botID)
	post := &model.Post{UserId: p.botID, ChannelId: ch.Id}
	post.AddProp(model.PostPropsAttachments, []any{in.Attachment})
	if in.Priority != "" {
		prio := in.Priority
		post.Metadata = &model.PostMetadata{Priority: &model.PostPriority{Priority: &prio}}
	}
	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		return nil, fmt.Errorf("post alert: %s", appErr.Message)
	}
	return map[string]string{"post_id": created.Id, "channel_id": ch.Id}, nil
}

func (p *Plugin) handleRun(body json.RawMessage) (any, error) {
	var in struct {
		Team       string            `json:"team"`
		Playbook   string            `json:"playbook"`
		Owner      string            `json:"owner"`
		PostID     string            `json:"post_id"`
		Name       string            `json:"name"`
		Summary    string            `json:"summary"`
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	team, appErr := p.API.GetTeamByName(in.Team)
	if appErr != nil {
		return nil, fmt.Errorf("team %s: %s", in.Team, appErr.Message)
	}
	owner, appErr := p.API.GetUserByUsername(in.Owner)
	if appErr != nil {
		return nil, fmt.Errorf("owner %s: %s", in.Owner, appErr.Message)
	}
	if _, appErr := p.API.GetTeamMember(team.Id, owner.Id); appErr != nil {
		return nil, fmt.Errorf("owner %s isn't on team %s", in.Owner, in.Team)
	}
	alert, appErr := p.API.GetPost(in.PostID)
	if appErr != nil || alert.UserId != p.botID {
		return nil, fmt.Errorf("post %s isn't an alert from this plugin", in.PostID)
	}

	var playbooks struct {
		Items []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			CreateAt int64  `json:"create_at"`
		} `json:"items"`
	}
	if err := p.playbooks(owner.Id, http.MethodGet, "/playbooks?per_page=100&team_id="+team.Id, nil, &playbooks); err != nil {
		return nil, err
	}
	playbookID, newest := "", int64(-1)
	for _, pb := range playbooks.Items {
		if pb.Title == in.Playbook && pb.CreateAt > newest {
			playbookID, newest = pb.ID, pb.CreateAt
		}
	}
	if playbookID == "" {
		return nil, fmt.Errorf("playbook %q not found on team %s", in.Playbook, in.Team)
	}

	var run struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ChannelID string `json:"channel_id"`
	}
	err := p.playbooks(owner.Id, http.MethodPost, "/runs", map[string]any{
		"name": in.Name, "owner_user_id": owner.Id, "team_id": team.Id, "playbook_id": playbookID,
		"post_id": in.PostID, "summary": in.Summary, "create_public_run": false, "type": "playbook",
	}, &run)
	if err != nil {
		return nil, err
	}
	if appErr := p.API.KVSet("run_channel_"+run.ChannelID, []byte(run.ID)); appErr != nil {
		return nil, fmt.Errorf("remember run channel: %s", appErr.Message)
	}
	if appErr := p.API.KVSet("run_owner_"+run.ID, []byte(owner.Id)); appErr != nil {
		return nil, fmt.Errorf("remember run owner: %s", appErr.Message)
	}

	var fields []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := p.playbooks(owner.Id, http.MethodGet, "/runs/"+run.ID+"/property_fields", nil, &fields); err != nil {
		p.API.LogWarn("run property fields", "error", err.Error())
	}
	for _, f := range fields {
		if v, ok := in.Properties[f.Name]; ok {
			if err := p.playbooks(owner.Id, http.MethodPut, "/runs/"+run.ID+"/property_fields/"+f.ID+"/value", map[string]string{"value": v}, nil); err != nil {
				p.API.LogWarn("set run property", "field", f.Name, "error", err.Error())
			}
		}
	}
	reply := &model.Post{
		UserId:    p.botID,
		ChannelId: alert.ChannelId,
		RootId:    alert.Id,
		Message:   fmt.Sprintf("Triggered response Playbook [%s](/playbooks/runs/%s) from this alert.", run.Name, run.ID),
	}
	if _, appErr := p.API.CreatePost(reply); appErr != nil {
		p.API.LogWarn("reply on alert", "error", appErr.Error())
	}
	return map[string]string{"run_id": run.ID, "channel_id": run.ChannelID, "name": run.Name}, nil
}

func (p *Plugin) handleMessage(body json.RawMessage) (any, error) {
	var in struct {
		ChannelID string `json:"channel_id"`
		Message   string `json:"message"`
		Pin       bool   `json:"pin"`
		// Persistent notifications need urgent priority and an @mention in the message.
		Priority   string `json:"priority"`
		Ack        bool   `json:"ack"`
		Persistent bool   `json:"persistent"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	if v, appErr := p.API.KVGet("run_channel_" + in.ChannelID); appErr != nil || v == nil {
		return nil, fmt.Errorf("channel %s isn't a run this plugin started", in.ChannelID)
	}
	p.ensureMember(in.ChannelID, p.botID)
	post := &model.Post{UserId: p.botID, ChannelId: in.ChannelID, Message: in.Message, IsPinned: in.Pin}
	if in.Priority != "" || in.Ack || in.Persistent {
		post.Metadata = &model.PostMetadata{Priority: &model.PostPriority{
			Priority:                model.NewPointer(in.Priority),
			RequestedAck:            model.NewPointer(in.Ack),
			PersistentNotifications: model.NewPointer(in.Persistent),
		}}
	}
	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		return nil, fmt.Errorf("post message: %s", appErr.Message)
	}
	return map[string]string{"post_id": created.Id}, nil
}

func (p *Plugin) ensureMember(channelID, botID string) {
	if _, appErr := p.API.GetChannelMember(channelID, botID); appErr != nil {
		if _, appErr := p.API.AddChannelMember(channelID, botID); appErr != nil {
			p.API.LogWarn("add bot to channel", "channel", channelID, "error", appErr.Error())
		}
	}
}

// playbooks calls the Playbooks plugin's REST API on behalf of userID.
func (p *Plugin) playbooks(userID, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, playbooksAPI+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Mattermost-Plugin-Acting-User-Id", userID)
	req.Header.Set("Content-Type", "application/json")
	resp := p.API.PluginHTTP(req)
	if resp == nil {
		return fmt.Errorf("playbooks %s %s: no response", method, path)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("playbooks %s %s: %s: %s", method, path, resp.Status, msg)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
