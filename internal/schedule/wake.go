package schedule

import "context"

// NextWake returns the earliest durable enabled occurrence. A past occurrence
// remains past while readiness blocks it; the caller chooses an allowance timer.
func (s *Service) NextWake(ctx context.Context) (*int64, error) {
	var at *int64
	err := s.db.QueryRowContext(ctx, `SELECT MIN(CAST(json_extract(body,'$.info.next_at_ms') AS INTEGER)) FROM schedules WHERE json_extract(body,'$.info.spec.enabled')=1 AND json_extract(body,'$.info.deleted')=0`).Scan(&at)
	return at, err
}
