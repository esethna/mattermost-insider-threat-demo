package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/driver"
)

// These mirror what the Calls plugin (v1.12) posts in the call thread, so its own post
// components render them, including the Agents "Create meeting summary" button.
const (
	callPostType          = "custom_calls"
	callRecordingPostType = "custom_calls_recording"
	callTranscriptionType = "custom_calls_transcription"
	callsBotUsername      = "calls"
)

var callFilenameRe = regexp.MustCompile(`[\\:*?\"<>|\n\s/]`)

// recordCall posts the simulated recording and transcription after a call ends in a demo run channel.
func (p *Plugin) recordCall(post *model.Post) {
	if post.Type != callPostType || propInt(post.GetProp("end_at")) == 0 {
		return
	}
	if runID, appErr := p.API.KVGet("run_channel_" + post.ChannelId); appErr != nil || runID == nil {
		return
	}
	if ok, appErr := p.API.KVCompareAndSet("call_recorded_"+post.Id, nil, []byte("1")); appErr != nil || !ok {
		return
	}
	go func() {
		if err := p.postCallArtifacts(post); err != nil {
			p.API.LogWarn("call recording", "error", err.Error())
		}
	}()
}

func (p *Plugin) postCallArtifacts(call *model.Post) error {
	bot, appErr := p.API.GetUserByUsername(callsBotUsername)
	if appErr != nil || !bot.IsBot {
		return fmt.Errorf("calls bot not found")
	}
	bundle, err := p.API.GetBundlePath()
	if err != nil {
		return err
	}
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(bundle, "assets", "call", name)) }
	threadID := call.Id
	if call.RootId != "" {
		threadID = call.RootId
	}
	base := p.callFilename(call.ChannelId)

	time.Sleep(4 * time.Second)
	video, err := read("recording.mp4")
	if err != nil {
		return err
	}
	videoInfo, appErr := p.API.UploadFile(video, call.ChannelId, base+".mp4")
	if appErr != nil {
		return appErr
	}
	recPost := &model.Post{
		UserId:    bot.Id,
		ChannelId: call.ChannelId,
		RootId:    threadID,
		Type:      callRecordingPostType,
		Message:   "Here's the call recording",
		FileIds:   []string{videoInfo.Id},
	}
	recPost.AddProp("recording_id", model.NewId())
	recPost.AddProp("call_post_id", call.Id)
	if recPost, appErr = p.API.CreatePost(recPost); appErr != nil {
		return appErr
	}

	time.Sleep(6 * time.Second)
	vtt, err := read("transcript.vtt")
	if err != nil {
		return err
	}
	txt, err := read("transcript.txt")
	if err != nil {
		return err
	}
	vttInfo, appErr := p.API.UploadFile(vtt, call.ChannelId, base+".vtt")
	if appErr != nil {
		return appErr
	}
	txtInfo, appErr := p.API.UploadFile(txt, call.ChannelId, base+".txt")
	if appErr != nil {
		return appErr
	}
	// Calls attaches the VTT to the call post without showing it; the plugin API can't, so do it in the DB.
	db := sql.OpenDB(driver.NewConnector(p.Driver, true))
	defer db.Close()
	if _, err := db.Exec("UPDATE FileInfo SET PostId = $1, ChannelId = $2 WHERE Id = $3", call.Id, call.ChannelId, vttInfo.Id); err != nil {
		return fmt.Errorf("attach captions: %w", err)
	}

	captions := []any{map[string]any{"title": "en", "language": "en", "file_id": vttInfo.Id}}
	trPost := &model.Post{
		UserId:    bot.Id,
		ChannelId: call.ChannelId,
		RootId:    threadID,
		Type:      callTranscriptionType,
		Message:   "Here's the call transcription",
		FileIds:   []string{txtInfo.Id},
	}
	trPost.AddProp("call_post_id", call.Id)
	trPost.AddProp("transcription_id", model.NewId())
	trPost.AddProp("captions", captions)
	if _, appErr := p.API.CreatePost(trPost); appErr != nil {
		return appErr
	}

	recPost.AddProp("captions", captions)
	if _, appErr := p.API.UpdatePost(recPost); appErr != nil {
		return appErr
	}
	return nil
}

func (p *Plugin) callFilename(channelID string) string {
	name := channelID
	if ch, appErr := p.API.GetChannel(channelID); appErr == nil {
		name = ch.DisplayName
	}
	if r := []rune(name); len(r) > 24 {
		name = string(r[:24]) + "…"
	}
	return callFilenameRe.ReplaceAllString(fmt.Sprintf("Call_%s_%s", strings.ReplaceAll(name, " ", "_"), time.Now().UTC().Format("2006-01-02_15-04-05")), "_")
}

func propInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}
