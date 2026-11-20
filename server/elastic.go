package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

// start runs the Elastic workflow, which calls back into this plugin to post the alert and
// start the run. It returns a message for the user who asked.
func (p *Plugin) start(userID, teamID string) string {
	cfg := p.getConfig()
	if cfg.KibanaURL == "" || cfg.ElasticAPIKey == "" || len(cfg.CallbackSecret) < 24 {
		return "The demo isn't configured: Kibana URL, Elastic API key and callback secret are required."
	}
	instance, err := p.instance(cfg.PreviewDomain)
	if err != nil {
		return "The demo can't start here: " + err.Error()
	}
	team, appErr := p.API.GetTeam(teamID)
	if appErr != nil {
		return "Open the demo team first, then try again."
	}
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return "Couldn't look up your account."
	}

	p.mu.Lock()
	if time.Since(p.lastStart) < 30*time.Second {
		p.mu.Unlock()
		return "The demo is already starting. Watch ~soc-alerts."
	}
	p.lastStart = time.Now()
	p.mu.Unlock()

	if err := runWorkflow(cfg, map[string]string{"instance": instance, "team": team.Name, "analyst": user.Username}); err != nil {
		p.mu.Lock()
		p.lastStart = time.Time{}
		p.mu.Unlock()
		p.API.LogError("start demo workflow", "error", err.Error())
		return "Elastic didn't start the workflow. Try again in a moment."
	}
	return "Starting the insider threat demo. The Elastic alert will appear in ~soc-alerts in a few seconds."
}

func runWorkflow(cfg configuration, inputs map[string]string) error {
	body, _ := json.Marshal(map[string]any{"inputs": inputs})
	req, err := http.NewRequest(http.MethodPost, cfg.KibanaURL+"/api/workflows/workflow/"+cfg.WorkflowID+"/run", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "ApiKey "+cfg.ElasticAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("kbn-xsrf", "true")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("workflow run: %s: %s", resp.Status, msg)
	}
	return nil
}
