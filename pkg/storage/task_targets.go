package storage

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"modernc.org/sqlite"
)

// Frozen wire layout of the unified Task request, used for exact retry receipts.
type targetTaskRequest struct {
	Target        protocol.AgentRef    `json:"target"`
	Configuration *protocol.CreateTask `json:"configuration,omitempty"`
	RequestID     string               `json:"request_id"`
	From          protocol.AgentRef    `json:"from"`
	Text          string               `json:"text"`
	Workspace     string               `json:"workspace,omitempty"`
}

type targetDocument map[string]json.RawMessage

func targetDecode(raw string) (targetDocument, error) {
	var doc targetDocument
	err := json.Unmarshal([]byte(raw), &doc)
	if err == nil && doc == nil {
		err = fmt.Errorf("expected an object")
	}
	return doc, err
}
func targetField[T any](doc targetDocument, key string) T {
	var value T
	_ = json.Unmarshal(doc[key], &value)
	return value
}
func targetPut(doc targetDocument, key string, value any) { doc[key], _ = json.Marshal(value) }
func targetEncode(value any) (string, error) {
	raw, err := json.Marshal(value)
	return string(raw), err
}
func targetNode(node string) protocol.AgentRef {
	return protocol.AgentRef{NodeID: node, Kind: "node", AgentID: node}
}
func targetAssistant(node, id string) protocol.AgentRef {
	return protocol.AgentRef{NodeID: node, Kind: "assistant", AgentID: id}
}
func targetValid(ref protocol.AgentRef) bool {
	return ref.Valid() && (ref.Kind == "assistant" || ref.Kind == "node" && ref.AgentID == ref.NodeID)
}

func targetCreate(old legacyCreateTask, resolved protocol.AgentRef) protocol.CreateTask {
	target := old.Target
	if target == nil && (old.LinkID != "" || old.Executor != "") && targetValid(resolved) {
		target = &resolved
	}
	return protocol.CreateTask{Target: target, Workspace: old.Workspace, Skills: old.Skills, RequestID: old.RequestID, Text: old.Text, Profile: old.Profile, Model: old.Model, Requirements: old.Requirements, SelectionReason: old.SelectionReason}
}
func targetReceipt(raw json.RawMessage) (targetDocument, error) {
	doc, err := targetDecode(string(raw))
	if err != nil {
		return nil, err
	}
	if receiver, ok := doc["execution"]; ok && string(receiver) != "null" {
		doc["receiver"] = receiver
	} else if receiver, ok = doc["assistant"]; ok {
		doc["receiver"] = receiver
	}
	delete(doc, "assistant")
	delete(doc, "execution")
	return doc, nil
}
func targetReceiver(doc targetDocument) protocol.AgentRef {
	identity := targetField[targetDocument](doc, "receiver")
	return targetField[protocol.AgentRef](identity, "address")
}
func targetWire(raw json.RawMessage, resolved protocol.AgentRef) (targetTaskRequest, error) {
	var old executorUpgradeRequest
	if err := json.Unmarshal(raw, &old); err != nil {
		return targetTaskRequest{}, err
	}
	var current targetTaskRequest
	if err := json.Unmarshal(raw, &current); err != nil {
		return current, err
	}
	if targetValid(current.Target) {
		return current, nil
	}
	current.RequestID, current.From, current.Text, current.Workspace = old.RequestID, old.From, old.Text, old.Workspace
	current.Target = resolved
	if old.Configuration != nil {
		config := targetCreate(*old.Configuration, protocol.AgentRef{})
		current.Configuration = &config
	}
	return current, nil
}
func targetResolve(old executorUpgradeRequest, receiver protocol.AgentRef, node string) protocol.AgentRef {
	if receiver.Kind == "task" {
		return targetNode(receiver.NodeID)
	}
	if receiver.Kind == "assistant" {
		return receiver
	}
	if old.Executor != "" {
		return targetNode(node)
	}
	if old.AssistantID != "" {
		return targetAssistant(node, old.AssistantID)
	}
	return protocol.AgentRef{}
}

func migrateTargetRecord(raw, node string) (string, error) {
	doc, err := targetDecode(raw)
	if err != nil {
		return "", err
	}
	if targetField[int](doc, "schema_version") != 12 {
		return raw, nil
	}
	var old legacyCreateTask
	if err = json.Unmarshal(doc["request"], &old); len(doc["request"]) > 0 && err != nil {
		return "", err
	}
	executionNode := old.MachineID
	if executionNode == "" {
		executionNode = targetField[string](targetField[targetDocument](doc, "info"), "machine_id")
	}
	if executionNode == "" {
		executionNode = targetField[string](targetField[targetDocument](doc, "launch"), "runtime_id")
	}
	if executionNode == "" {
		executionNode = node
	}
	if executionNode != "" && targetField[targetDocument](doc, "execution") == nil {
		targetPut(doc, "node_id", executionNode)
	}

	var resolved protocol.AgentRef
	if execution := targetField[targetDocument](doc, "execution"); execution != nil {
		receipt, err := targetReceipt(execution["receipt"])
		if err != nil {
			return "", err
		}
		origin := targetField[protocol.AgentLinkOrigin](targetField[targetDocument](doc, "info"), "agent_link")
		var request executorUpgradeRequest
		if err = json.Unmarshal(execution["request"], &request); err != nil {
			return "", err
		}
		resolved = targetResolve(request, targetReceiver(receipt), origin.NodeID)
		if !targetValid(resolved) && origin.AssistantID != "" {
			resolved = targetAssistant(origin.NodeID, origin.AssistantID)
		}
		if !targetValid(resolved) && old.Target != nil {
			resolved = *old.Target
		}
		wire, err := targetWire(execution["request"], resolved)
		if err != nil {
			return "", err
		}
		if !targetValid(resolved) {
			// Preserve uncertain default-directed submissions without selecting today's default.
			execution["unresolved_request"] = execution["request"]
			targetPut(receipt, "state", "unknown")
			targetPut(receipt, "error", "agent_link_target_unresolved")
			info := targetField[targetDocument](doc, "info")
			if info != nil {
				targetPut(info, "phase", "interrupted")
				targetPut(doc, "info", info)
			}
		}
		targetPut(execution, "request", wire)
		targetPut(execution, "receipt", receipt)
		targetPut(doc, "execution", execution)
	}
	if len(doc["request"]) > 0 {
		targetPut(doc, "request", targetCreate(old, resolved))
	}
	targetPut(doc, "schema_version", 13)
	return targetEncode(doc)
}
func migrateTargetJob(raw string) (string, error) {
	doc, err := targetDecode(raw)
	if err != nil {
		return "", err
	}
	receipt, err := targetReceipt(doc["task"])
	if err != nil {
		return "", err
	}
	var old executorUpgradeRequest
	if err = json.Unmarshal(doc["request"], &old); err != nil {
		return "", err
	}
	receiver := targetReceiver(receipt)
	wire, err := targetWire(doc["request"], targetResolve(old, receiver, receiver.NodeID))
	if err != nil {
		return "", err
	}
	targetPut(doc, "task", receipt)
	targetPut(doc, "request", wire)
	return targetEncode(doc)
}
func migrateTargetDirectory(raw string) (string, error) {
	doc, err := targetDecode(raw)
	if err != nil {
		return "", err
	}
	if _, ok := doc["targets"]; ok {
		return raw, nil
	}
	node := targetField[string](doc, "node_id")
	operations := targetField[targetDocument](doc, "operations")
	entries := []targetDocument{}
	for _, identity := range targetField[[]targetDocument](doc, "assistants") {
		address := targetField[protocol.AgentRef](identity, "address")
		identity["capabilities"] = operations[address.AgentID]
		if len(identity["capabilities"]) == 0 {
			targetPut(identity, "capabilities", map[string]any{})
		}
		entries = append(entries, identity)
	}
	if caps, ok := doc["task_executor"]; ok && string(caps) != "null" {
		identity := targetDocument{"capabilities": caps}
		targetPut(identity, "address", targetNode(node))
		identity["name"], identity["adapter"] = doc["name"], doc["adapter"]
		entries = append(entries, identity)
	}
	if id := targetField[string](doc, "default_assistant_id"); id != "" {
		targetPut(doc, "default_target", targetAssistant(node, id))
	}
	targetPut(doc, "targets", entries)
	delete(doc, "assistants")
	delete(doc, "operations")
	delete(doc, "task_executor")
	delete(doc, "default_assistant_id")
	return targetEncode(doc)
}
func init() {
	register := func(name string, n int, fn func([]string) (string, error)) {
		sqlite.MustRegisterDeterministicScalarFunction(name, int32(n), func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			values := make([]string, len(args))
			for i, arg := range args {
				if arg != nil {
					var ok bool
					values[i], ok = arg.(string)
					if !ok {
						return nil, fmt.Errorf("%s expects JSON text", name)
					}
				}
			}
			return fn(values)
		})
	}
	register("pot_target_record", 2, func(v []string) (string, error) { return migrateTargetRecord(v[0], v[1]) })
	register("pot_target_job", 1, func(v []string) (string, error) { return migrateTargetJob(v[0]) })
	register("pot_target_directory", 1, func(v []string) (string, error) { return migrateTargetDirectory(v[0]) })
	register("pot_target_payload", 1, func(v []string) (string, error) {
		doc, err := targetDecode(v[0])
		if err != nil {
			return "", err
		}
		var wire targetTaskRequest
		if err = json.Unmarshal(doc["request"], &wire); err != nil {
			return "", err
		}
		return targetEncode(wire)
	})
	register("pot_target_received", 3, func(v []string) (string, error) {
		if v[1] == "" {
			return "", fmt.Errorf("cannot resolve historical receiver node")
		}
		wire, err := targetWire(json.RawMessage(v[0]), targetAssistant(v[1], v[2]))
		if err != nil {
			return "", err
		}
		return targetEncode(wire)
	})
	register("pot_target_digest", 1, func(v []string) (string, error) {
		var record struct {
			Request protocol.CreateTask `json:"request"`
			Origin  protocol.TaskOrigin `json:"origin"`
		}
		if err := json.Unmarshal([]byte(v[0]), &record); err != nil {
			return "", err
		}
		return core.HashJSON([]any{record.Request, record.Origin})
	})
	register("pot_target_submission", 1, func(v []string) (string, error) {
		doc, err := targetDecode(v[0])
		if err != nil {
			return "", err
		}
		receipt, err := targetReceipt(doc["remote"])
		if err != nil {
			return "", err
		}
		info := targetField[targetDocument](doc, "info")
		origin := targetField[protocol.AgentLinkOrigin](info, "agent_link")
		var old executorUpgradeRequest
		if err = json.Unmarshal(doc["request"], &old); err != nil {
			return "", err
		}
		resolved := targetResolve(old, targetReceiver(receipt), origin.NodeID)
		if !targetValid(resolved) && origin.AssistantID != "" {
			resolved = targetAssistant(origin.NodeID, origin.AssistantID)
		}
		wire, err := targetWire(doc["request"], resolved)
		if err != nil {
			return "", err
		}
		if !targetValid(resolved) {
			doc["unresolved_request"] = doc["request"]
			targetPut(receipt, "state", "unknown")
			targetPut(receipt, "error", "agent_link_target_unresolved")
			if info != nil {
				targetPut(info, "phase", "interrupted")
				targetPut(doc, "info", info)
			}
		}
		targetPut(doc, "request", wire)
		targetPut(doc, "remote", receipt)
		return targetEncode(doc)
	})
	register("pot_target_create", 2, func(v []string) (string, error) {
		var old legacyCreateTask
		if err := json.Unmarshal([]byte(v[0]), &old); err != nil {
			return "", err
		}
		doc, err := targetDecode(v[1])
		if err != nil {
			return "", err
		}
		var wire targetTaskRequest
		if err = json.Unmarshal(doc["request"], &wire); err != nil {
			return "", err
		}
		return targetEncode(targetCreate(old, wire.Target))
	})
}
