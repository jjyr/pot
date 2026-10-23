package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// External editors and ordinary execution tools share the UI's file source.
// Watch file identity/metadata rather than repeatedly decoding every avatar.
func (s *Service) assistantFileStamp() string {
	root := filepath.Join(s.options.Root, "assistants")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err.Error()
	}
	var stamp strings.Builder
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, name := range []string{"profile.json", "avatar.png", "default-avatar.png", "AGENTS.md"} {
			path := filepath.Join(root, entry.Name(), name)
			info, err := os.Stat(path)
			if err != nil {
				fmt.Fprintf(&stamp, "%s:%v\n", path, err)
			} else {
				revision, err := taskFileRevision(info)
				fmt.Fprintf(&stamp, "%s:%s:%v\n", path, revision, err)
			}
		}
	}
	return stamp.String()
}

func (s *Service) watchAssistantFiles() {
	previous := s.assistantFileStamp()
	s.fileWG.Add(1)
	go func() {
		defer s.fileWG.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				current := s.assistantFileStamp()
				if current != previous {
					previous = current
					s.Changes.Notify("assistants")
					s.Changes.Notify("instructions")
				}
			}
		}
	}()
}
