package usage

import "context"

// ActiveToolNames is a lightweight projection of unfinished calls in one run.
func (l *Ledger) ActiveToolNames(ctx context.Context, scope, turn string) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, "SELECT DISTINCT name FROM tools WHERE scope=? AND turn_id=? AND end_at IS NULL ORDER BY name", scope, turn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
