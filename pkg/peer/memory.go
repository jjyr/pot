package peer

import (
	"errors"
	"sync"
)

const maxSessionMemory = 96 * 1024 * 1024
const maxHostMemory = 256 * 1024 * 1024

// Windows and transport-owned body buffers share per-session and process-wide
// accounting. Caller-owned requests/results and bounded codec workspaces are
// separate. Refuse reservations before allocating; never wait while holding a
// stream's receive window, which could deadlock unrelated calls.
var errMemoryCapacity = errors.New("peer memory capacity exceeded")

var hostMemory = &memoryBudget{limit: maxHostMemory}

type memoryBudget struct {
	mu          sync.Mutex
	used, limit int
	parent      *memoryBudget
}

func (b *memoryBudget) reserve(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > b.limit-b.used {
		return false
	}
	if b.parent != nil && !b.parent.reserve(n) {
		return false
	}
	b.used += n
	return true
}
func (b *memoryBudget) release(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.parent != nil {
		b.parent.release(n)
	}
}

type streamMemory struct {
	mu       sync.Mutex
	budget   *memoryBudget
	reserved int
	closed   bool
}

func (m *streamMemory) ReserveMemory(n int, _ uint8) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.budget.reserve(n) {
		return errMemoryCapacity
	}
	m.reserved += n
	return nil
}
func (m *streamMemory) ReleaseMemory(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > m.reserved {
		n = m.reserved
	}
	m.budget.release(n)
	m.reserved -= n
}
func (m *streamMemory) Done() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.budget.release(m.reserved)
	m.reserved = 0
	m.closed = true
}
