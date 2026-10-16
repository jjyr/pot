//go:build darwin

package desktop

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/attention"
	"github.com/jjyr/pot/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
	"image"
	"image/draw"
	"image/png"
	"math"
	"time"
)

//go:embed assets/TrayLight.png
var trayLight []byte

//go:embed assets/TrayDark.png
var trayDark []byte

func (s *Service) AssistantReads() []attention.Receipt { return s.reads.Records() }
func (s *Service) SetReducedMotion(value bool) {
	s.mu.Lock()
	s.reduceMotion = value
	s.motionKnown = true
	s.mu.Unlock()
	s.wakeAttention()
}
func (s *Service) ReadAssistant(ctx context.Context, space, owner, replyID string, count uint64) (map[string]any, error) {
	if !core.Identifier(owner) || count > core.MaxSafeInteger {
		return nil, errors.New("invalid assistant read acknowledgement")
	}
	if !(s.main.IsVisible() && s.main.IsFocused()) && !(s.tray.IsVisible() && s.tray.IsFocused()) {
		return nil, errors.New("a visible focused conversation is required")
	}
	value, err := s.spaces.Space(space)
	if err != nil {
		return nil, err
	}
	if !value.Connected {
		return nil, errors.New("Space is offline")
	}
	if _, err = s.observeAttention(); err != nil {
		return nil, err
	}
	receipt, err := s.reads.Read(attention.Receipt{ControlID: value.ControlID, Owner: owner, ReplyID: replyID, Count: count})
	if err != nil {
		return nil, err
	}
	result := map[string]any{"space_id": space, "control_id": receipt.ControlID, "owner": receipt.Owner, "reply_id": receipt.ReplyID, "reply_count": receipt.Count}
	s.app.Event.Emit("pot:read", result)
	s.wakeAttention()
	return result, nil
}
func (s *Service) wakeAttention() {
	select {
	case s.attentionWake <- struct{}{}:
	default:
	}
}
func (s *Service) observeAttention() (attention.Status, error) {
	rows := []attention.Row{}
	for _, space := range s.spaces.Snapshot().Items {
		var catalogue []struct {
			ID                 string `json:"id"`
			ReplyID            string `json:"reply_id"`
			Count              uint64 `json:"reply_count"`
			Responding         bool   `json:"responding"`
			Working            uint64 `json:"working"`
			Failed             bool   `json:"failed"`
			NeedsConfiguration bool   `json:"needs_configuration"`
		}
		if len(space.Assistants) == 0 {
			continue
		}
		if err := json.Unmarshal(space.Assistants, &catalogue); err != nil {
			return attention.Status{}, err
		}
		for _, item := range catalogue {
			rows = append(rows, attention.Row{Receipt: attention.Receipt{ControlID: space.ControlID, Owner: item.ID, ReplyID: item.ReplyID, Count: item.Count}, Connected: space.Connected, Running: item.Responding || item.Working > 0, Failed: item.Failed, NeedsConfiguration: item.NeedsConfiguration})
		}
	}
	return s.reads.Observe(rows)
}

func (s *Service) runAttention(ctx context.Context, status *application.SystemTray) {
	light, err := png.Decode(bytes.NewReader(trayLight))
	if err != nil {
		return
	}
	dark, err := png.Decode(bytes.NewReader(trayDark))
	if err != nil {
		return
	}
	atlas := []image.Image{light, dark}
	cache := map[string][]byte{}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	state, _ := s.observeAttention()
	last := ""
	started := time.Now()
	darkMode := s.app.Env.IsDarkMode()
	for {
		refresh := false
		select {
		case <-ctx.Done():
			return
		case <-s.attentionWake:
			refresh = true
		case <-ticker.C:
		}
		if refresh {
			if next, err := s.observeAttention(); err == nil {
				state = next
			}
			darkMode = s.app.Env.IsDarkMode()
		}
		s.mu.Lock()
		animate := state.Running && s.motionKnown && !s.reduceMotion
		s.mu.Unlock()
		frame := 0
		if state.Running {
			frame = 5
			if animate {
				frame = 1 + int(math.Round((math.Sin(time.Since(started).Seconds()*2*math.Pi/2.7)+1)*3.5))
			}
		}
		variant := 0
		if state.NeedsConfiguration {
			variant |= 2
		}
		if state.Failed {
			variant |= 1
		}
		appearance := 0
		if darkMode {
			appearance = 1
		}
		unread := min(state.Unread, 10)
		signature := fmt.Sprintf("%d/%d/%d/%d", appearance, variant, frame, unread)
		if signature == last {
			continue
		}
		last = signature
		data := cache[signature]
		if data == nil {
			tile := image.NewNRGBA(image.Rect(0, 0, 100, 44))
			draw.Draw(tile, tile.Bounds(), atlas[appearance], image.Pt(int(unread)*100, (variant*9+frame)*44), draw.Src)
			var buffer bytes.Buffer
			if png.Encode(&buffer, tile) != nil {
				continue
			}
			data = buffer.Bytes()
			cache[signature] = data
		}
		if ctx.Err() != nil {
			return
		}
		status.SetIcon(data)
		label := "Pot · Idle"
		if state.Running {
			label = "Pot · Working"
		}
		if state.NeedsConfiguration {
			label += " · Waiting for configuration"
		}
		if state.Failed {
			label += " · Error"
		}
		if state.Unread > 0 {
			label += fmt.Sprintf(" · %d unread", state.Unread)
		}
		status.SetTooltip(label)
	}
}
