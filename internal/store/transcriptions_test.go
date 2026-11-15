package store

import (
	"context"
	"errors"
	"testing"
)

func TestTranscriptionUpdatesOnlyMatchingRequest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tx := Transcription{ChatID: 42, MessageID: 7, TranscriptionID: 9, Pending: true}
	updated, err := db.UpdateTranscription(ctx, tx)
	if err != nil || updated {
		t.Fatalf("unsolicited update: %v, %v", updated, err)
	}
	if _, err := db.GetTranscription(ctx, 42, 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsolicited result: %v", err)
	}
	if err := db.PutTranscription(ctx, tx); err != nil {
		t.Fatal(err)
	}
	tx.TranscriptionID++
	updated, err = db.UpdateTranscription(ctx, tx)
	if err != nil || updated {
		t.Fatalf("wrong transcription: %v, %v", updated, err)
	}
	tx.TranscriptionID--
	tx.Text, tx.Pending = "hello", false
	updated, err = db.UpdateTranscription(ctx, tx)
	if err != nil || !updated {
		t.Fatalf("matching update: %v, %v", updated, err)
	}
	got, err := db.GetTranscription(ctx, 42, 7)
	if err != nil || got.Text != "hello" || got.Pending {
		t.Fatalf("stored result: %+v, %v", got, err)
	}
}
