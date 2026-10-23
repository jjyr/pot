package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	"os"
	"path/filepath"
)

type ResourceFiles struct{ Root string }
type resourceReference struct {
	Digest string `json:"digest"`
}

func (f ResourceFiles) reference(node, task, run string) string {
	key, _ := core.HashJSON([]string{node, task, run})
	return filepath.Join(f.Root, "runs", key+".json")
}
func (f ResourceFiles) Read(node, task, run string) (json.RawMessage, string, error) {
	raw, err := os.ReadFile(f.reference(node, task, run))
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var ref resourceReference
	if err = core.DecodeJSON(raw, &ref); err != nil {
		return nil, "", err
	}
	if len(ref.Digest) != 64 || filepath.Base(ref.Digest) != ref.Digest {
		return nil, "", errors.New("invalid resource reference")
	}
	raw, err = os.ReadFile(filepath.Join(f.Root, "objects", ref.Digest, "resources.json"))
	if err != nil {
		return nil, "", err
	}
	hash, err := core.HashJSON(json.RawMessage(raw))
	if err != nil || hash != ref.Digest {
		return nil, "", errors.New("resource snapshot checksum mismatch")
	}
	canonical, err := core.CanonicalJSON(json.RawMessage(raw))
	return canonical, hash, err
}
func (f ResourceFiles) Pin(node, task, run string, raw json.RawMessage) (json.RawMessage, string, error) {
	if old, hash, err := f.Read(node, task, run); err != nil || old != nil {
		return old, hash, err
	}
	canonical, err := core.CanonicalJSON(raw)
	if err != nil {
		return nil, "", err
	}
	raw = canonical
	hash, err := core.HashJSON(raw)
	if err != nil {
		return nil, "", err
	}
	dest := filepath.Join(f.Root, "objects", hash, "resources.json")
	if err = exportConfig(dest, raw, true); err != nil {
		return nil, "", err
	}
	if err = instance.WriteJSON(f.reference(node, task, run), resourceReference{hash}); err != nil {
		return nil, "", err
	}
	return raw, hash, nil
}

// ExportResourceFiles decodes the old resource transport once. Runtime code no
// longer serializes Skill trees or stores resource bodies in execution databases.
func ExportResourceFiles(db *sql.DB, cache string) error {
	rows, err := db.Query("SELECT control_id,task_id,run_id,digest,body FROM resource_file_export")
	if err != nil {
		return err
	}
	type item struct{ node, task, run, digest, body string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.node, &v.task, &v.run, &v.digest, &v.body); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range items {
		hash, e := core.HashJSON(json.RawMessage(v.body))
		if e != nil || hash != v.digest {
			return errors.New("original resource checksum mismatch")
		}
		var body map[string]json.RawMessage
		if err = json.Unmarshal([]byte(v.body), &body); err != nil {
			return err
		}
		var old []struct {
			Builtin bool              `json:"builtin"`
			Files   map[string]string `json:"files"`
		}
		if err = json.Unmarshal(body["roots"], &old); err != nil && len(body["roots"]) > 0 {
			return err
		}
		roots := []core.SkillRoot{}
		for i, tree := range old {
			dest := filepath.Join(cache, "objects", v.digest, "skills", fmt.Sprint(i))
			if err = os.MkdirAll(dest, 0700); err != nil {
				return err
			}
			for name, value := range tree.Files {
				if !core.SafeRelativePath(name) {
					return errors.New("invalid archived skill path")
				}
				data, e := base64.RawStdEncoding.DecodeString(value)
				if e != nil {
					return e
				}
				path := filepath.Join(dest, filepath.FromSlash(name))
				if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					return err
				}
				if err = os.WriteFile(path, data, 0700); err != nil {
					return err
				}
			}
			roots = append(roots, core.SkillRoot{Path: dest, Location: dest, Builtin: tree.Builtin})
		}
		if len(body["roots"]) > 0 {
			body["roots"], _ = json.Marshal(roots)
		}
		raw, e := json.Marshal(body)
		if e != nil {
			return e
		}
		if _, _, err = (ResourceFiles{cache}).Pin(v.node, v.task, v.run, raw); err != nil {
			return err
		}
	}
	return Transaction(context.Background(), db, func(tx *sql.Tx) error { _, err := tx.Exec("DELETE FROM resource_file_export"); return err })
}
