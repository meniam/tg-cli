package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	transcriptChat  = int64(1)
	transcriptClose = int64(2)
)

func strPtr(s string) *string { return &s }

// transcriptFixture has a voice message (10) and a round video with a caption
// (11) in an open chat, a plain text message (12), and a voice message (20) in
// a chat the tests close. No transcription exists yet.
func transcriptFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{
		{ID: transcriptChat, Type: "group", Title: "One"},
		{ID: transcriptClose, Type: "group", Title: "Two"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{
		{ChatID: transcriptChat, ID: 10, Date: 10, MediaType: strPtr("voice"), Raw: raw(`{}`)},
		{ChatID: transcriptChat, ID: 11, Date: 11, MediaType: strPtr("video_note"), Text: "Квитанция", Raw: raw(`{}`)},
		{ChatID: transcriptChat, ID: 12, Date: 12, Text: "Обычный текст про склад", Raw: raw(`{}`)},
		{ChatID: transcriptClose, ID: 20, Date: 20, MediaType: strPtr("voice"), Raw: raw(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

func putTranscript(t *testing.T, db *DB, chat int64, id int, tid int64, text string, pending bool) {
	t.Helper()
	if err := db.PutTranscription(context.Background(), Transcription{ChatID: chat, MessageID: id, TranscriptionID: tid, Text: text, Pending: pending}); err != nil {
		t.Fatal(err)
	}
}

func wordLegIDs(t *testing.T, db *DB, query string, filter ChatFilter) (exact, stemmed []int64) {
	t.Helper()
	e, s, err := db.WordLegs(context.Background(), WordLegOptions{Query: query, Limit: 20, ChatFilter: filter})
	if err != nil {
		t.Fatal(err)
	}
	return wordMatchIDs(e), wordMatchIDs(s)
}

func TestTranscriptIsFoundByTheExactAndTheStemmedLeg(t *testing.T) {
	db := transcriptFixture(t)
	if exact, stemmed := wordLegIDs(t, db, "доставка", ChatFilter{}); len(exact)+len(stemmed) != 0 {
		t.Fatalf("a voice message without transcription is found: %v %v", exact, stemmed)
	}
	putTranscript(t, db, transcriptChat, 10, 1, "Доставка будет завтра утром", false)

	exact, stemmed := wordLegIDs(t, db, "доставка", ChatFilter{})
	if !reflect.DeepEqual(exact, []int64{10}) || !reflect.DeepEqual(stemmed, []int64{10}) {
		t.Errorf("доставка: exact %v stemmed %v, want [10] both", exact, stemmed)
	}
	// Another form of the word is found by stems only.
	exact, stemmed = wordLegIDs(t, db, "доставки", ChatFilter{})
	if len(exact) != 0 || !reflect.DeepEqual(stemmed, []int64{10}) {
		t.Errorf("доставки: exact %v stemmed %v, want none and [10]", exact, stemmed)
	}
}

func TestTranscriptHitShowsTheTranscriptionAndKeepsItsLink(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: -1001000000007, Type: "channel", Title: "News", Username: strPtr("newsfeed")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: -1001000000007, ID: 5, Date: 5, MediaType: strPtr("voice"), Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	putTranscript(t, db, -1001000000007, 5, 1, "Завтра привезём оплату", false)
	hits, err := db.Search(ctx, SearchOptions{Query: "оплаты", Scopes: []string{"messages"}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits %v, %v", hits, err)
	}
	if !strings.Contains(hits[0].Snippet, "оплату") {
		t.Errorf("snippet %q does not show the transcription", hits[0].Snippet)
	}
	if hits[0].Link == nil || *hits[0].Link != "https://t.me/newsfeed/5" {
		t.Errorf("link = %v, want https://t.me/newsfeed/5", hits[0].Link)
	}
	stored, err := db.GetMessage(ctx, -1001000000007, 5)
	if err != nil || stored.Text != "" || stored.MediaType == nil || *stored.MediaType != "voice" {
		t.Errorf("the message itself must stay as read: %+v, %v", stored, err)
	}
}

// Reading the same message again recomputes search_text from the message; it
// must keep the transcription.
func TestTranscriptSurvivesReadingTheMessageAgain(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	putTranscript(t, db, transcriptChat, 10, 1, "Доставка будет завтра", false)
	edited := int64(500)
	if err := db.UpsertMessages(ctx, []Message{{ChatID: transcriptChat, ID: 10, Date: 10, MediaType: strPtr("voice"), EditedAt: &edited, Raw: raw(`{"again":true}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: transcriptChat, ID: 10, Date: 10, Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	exact, stemmed := wordLegIDs(t, db, "доставки", ChatFilter{})
	if len(exact) != 0 || !reflect.DeepEqual(stemmed, []int64{10}) {
		t.Errorf("after a second read: exact %v stemmed %v, want none and [10]", exact, stemmed)
	}
	if exact, _ := wordLegIDs(t, db, "завтра", ChatFilter{}); !reflect.DeepEqual(exact, []int64{10}) {
		t.Errorf("after a second read the exact leg gave %v, want [10]", exact)
	}
}

// A caption edit changes search_text too, and the transcription stays in it.
func TestTranscriptStaysAfterTheCaptionIsEdited(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	putTranscript(t, db, transcriptChat, 11, 1, "Оплата прошла", false)
	if err := db.UpsertMessages(ctx, []Message{{ChatID: transcriptChat, ID: 11, Date: 11, MediaType: strPtr("video_note"), Text: "Чек", Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"чек", "оплата"} {
		if exact, _ := wordLegIDs(t, db, word, ChatFilter{}); !reflect.DeepEqual(exact, []int64{11}) {
			t.Errorf("%q after the caption edit: %v, want [11]", word, exact)
		}
	}
	if exact, _ := wordLegIDs(t, db, "квитанция", ChatFilter{}); len(exact) != 0 {
		t.Errorf("the old caption is still found: %v", exact)
	}
}

func TestTranscriptChangeReplacesTheSearchedText(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	putTranscript(t, db, transcriptChat, 10, 7, "", true)
	updated, err := db.UpdateTranscription(ctx, Transcription{ChatID: transcriptChat, MessageID: 10, TranscriptionID: 7, Text: "Привезём цемент", Pending: false})
	if err != nil || !updated {
		t.Fatalf("update: %v, %v", updated, err)
	}
	if exact, _ := wordLegIDs(t, db, "цемент", ChatFilter{}); !reflect.DeepEqual(exact, []int64{10}) {
		t.Fatalf("цемент after the update: %v, want [10]", exact)
	}
	// A repeated request stores a new text for the same message.
	putTranscript(t, db, transcriptChat, 10, 8, "Привезём кирпич", false)
	if exact, stemmed := wordLegIDs(t, db, "цемент", ChatFilter{}); len(exact)+len(stemmed) != 0 {
		t.Errorf("the old word is still found: %v %v", exact, stemmed)
	}
	if exact, _ := wordLegIDs(t, db, "кирпич", ChatFilter{}); !reflect.DeepEqual(exact, []int64{10}) {
		t.Errorf("кирпич: %v, want [10]", exact)
	}
	// Back to pending: the text no longer counts.
	putTranscript(t, db, transcriptChat, 10, 9, "кирпич", true)
	if exact, stemmed := wordLegIDs(t, db, "кирпич", ChatFilter{}); len(exact)+len(stemmed) != 0 {
		t.Errorf("a pending transcription is found: %v %v", exact, stemmed)
	}
}

func TestTranscriptPendingAndEmptyAreNotText(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	putTranscript(t, db, transcriptChat, 10, 1, "Склад закрыт", true)
	putTranscript(t, db, transcriptChat, 11, 2, "", false)
	if exact, stemmed := wordLegIDs(t, db, "склад", ChatFilter{}); !reflect.DeepEqual(exact, []int64{12}) || !reflect.DeepEqual(stemmed, []int64{12}) {
		t.Errorf("pending transcription was searched: exact %v stemmed %v, want [12]", exact, stemmed)
	}
	if got, err := db.MessagesForFragments(ctx, ChatFilter{}); err != nil || fragmentIDs(got, transcriptChat)[10] {
		t.Errorf("a pending voice message is a fragment source: %v, %v", got, err)
	}
	// The caption alone is the text of the round video while its transcription is empty.
	for _, m := range mustFragments(t, db, ChatFilter{}) {
		if m.ID == 11 && m.Text != "Квитанция" {
			t.Errorf("text of 11 = %q, want the caption alone", m.Text)
		}
	}
	if got, _ := db.FragmentText(ctx, transcriptChat, 0, 10, 10, 1500); got != "" {
		t.Errorf("fragment text of a pending voice message = %q, want empty", got)
	}
}

func mustFragments(t *testing.T, db *DB, filter ChatFilter) []FragmentMessage {
	t.Helper()
	got, err := db.MessagesForFragments(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func fragmentIDs(messages []FragmentMessage, chat int64) map[int64]bool {
	ids := map[int64]bool{}
	for _, m := range messages {
		if m.ChatID == chat {
			ids[m.ID] = true
		}
	}
	return ids
}

func TestTranscriptJoinsTheCaptionInFragmentsAndSearch(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	putTranscript(t, db, transcriptChat, 10, 1, "Привет, мир", false)
	putTranscript(t, db, transcriptChat, 11, 2, "Оплатил вчера", false)

	var voice, note *FragmentMessage
	got := mustFragments(t, db, ChatFilter{})
	for i := range got {
		switch got[i].ID {
		case 10:
			voice = &got[i]
		case 11:
			note = &got[i]
		}
	}
	if voice == nil || note == nil {
		t.Fatalf("fragment source lacks the transcribed messages: %+v", got)
	}
	if voice.Text != "Привет, мир" || voice.Chars != 11 {
		t.Errorf("voice: text %q chars %d, want the transcription and 11 characters", voice.Text, voice.Chars)
	}
	wantNote := "Квитанция\nОплатил вчера"
	if note.Text != wantNote || note.Chars != utf8.RuneCountInString(wantNote) || note.Chars == len(wantNote) {
		t.Errorf("round video: text %q chars %d, want %q and its rune count %d", note.Text, note.Chars, wantNote, utf8.RuneCountInString(wantNote))
	}
	text, err := db.FragmentText(ctx, transcriptChat, 0, 10, 12, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Привет, мир\n" + wantNote + "\nОбычный текст про склад"; text != want {
		t.Errorf("fragment text = %q, want %q", text, want)
	}
	// Caption and transcription are both searchable.
	for _, word := range []string{"квитанция", "оплатили"} {
		if _, stemmed := wordLegIDs(t, db, word, ChatFilter{}); !reflect.DeepEqual(stemmed, []int64{11}) {
			t.Errorf("%q: stemmed leg %v, want [11]", word, stemmed)
		}
	}
}

func TestTranscriptFragmentSourceSelectsOnlyTranscribedVoice(t *testing.T) {
	db := transcriptFixture(t)
	if ids := fragmentIDs(mustFragments(t, db, ChatFilter{}), transcriptChat); ids[10] {
		t.Fatalf("a voice message without transcription is in the fragment source: %v", ids)
	}
	putTranscript(t, db, transcriptChat, 10, 1, "Готово", false)
	if ids := fragmentIDs(mustFragments(t, db, ChatFilter{}), transcriptChat); !ids[10] {
		t.Fatalf("a transcribed voice message is missing: %v", ids)
	}
}

func TestTranscriptHonorsClosedChatsAndDeletedMessages(t *testing.T) {
	db := transcriptFixture(t)
	ctx := context.Background()
	putTranscript(t, db, transcriptClose, 20, 1, "Секретная доставка", false)
	putTranscript(t, db, transcriptChat, 10, 2, "Открытая доставка", false)

	block := ChatFilter{Block: []int64{transcriptClose}}
	if _, stemmed := wordLegIDs(t, db, "доставка", block); !reflect.DeepEqual(stemmed, []int64{10}) {
		t.Errorf("closed chat in the word legs: %v, want [10]", stemmed)
	}
	if ids := fragmentIDs(mustFragments(t, db, block), transcriptClose); len(ids) != 0 {
		t.Errorf("closed chat in the fragment source: %v", ids)
	}
	if ids := fragmentIDs(mustFragments(t, db, ChatFilter{Hide: true}), transcriptChat); len(ids) != 0 {
		t.Errorf("hidden chats in the fragment source: %v", ids)
	}

	if _, err := db.MarkMessagesDeleted(ctx, transcriptChat, []int{10}); err != nil {
		t.Fatal(err)
	}
	if _, stemmed := wordLegIDs(t, db, "доставка", block); len(stemmed) != 0 {
		t.Errorf("deleted voice message in the word legs: %v", stemmed)
	}
	if ids := fragmentIDs(mustFragments(t, db, block), transcriptChat); ids[10] {
		t.Errorf("deleted voice message in the fragment source: %v", ids)
	}
	if got, _ := db.FragmentText(ctx, transcriptChat, 0, 10, 10, 1500); got != "" {
		t.Errorf("fragment text of a deleted voice message = %q, want empty", got)
	}
	// Reading a deleted message again brings it back, with its transcription.
	if err := db.UpsertMessages(ctx, []Message{{ChatID: transcriptChat, ID: 10, Date: 10, MediaType: strPtr("voice"), Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, stemmed := wordLegIDs(t, db, "доставка", block); !reflect.DeepEqual(stemmed, []int64{10}) {
		t.Errorf("restored voice message: %v, want [10]", stemmed)
	}
}

// A transcription saved before the message row exists reaches search_text when
// the message is first stored.
func TestTranscriptSavedBeforeTheMessageIsStored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "One"}}); err != nil {
		t.Fatal(err)
	}
	putTranscript(t, db, 1, 30, 1, "Груз прибыл", false)
	if exact, _ := wordLegIDs(t, db, "груз", ChatFilter{}); len(exact) != 0 {
		t.Fatalf("a transcription without a message is found: %v", exact)
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 30, Date: 30, MediaType: strPtr("voice"), Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if exact, _ := wordLegIDs(t, db, "груз", ChatFilter{}); !reflect.DeepEqual(exact, []int64{30}) {
		t.Errorf("груз: %v, want [30]", exact)
	}
}

// Other media and plain text never take a transcription row into their text.
func TestTranscriptIsIgnoredForOtherMessages(t *testing.T) {
	db := transcriptFixture(t)
	putTranscript(t, db, transcriptChat, 12, 1, "посторонние слова", false)
	if exact, stemmed := wordLegIDs(t, db, "посторонние", ChatFilter{}); len(exact)+len(stemmed) != 0 {
		t.Errorf("a text message took a transcription: %v %v", exact, stemmed)
	}
	for _, m := range mustFragments(t, db, ChatFilter{}) {
		if m.ID == 12 && m.Text != "Обычный текст про склад" {
			t.Errorf("text of a plain message = %q", m.Text)
		}
	}
	if got, _ := db.FragmentText(context.Background(), transcriptChat, 0, 12, 12, 1500); got != "Обычный текст про склад" {
		t.Errorf("fragment text of a plain message = %q", got)
	}
}

// Transcriptions saved before the word index knew about them become searchable
// when the database opens, without a migration.
func TestTranscriptBackfillOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "One"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 40, Date: 40, MediaType: strPtr("voice"), Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	// A row written the way a program without this feature wrote it.
	if _, err := db.write.ExecContext(ctx, `INSERT INTO transcriptions(chat_id,message_id,transcription_id,text,pending,updated_at) VALUES(1,40,5,'Старый голосовой про доставку',0,1)`); err != nil {
		t.Fatal(err)
	}
	if exact, _ := wordLegIDs(t, db, "доставку", ChatFilter{}); len(exact) != 0 {
		t.Fatalf("precondition: found before the backfill: %v", exact)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if exact, stemmed := wordLegIDs(t, db, "доставку", ChatFilter{}); !reflect.DeepEqual(exact, []int64{40}) || !reflect.DeepEqual(stemmed, []int64{40}) {
		t.Errorf("after reopening: exact %v stemmed %v, want [40] both", exact, stemmed)
	}
	var check string
	if err := db.write.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Errorf("integrity_check = %q, %v", check, err)
	}
	// The next open has nothing to do.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if exact, _ := wordLegIDs(t, db, "доставку", ChatFilter{}); !reflect.DeepEqual(exact, []int64{40}) {
		t.Errorf("after a second reopening: %v, want [40]", exact)
	}
}
