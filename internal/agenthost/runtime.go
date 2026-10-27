package agenthost

// Runtime selects a native agent protocol. Each runtime has its own root,
// identity and listeners; transport and durable execution remain shared.
type Runtime struct {
	Kind, Name, Binary, Listen string
}

func Codex() Runtime { return Runtime{"codex", "Codex", "codex", hostListen} }
func Antigravity() Runtime {
	return Runtime{"antigravity", "Antigravity", "agy", "127.0.0.1:8795"}
}
func runtimeOrDefault(r []Runtime) Runtime {
	if len(r) > 0 {
		return r[0]
	}
	return Codex()
}
func (h *agentHost) runtime() Runtime {
	if h.adapter.Kind == "" {
		return Codex()
	}
	return h.adapter
}
func (b *backend) runtime() Runtime {
	if b.adapter.Kind == "" {
		return Codex()
	}
	return b.adapter
}

func (c hostClient) runtime() Runtime {
	if c.adapter.Kind == "" {
		return Codex()
	}
	return c.adapter
}
