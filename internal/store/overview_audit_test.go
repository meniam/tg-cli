package store

import (
	"context"
	"testing"
)

// TestAudit07_ChatsOverviewUpperLimit keeps a caller from reading the whole
// table: a limit above the cap returns the cap, not everything.
func TestAudit07_ChatsOverviewUpperLimit(t *testing.T) {
	db := openTestDB(t)
	schemaExec(t, db.write, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<(1 + 600))
		INSERT INTO chats(id,type,title,first_seen_at,last_seen_at) SELECT i,'user','chat '||i,1,1 FROM n`)
	// The cap is part of the contract, so the test names it itself instead of
	// reading the constant of the code under test.
	const cap = 500
	for _, tc := range []struct {
		limit, want int
	}{{3, 3}, {cap, cap}, {cap + 1, cap}, {10000, cap}, {0, 1}, {-5, 1}} {
		rows, err := db.ChatsOverview(context.Background(), ChatFilter{}, tc.limit, "")
		if err != nil || len(rows) != tc.want {
			t.Errorf("limit %d returned %d rows, err=%v, want %d", tc.limit, len(rows), err, tc.want)
		}
	}
}
