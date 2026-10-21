package assistant

// ExecutionState is a payload-free live observation. It neither opens a
// conversation nor starts pending work when the topology reads it.
func (c *Conversation) ExecutionState() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked {
		return "failed", true
	}
	if c.turn != nil {
		if c.turn.Paused {
			return "paused", false
		}
		if c.turn.State == "waiting" {
			return "waiting", false
		}
		if c.turn.State == "running" {
			return "running", false
		}
		return "queued", false
	}
	if c.lastError != nil {
		return "failed", false
	}
	return "idle", false
}
