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

func TestFinishedTranscriptionsSkipsPendingAndOtherChats(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, tx := range []Transcription{
		{ChatID: 42, MessageID: 1, TranscriptionID: 1, Text: "done by Telegram"},
		{ChatID: 42, MessageID: 2, TranscriptionID: 2, Text: "still working", Pending: true},
		{ChatID: 42, MessageID: 3, TranscriptionID: 3, Text: "", Source: TranscriptionSourceLocal, Engine: "gigaam-v3"},
		{ChatID: 42, MessageID: 4, TranscriptionID: 4, Text: "not asked for"},
		{ChatID: 43, MessageID: 1, TranscriptionID: 5, Text: "other chat"},
	} {
		if err := db.PutTranscription(ctx, tx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.FinishedTranscriptions(ctx, 42, []int{1, 2, 3, 99})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Text != "done by Telegram" || got[1].Source != TranscriptionSourceTelegram || got[1].Engine != "" {
		t.Errorf("Telegram text = %+v", got)
	}
	if empty, ok := got[3]; !ok || empty.Text != "" || empty.Source != TranscriptionSourceLocal || empty.Engine != "gigaam-v3" {
		t.Errorf("a finished empty text must be reported, not dropped: %+v", got)
	}
	if _, ok := got[2]; ok {
		t.Error("a pending transcription was reported as finished")
	}
	// More ids than one query takes: every chunk is read.
	ids := make([]int, 1000)
	for i := range ids {
		ids[i] = i + 1
	}
	many, err := db.FinishedTranscriptions(ctx, 42, ids)
	if err != nil || len(many) != 3 {
		t.Errorf("1000 ids: %d found, %v", len(many), err)
	}
	if none, err := db.FinishedTranscriptions(ctx, 42, nil); err != nil || len(none) != 0 {
		t.Errorf("no ids: %v, %v", none, err)
	}
}
