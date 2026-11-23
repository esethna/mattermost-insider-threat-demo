package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const botUsername = "elastic-security"

type configuration struct {
	KibanaURL      string
	ElasticAPIKey  string
	WorkflowID     string
	CallbackSecret string
	PreviewDomain  string
}

type Plugin struct {
	plugin.MattermostPlugin

	mu        sync.RWMutex
	config    configuration
	botID     string
	soarID    string
	lastStart time.Time
}

func (p *Plugin) getConfig() configuration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

func (p *Plugin) OnConfigurationChange() error {
	var c configuration
	if err := p.API.LoadPluginConfiguration(&c); err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	c.KibanaURL = strings.TrimRight(strings.TrimSpace(c.KibanaURL), "/")
	if c.WorkflowID == "" {
		c.WorkflowID = "insider-threat-preview-start"
	}
	if c.PreviewDomain == "" {
		c.PreviewDomain = "test.mattermost.cloud"
	}
	p.mu.Lock()
	p.config = c
	p.mu.Unlock()
	return nil
}

func (p *Plugin) OnActivate() error {
	botID, err := p.ensureBot()
	if err != nil {
		return err
	}
	p.botID = botID
	if p.soarID, err = p.ensureSoarBot(); err != nil {
		return err
	}
	if err := p.API.RegisterCommand(&model.Command{
		Trigger:          "demo",
		DisplayName:      "Insider threat demo",
		Description:      "Start the insider threat demo",
		AutoComplete:     true,
		AutoCompleteDesc: "Start the insider threat demo from the Elastic alert",
		AutoCompleteHint: "start",
	}); err != nil {
		return err
	}
	if err := p.API.RegisterCommand(&model.Command{
		Trigger:          "soar",
		DisplayName:      "SOAR",
		Description:      "Run a SOAR response action",
		AutoComplete:     true,
		AutoCompleteDesc: "Run a SOAR response action on a user and host",
		AutoCompleteHint: "restrict|collect <user> <host>",
	}); err != nil {
		return err
	}
	return p.API.RegisterCommand(&model.Command{
		Trigger:          "cohere",
		DisplayName:      "Cohere North",
		Description:      "Ask Cohere North and post its answer in the channel",
		AutoComplete:     true,
		AutoCompleteDesc: "Ask Cohere North, using this channel as context, and post the answer here",
		AutoCompleteHint: "<request>",
	})
}

// ensureBot reuses a dataset's elastic-security bot so seeded alerts and new ones share an author.
func (p *Plugin) ensureBot() (string, error) {
	if u, appErr := p.API.GetUserByUsername(botUsername); appErr == nil && u.IsBot {
		return u.Id, nil
	}
	id, appErr := p.API.EnsureBotUser(&model.Bot{
		Username:    botUsername,
		DisplayName: "Elastic Security",
		Description: "Posts Elastic Security alerts",
	})
	if appErr != nil {
		return "", fmt.Errorf("ensure bot: %w", appErr)
	}
	if bundle, err := p.API.GetBundlePath(); err == nil {
		if img, err := os.ReadFile(filepath.Join(bundle, "assets", "elastic-security.png")); err == nil {
			_ = p.API.SetProfileImage(id, img)
		}
	}
	return id, nil
}

// instance returns the preview instance name from the site URL, or an error if this server
// isn't on the preview domain the workflow is allowed to call.
func (p *Plugin) instance(domain string) (string, error) {
	site := p.API.GetConfig().ServiceSettings.SiteURL
	if site == nil || *site == "" {
		return "", fmt.Errorf("the site URL isn't set")
	}
	u, err := url.Parse(*site)
	if err != nil || u.Scheme != "https" || u.Port() != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("the site URL %q must be https://<instance>.%s", *site, domain)
	}
	name, ok := strings.CutSuffix(strings.ToLower(u.Hostname()), "."+domain)
	if !ok || name == "" || strings.Contains(name, ".") {
		return "", fmt.Errorf("the site URL %q must be https://<instance>.%s", *site, domain)
	}
	return name, nil
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	fields := strings.Fields(args.Command)
	if len(fields) > 0 && fields[0] == "/soar" {
		return p.executeSoar(args), nil
	}
	if len(fields) > 0 && fields[0] == "/cohere" {
		return p.executeCohere(args), nil
	}
	if len(fields) < 2 || fields[1] != "start" {
		return ephemeral("Usage: `/demo start`"), nil
	}
	var first string
	p.start(args.UserId, args.TeamId, func(msg string) {
		if first == "" {
			first = msg
			return
		}
		p.API.SendEphemeralPost(args.UserId, &model.Post{UserId: p.botID, ChannelId: args.ChannelId, Message: msg})
	})
	return ephemeral(first), nil
}

func ephemeral(text string) *model.CommandResponse {
	return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
}
