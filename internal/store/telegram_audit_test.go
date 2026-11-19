package store

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkAudit08UpsertMessages5000 replaces the former wall-clock threshold in
// TestBulkMessages: speed is measured here, correctness stays in the test.
func BenchmarkAudit08UpsertMessages5000(b *testing.B) {
	ctx := context.Background()
	batch := make([]Message, 5000)
	for i := range batch {
		batch[i] = Message{ChatID: int64(i%5 + 1), ID: i + 1, Date: int64(i + 1), Text: fmt.Sprintf("Bulk Ёлка %d", i), Raw: raw(`{"kind":"synthetic"}`)}
	}
	for b.Loop() {
		b.StopTimer()
		db, err := Open(b.TempDir() + "/bench.db")
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err = db.UpsertMessages(ctx, batch); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		db.Close()
		b.StartTimer()
	}
}
