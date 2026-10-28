package storage

import (
	"bytes"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/jjyr/pot/pkg/core"
	"modernc.org/sqlite"
)

// These functions belong to the immutable on-disk migration, not the protocol
// decoder. Object key order is preserved: mailbox receipts compare Go-marshal
// bytes, and Agent Link route hashes bind those same bytes.
type nodeJSON struct {
	scalar   json.RawMessage
	object   []nodeField
	array    []*nodeJSON
	kind     byte
	original json.RawMessage
	changed  bool
}
type nodeField struct {
	key   string
	value *nodeJSON
}

func parseNodeJSON(raw []byte) (*nodeJSON, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var read func() (*nodeJSON, error)
	read = func() (*nodeJSON, error) {
		start := int(d.InputOffset())
		for start < len(raw) && (raw[start] == ' ' || raw[start] == '\n' || raw[start] == '\r' || raw[start] == '\t' || raw[start] == ':' || raw[start] == ',') {
			start++
		}
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		n := &nodeJSON{}
		if delim, ok := token.(json.Delim); ok {
			n.kind = byte(delim)
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return nil, err
					}
					name := key.(string)
					if seen[name] {
						return nil, fmt.Errorf("duplicate migration JSON key")
					}
					seen[name] = true
					child, err := read()
					if err != nil {
						return nil, err
					}
					n.object = append(n.object, nodeField{name, child})
				}
			case '[':
				for d.More() {
					child, err := read()
					if err != nil {
						return nil, err
					}
					n.array = append(n.array, child)
				}
			default:
				return nil, fmt.Errorf("invalid migration JSON delimiter")
			}
			_, err = d.Token()
			n.original = raw[start:int(d.InputOffset())]
			return n, err
		}
		n.scalar = raw[start:int(d.InputOffset())]
		n.original = n.scalar
		return n, nil
	}
	n, err := read()
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing migration JSON")
	}
	return n, nil
}
func (n *nodeJSON) get(key string) *nodeJSON {
	if n == nil {
		return nil
	}
	for _, f := range n.object {
		if f.key == key {
			return f.value
		}
	}
	return nil
}
func (n *nodeJSON) rename(old, new string) {
	for i := range n.object {
		if n.object[i].key == old {
			n.object[i].key = new
			n.changed = true
		}
	}
}
func (n *nodeJSON) text() string {
	if n == nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(n.scalar, &value)
	return value
}
func (n *nodeJSON) ref() {
	if n != nil {
		n.rename("control_id", "node_id")
	}
}
func (n *nodeJSON) host() {
	if n != nil {
		n.rename("id", "node_id")
	}
}
func (n *nodeJSON) identity() {
	if n != nil {
		n.get("address").ref()
	}
}
func (n *nodeJSON) each(fn func(*nodeJSON)) {
	if n != nil {
		for _, v := range n.array {
			fn(v)
		}
	}
}
func (n *nodeJSON) migrate(mode string) {
	if n == nil {
		return
	}
	switch mode {
	case "envelope":
		n.get("from").ref()
		n.get("to").ref()
	case "identity":
		n.identity()
	case "connection":
		n.get("local").identity()
		n.get("peer").identity()
		n.get("peer_host").host()
		if v := n.get("schema_version"); v != nil {
			if string(v.scalar) == "2" {
				v.scalar = json.RawMessage("3")
				v.changed = true
			}
		}
	case "directory":
		n.rename("control_id", "node_id")
		n.get("assistants").each(func(v *nodeJSON) { v.get("address").ref() })
		n.get("tasks").each(func(v *nodeJSON) { v.get("address").ref() })
	case "task":
		if info := n.get("info"); info != nil {
			info.get("address").ref()
		}
		if v := n.get("schema_version"); v != nil {
			if string(v.scalar) == "9" {
				v.scalar = json.RawMessage("10")
				v.changed = true
			}
		}
	case "checkpoint":
		// Historical model/user messages, plugin state and provider-native replay
		// remain evidence. Only pending send_message calls can still be executed.
		pending := map[string]bool{}
		n.get("pending").each(func(v *nodeJSON) {
			if v.get("name").text() == "send_message" {
				if args := v.get("arguments"); args != nil {
					args.get("to").ref()
				}
				pending[v.get("id").text()] = true
			}
		})
		n.get("receipts").each(func(v *nodeJSON) {
			if pending[v.get("id").text()] && v.get("name").text() == "send_message" {
				if args := v.get("arguments"); args != nil {
					args.get("to").ref()
				}
			}
		})
	case "run":
		n.get("context").migrate("checkpoint")
	case "operation":
		if n.get("tool").text() == "send_message" {
			if args := n.get("args"); args != nil {
				args.get("to").ref()
			}
		}
	}
}
func (n *nodeJSON) modified() bool {
	if n.changed {
		return true
	}
	for _, f := range n.object {
		if f.value.modified() {
			return true
		}
	}
	for _, v := range n.array {
		if v.modified() {
			return true
		}
	}
	return false
}
func (n *nodeJSON) append(out *bytes.Buffer) {
	if !n.modified() && n.original != nil {
		out.Write(n.original)
		return
	}
	switch n.kind {
	case '{':
		out.WriteByte('{')
		for i, f := range n.object {
			if i > 0 {
				out.WriteByte(',')
			}
			key, _ := json.Marshal(f.key)
			out.Write(key)
			out.WriteByte(':')
			f.value.append(out)
		}
		out.WriteByte('}')
	case '[':
		out.WriteByte('[')
		for i, v := range n.array {
			if i > 0 {
				out.WriteByte(',')
			}
			v.append(out)
		}
		out.WriteByte(']')
	default:
		out.Write(n.scalar)
	}
}
func (n *nodeJSON) bytes() []byte { var out bytes.Buffer; n.append(&out); return out.Bytes() }
func migrateNodeJSON(raw []byte, mode string) ([]byte, error) {
	n, err := parseNodeJSON(raw)
	if err != nil {
		return nil, err
	}
	before := n.bytes()
	n.migrate(mode)
	after := n.bytes()
	if bytes.Equal(before, after) {
		return raw, nil
	}
	return after, nil
}
func nodeSQLString(v driver.Value) ([]byte, error) {
	switch value := v.(type) {
	case string:
		return []byte(value), nil
	case []byte:
		return value, nil
	default:
		return nil, fmt.Errorf("expected migration JSON string")
	}
}
func init() {
	functions := map[string]struct {
		arity int
		fn    func([]driver.Value) (driver.Value, error)
	}{
		"pot_node_json": {2, func(args []driver.Value) (driver.Value, error) {
			if args[0] == nil {
				return nil, nil
			}
			raw, e := nodeSQLString(args[0])
			if e != nil {
				return nil, e
			}
			mode, e := nodeSQLString(args[1])
			if e != nil {
				return nil, e
			}
			out, e := migrateNodeJSON(raw, string(mode))
			return string(out), e
		}},
		"pot_node_hash": {1, func(args []driver.Value) (driver.Value, error) {
			raw, e := nodeSQLString(args[0])
			if e != nil {
				return nil, e
			}
			return core.HashJSON(json.RawMessage(raw))
		}},
		"pot_node_route": {1, func(args []driver.Value) (driver.Value, error) {
			raw, e := nodeSQLString(args[0])
			if e != nil {
				return nil, e
			}
			n, e := parseNodeJSON(raw)
			if e != nil {
				return nil, e
			}
			a, b := n.get("local"), n.get("peer")
			if a == nil || b == nil {
				return nil, nil
			}
			a, b = a.get("address"), b.get("address")
			if a == nil || b == nil {
				return nil, nil
			}
			a.ref()
			b.ref()
			return nodeRoute(a, b), nil
		}},
		"pot_node_correspondence": {2, func(args []driver.Value) (driver.Value, error) {
			raw, e := nodeSQLString(args[0])
			if e != nil {
				return nil, e
			}
			n, e := parseNodeJSON(raw)
			if e != nil {
				return nil, e
			}
			a, b := n.get("to"), n.get("from")
			if a == nil || b == nil {
				return nil, nil
			}
			if args[1] == int64(1) {
				a.ref()
				b.ref()
			}
			return core.HashJSON(json.RawMessage(append(append(append([]byte("["), a.bytes()...), ','), append(b.bytes(), ']')...)))
		}},
	}
	for name, f := range functions {
		fn := f.fn
		sqlite.MustRegisterDeterministicScalarFunction(name, int32(f.arity), func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) { return fn(args) })
	}
}

func nodeRoute(a, b *nodeJSON) string {
	raw := append(append(append([]byte("["), a.bytes()...), ','), append(b.bytes(), ']')...)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
