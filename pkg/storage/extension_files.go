package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
)

// ExportLocalExtensions completes the recorded SQLite-to-files transition before
// readers start. Atomic files precede the export receipt; a crash retries the
// identical export and never replaces an independently edited configuration.
func ExportLocalExtensions(db *sql.DB, root, home string) error {
	var pending int
	if err := db.QueryRow("SELECT count(*) FROM extension_file_export").Scan(&pending); err != nil {
		return err
	}
	if pending == 0 {
		return nil
	}
	if home == root {
		for _, name := range []string{"skills", "plugins", "AGENTS.md"} {
			old := filepath.Join(root, "agent", name)
			dest := filepath.Join(home, name)
			if _, err := os.Lstat(old); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return err
			}
			if info, err := os.Lstat(dest); err == nil {
				// Older startup code could create the empty global Skill root.
				// Remove only that empty directory; never merge or overwrite data.
				if !info.IsDir() || os.Remove(dest) != nil {
					return fmt.Errorf("extension migration destination already exists: %s", dest)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.Rename(old, dest); err != nil {
				return err
			}
		}
	}
	packages, err := os.ReadDir(filepath.Join(home, "plugins"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range packages {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(home, "plugins", entry.Name(), "plugin.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var d map[string]json.RawMessage
		if err = json.Unmarshal(raw, &d); err != nil {
			return err
		}
		if string(d["schema_version"]) == "2" {
			var name string
			if err = json.Unmarshal(d["id"], &name); err != nil || name != entry.Name() {
				return errors.New("installed plugin name differs from directory")
			}
			if display := d["name"]; len(display) > 0 {
				d["display_name"] = display
			}
			d["name"] = d["id"]
			delete(d, "id")
			d["schema_version"] = json.RawMessage("3")
			if err = instance.WriteJSON(path, d); err != nil {
				return err
			}
		}
	}
	type setting struct {
		Enabled  bool   `json:"enabled"`
		Revision int64  `json:"revision"`
		Source   string `json:"source,omitempty"`
	}
	config := struct {
		Version int                `json:"version"`
		Plugins map[string]setting `json:"plugins"`
		Skills  map[string]setting `json:"skills"`
	}{1, map[string]setting{}, map[string]setting{}}
	// Skill catalogue keys include the absolute path. Move only global keys; project
	// paths and their settings retain their original ownership.
	moved := map[string]string{}
	if home == root {
		entries, err := os.ReadDir(filepath.Join(home, "skills"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				old, _ := core.HashJSON(filepath.Join(root, "agent", "skills", e.Name(), "SKILL.md"))
				current, _ := core.HashJSON(filepath.Join(home, "skills", e.Name(), "SKILL.md"))
				moved[old] = current
			}
		}
	}
	rows, err := db.Query("SELECT kind,id,enabled,revision,source FROM retired_extension_settings ORDER BY kind,id")
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var kind, id string
		var v setting
		if err = rows.Scan(&kind, &id, &v.Enabled, &v.Revision, &v.Source); err != nil {
			rows.Close()
			return err
		}
		if kind == "plugins" {
			config.Plugins[id] = v
		} else {
			if next := moved[id]; next != "" {
				id = next
			}
			config.Skills[id] = v
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = exportConfig(filepath.Join(root, "extensions.json"), config, count > 0); err != nil {
		return err
	}
	rows, err = db.Query("SELECT id,revision,body FROM retired_mcp_servers ORDER BY id")
	if err != nil {
		return err
	}
	servers := []json.RawMessage{}
	for rows.Next() {
		var id string
		var revision int64
		var raw []byte
		var server map[string]json.RawMessage
		if err = rows.Scan(&id, &revision, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &server); err != nil {
			rows.Close()
			return err
		}
		server["id"], _ = json.Marshal(id)
		server["revision"], _ = json.Marshal(revision)
		raw, err = json.Marshal(server)
		if err != nil {
			rows.Close()
			return err
		}
		servers = append(servers, raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	mcp := struct {
		Version int               `json:"version"`
		Servers []json.RawMessage `json:"servers"`
	}{1, servers}
	if err = exportConfig(filepath.Join(root, "mcp.json"), mcp, len(servers) > 0); err != nil {
		return err
	}
	return Transaction(context.Background(), db, func(tx *sql.Tx) error {
		for _, table := range []string{"retired_extension_settings", "retired_mcp_servers", "extension_file_export"} {
			if _, err := tx.Exec("DELETE FROM " + table); err != nil {
				return err
			}
		}
		return nil
	})
}

func exportConfig(path string, value any, hasLegacy bool) error {
	if raw, err := os.ReadFile(path); err == nil {
		if !hasLegacy {
			return nil
		}
		a, e := core.HashJSON(json.RawMessage(raw))
		if e != nil {
			return e
		}
		b, e := core.HashJSON(value)
		if e != nil {
			return e
		}
		if a != b {
			return fmt.Errorf("configuration export conflicts with existing file: %s", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return instance.WriteJSON(path, value)
}
