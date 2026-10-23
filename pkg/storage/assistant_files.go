package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/core"
)

type AssistantProfile struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
}

// Read fixed profile sources without following links out of their directory or
// treating pipes/devices as configuration. This is input validation, not a grant.
func ReadAssistantFile(directory, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("assistant configuration must be a regular file")
	}
	if limit <= 0 {
		return io.ReadAll(f)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("assistant configuration exceeds size limit")
	}
	return data, err
}

var assistantFileLocks sync.Map
var assistantExportMu sync.Mutex

// UI writers share a lock; content revisions also detect ordinary external edits.
func LockAssistantFiles(directory string) func() {
	key, _ := filepath.Abs(directory)
	value, _ := assistantFileLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func WriteAssistantFile(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".assistant-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	return syncAssistantDirectory(filepath.Dir(path))
}

func syncAssistantDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// Export never overwrites a different existing file. A crash before the DB
// receipt is safe to retry against the identical already-published bytes.
func exportAssistantFile(path string, body []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("assistant export destination is not a regular file: %s", path)
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, body) {
			return fmt.Errorf("assistant export conflicts with existing file: %s", path)
		}
		// A prior process may have stopped after publishing but before syncing.
		// Finish durability before allowing its pending database copy to be cleared.
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
		return syncAssistantDirectory(filepath.Dir(path))
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".assistant-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Link(f.Name(), path)
	}
	if os.IsExist(err) {
		return exportAssistantFile(path, body)
	}
	if err != nil {
		return err
	}
	return syncAssistantDirectory(filepath.Dir(path))
}

func ReadAssistantProfile(directory string) (AssistantProfile, uint64, string, error) {
	var profile AssistantProfile
	raw, err := ReadAssistantFile(directory, "profile.json", 262144)
	if err != nil {
		return profile, 0, "", err
	}
	if err = core.DecodeJSON(raw, &profile); err != nil {
		return profile, 0, "", err
	}
	if profile.SchemaVersion != 1 {
		return profile, 0, "", errors.New("unsupported assistant profile file")
	}
	avatar, err := ReadAssistantFile(directory, "avatar.png", 8*1024*1024)
	if err != nil {
		return profile, 0, "", err
	}
	hash := sha256.New()
	for _, content := range [][]byte{raw, avatar} {
		_ = binary.Write(hash, binary.BigEndian, uint64(len(content)))
		_, _ = hash.Write(content)
	}
	// JSON-safe nonzero revision; it changes when a file editor changes content.
	revision := (binary.BigEndian.Uint64(hash.Sum(nil)) >> 12) + 1
	return profile, revision, "data:image/png;base64," + base64.StdEncoding.EncodeToString(avatar), nil
}

func ReadAssistantInstructions(directory string) (Rules, error) {
	data, err := ReadAssistantFile(directory, "AGENTS.md", 0)
	if os.IsNotExist(err) {
		data, err = nil, nil
	}
	if err != nil {
		return Rules{}, err
	}
	digest := sha256.Sum256(data)
	return Rules{SchemaVersion: 1, Text: strings.ToValidUTF8(string(data), "�"), Revision: hex.EncodeToString(digest[:])}, nil
}

// ExportAssistantInstructions completes the SQL migration before any live reader.
func ExportAssistantInstructions(db *sql.DB, directory string) error {
	var raw string
	err := db.QueryRow("SELECT body FROM assistant_instruction_file_export WHERE singleton=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var rules Rules
	if err := core.DecodeJSON([]byte(raw), &rules); err != nil || rules.SchemaVersion != 1 {
		return errors.New("invalid instructions export")
	}
	if err := exportAssistantFile(filepath.Join(directory, "AGENTS.md"), []byte(rules.Text)); err != nil {
		return err
	}
	return Transaction(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO metadata(key,value) VALUES('assistant.instructions.exported','true') ON CONFLICT(key) DO UPDATE SET value='true'"); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM assistant_instruction_file_export WHERE singleton=1")
		return err
	})
}

// ExportAssistantFiles is also the durable file-publication outbox for creation.
// SQLite owns registration; only files own live profile/rule content.
func ExportAssistantFiles(db *sql.DB, root string) error {
	assistantExportMu.Lock()
	defer assistantExportMu.Unlock()
	rows, err := db.Query("SELECT id,name,avatar,character,seed,rules FROM assistant_file_export ORDER BY id")
	if err != nil {
		return err
	}
	type item struct{ id, name, avatar, character, seed, rules string }
	var items []item
	for rows.Next() {
		var value item
		if err = rows.Scan(&value.id, &value.name, &value.avatar, &value.character, &value.seed, &value.rules); err != nil {
			rows.Close()
			return err
		}
		items = append(items, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, value := range items {
		if !core.Identifier(value.id) {
			return errors.New("invalid assistant export identity")
		}
		directory := filepath.Join(root, "assistants", value.id)
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("assistant export requires a real directory: %s", directory)
		}
		// Make newly created directory entries durable before clearing the outbox.
		for _, parent := range []string{filepath.Dir(directory), root} {
			if err := syncAssistantDirectory(parent); err != nil {
				return err
			}
		}
		profile, _ := json.MarshalIndent(AssistantProfile{1, value.name}, "", "  ")
		fallback, err := assistantavatar.Render(value.character, value.seed)
		if err != nil {
			return err
		}
		avatar := value.avatar
		if avatar == "" {
			avatar = fallback
		}
		for _, file := range []struct{ name, content string }{{"avatar.png", avatar}, {"default-avatar.png", fallback}} {
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(file.content, "data:image/png;base64,"))
			if err != nil {
				return err
			}
			if err := exportAssistantFile(filepath.Join(directory, file.name), data); err != nil {
				return err
			}
		}
		if err := exportAssistantFile(filepath.Join(directory, "profile.json"), append(profile, '\n')); err != nil {
			return err
		}
		// A conversation's current rules supersede the registry's initial rules.
		conversation := filepath.Join(directory, "conversation.sqlite")
		liveRules := false
		if _, err := os.Stat(conversation); err == nil {
			c, err := OpenDatabase(conversation, ConversationDatabase)
			if err != nil {
				return err
			}
			var pending int
			err = c.QueryRow("SELECT count(*) FROM assistant_instruction_file_export").Scan(&pending)
			if err == nil && pending > 0 {
				err = ExportAssistantInstructions(c, directory)
				liveRules = true
			}
			if err == nil && !liveRules {
				err = c.QueryRow("SELECT EXISTS(SELECT 1 FROM metadata WHERE key='assistant.instructions.exported')").Scan(&liveRules)
			}
			err = errors.Join(err, c.Close())
			if err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if !liveRules {
			if err := exportAssistantFile(filepath.Join(directory, "AGENTS.md"), []byte(value.rules)); err != nil {
				return err
			}
		}
		if _, err := db.ExecContext(context.Background(), "DELETE FROM assistant_file_export WHERE id=?", value.id); err != nil {
			return err
		}
	}
	return nil
}
