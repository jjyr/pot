package control

import "time"

const mcpReceiverLease = 45 * time.Second

// ReceiverOnline is communication liveness, never authority or permission to
// replay accepted work. It resets on service restart; saved directories/history
// remain in SQLite and do not by themselves assert an online receiver.
func (q *MCPAccessQueue) ReceiverOnline(connection string) bool {
	q.liveMu.Lock()
	defer q.liveMu.Unlock()
	at := q.live[connection]
	return !at.IsZero() && time.Since(at) < mcpReceiverLease
}
func (q *MCPAccessQueue) receiverSeen(connection string) {
	q.liveMu.Lock()
	defer q.liveMu.Unlock()
	if q.live == nil {
		q.live = map[string]time.Time{}
	}
	q.live[connection] = time.Now()
}
