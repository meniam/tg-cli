package store

import (
	"context"
	"reflect"
	"testing"
)

func TestAudit08_TranscriptionCompletedResultIsNotOverwrittenByUpdate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	tx := Transcription{ChatID: 42, MessageID: 7, TranscriptionID: 9, Pending: true}
	if err := db.PutTranscription(ctx, tx); err != nil {
		t.Fatal(err)
	}
	setClock(db, 200)
	done := tx
	done.Text, done.Pending = "first", false
	if updated, err := db.UpdateTranscription(ctx, done); err != nil || !updated {
		t.Fatalf("completing update: %v, %v", updated, err)
	}
	got, err := db.GetTranscription(ctx, 42, 7)
	if err != nil || got.Text != "first" || got.Pending || got.UpdatedAt != 200 {
		t.Fatalf("after completion: %+v, %v", got, err)
	}

	// A finished transcription accepts no further updates, even for the same request.
	setClock(db, 300)
	late := done
	late.Text = "late"
	if updated, err := db.UpdateTranscription(ctx, late); err != nil || updated {
		t.Fatalf("late update: %v, %v", updated, err)
	}
	got, err = db.GetTranscription(ctx, 42, 7)
	if err != nil || got.Text != "first" || got.UpdatedAt != 200 {
		t.Fatalf("finished result was overwritten: %+v, %v", got, err)
	}
}

func TestAudit08_TranscriptionPutOverwritesAndStampsTime(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	if err := db.PutTranscription(ctx, Transcription{ChatID: 1, MessageID: 2, TranscriptionID: 5, Text: "old"}); err != nil {
		t.Fatal(err)
	}
	setClock(db, 400)
	if err := db.PutTranscription(ctx, Transcription{ChatID: 1, MessageID: 2, TranscriptionID: 6, Text: "new", Pending: true}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTranscription(ctx, 1, 2)
	want := Transcription{ChatID: 1, MessageID: 2, TranscriptionID: 6, Text: "new", Pending: true, UpdatedAt: 400, Source: TranscriptionSourceTelegram}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM transcriptions`); n != 1 {
		t.Fatalf("rows=%d, want 1 after overwrite", n)
	}
}
