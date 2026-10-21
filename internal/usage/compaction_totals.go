package usage

import "context"

// Use request start times, like the calendar, and join the operation only to
// classify its trigger. Multiple attempts count once each, even after failure.
func compactionTotals(ctx context.Context, db queryDB, scope string, start, stop int64) ([]CompactionTotal, error) {
	rows, err := db.QueryContext(ctx, `SELECT CASE WHEN c.reason='idle' THEN 'precompact' ELSE 'compact' END,
COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0),COUNT(*),
MIN(r.input IS NOT NULL),MIN(r.output IS NOT NULL),MIN(r.input IS NOT NULL AND r.cache_reported=1)
FROM requests r JOIN compactions c ON c.id=r.compaction_id AND c.scope=r.scope AND c.turn_id=r.turn_id
WHERE (?='' OR r.scope=?) AND r.at>=? AND r.at<? AND r.state<>'rejected'
GROUP BY CASE WHEN c.reason='idle' THEN 'precompact' ELSE 'compact' END ORDER BY 1`, scope, scope, start, stop)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := []CompactionTotal{}
	for rows.Next() {
		var value CompactionTotal
		if err := rows.Scan(&value.Kind, &value.Input, &value.Output, &value.Cache, &value.Requests, &value.InputKnown, &value.OutputKnown, &value.CacheKnown); err != nil {
			return nil, err
		}
		totals = append(totals, value)
	}
	return totals, rows.Err()
}
