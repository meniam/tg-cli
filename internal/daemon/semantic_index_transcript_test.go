package daemon

import (
	"context"
	"testing"
	"time"

	"tg-cli/internal/store"
	"tg-cli/internal/vectors"
)

// seedVoiceConversation stores a conversation of chat 1 of three text messages
// (360 characters, enough for the pause rule) followed by a voice message
// without text (id 4), and a second plain conversation three hours before now.
// Both are closed by silence.
func (f *semanticFixture) seedVoiceConversation() {
	f.t.Helper()
	f.seed(1, 11, semanticT0.Add(-3*time.Hour), 3)
	f.seed(1, 1, semanticT0.Add(-5*time.Hour), 3)
	voice := "voice"
	if err := f.db.UpsertMessages(context.Background(), []store.Message{
		{ChatID: 1, ID: 4, Date: semanticT0.Add(-5*time.Hour + 3*time.Minute).Unix(), MediaType: &voice, Raw: []byte(`{}`)},
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *semanticFixture) firstFragment() vectors.FragmentMeta {
	f.t.Helper()
	for _, m := range f.list() {
		if m.ChatID == 1 && m.FirstID == 1 {
			return m
		}
	}
	f.t.Fatalf("no fragment starts at message 1: %+v", f.list())
	return vectors.FragmentMeta{}
}

// A voice message without a transcription adds nothing: the three texts are 360
// characters. A finished transcription joins the fragment: Chars counts it in
// runes, the hash follows the joined text, and the indexer recomputes only that
// fragment, again when the transcription changes.
func TestTranscriptEntersTheFragmentAndItsHash(t *testing.T) {
	f := newSemanticFixture(t)
	ctx := context.Background()
	f.seedVoiceConversation()
	f.run()
	before := f.firstFragment()
	if before.Chars != 360 || before.LastID != 3 {
		t.Fatalf("without a transcription: chars %d last %d, want 360 and 3", before.Chars, before.LastID)
	}

	f.clock.AdvanceTo(semanticT0.Add(time.Hour))
	first := messageText(4, "voice")
	if err := f.db.PutTranscription(ctx, store.Transcription{ChatID: 1, MessageID: 4, TranscriptionID: 1, Text: first}); err != nil {
		t.Fatal(err)
	}
	st := f.run()
	after := f.firstFragment()
	if after.Chars != 480 || after.LastID != 4 {
		t.Fatalf("with a transcription: chars %d last %d, want 480 and 4", after.Chars, after.LastID)
	}
	want := vectors.TextHash(vectors.FragmentText([]string{messageText(1, ""), messageText(2, ""), messageText(3, ""), first}))
	if after.TextHash != want || after.TextHash == before.TextHash {
		t.Errorf("hash %q, want %q (and not the old %q)", after.TextHash, want, before.TextHash)
	}
	if st.Embedded != 1 {
		t.Errorf("embedded %d fragments after the transcription, want only the changed one", st.Embedded)
	}

	// Another text of the same length: Chars stays, the hash and the vector are recomputed.
	f.clock.AdvanceTo(semanticT0.Add(2 * time.Hour))
	second := messageText(4, "again")
	if err := f.db.PutTranscription(ctx, store.Transcription{ChatID: 1, MessageID: 4, TranscriptionID: 2, Text: second}); err != nil {
		t.Fatal(err)
	}
	st = f.run()
	changed := f.firstFragment()
	if changed.TextHash == after.TextHash || changed.Chars != 480 || st.Embedded != 1 {
		t.Errorf("changed transcription: hash same=%v chars %d embedded %d, want a new hash, 480 and 1", changed.TextHash == after.TextHash, changed.Chars, st.Embedded)
	}
	if st = f.run(); st.Embedded != 0 {
		t.Errorf("a run without changes embedded %d fragments", st.Embedded)
	}
}

// The transcription of a message in a chat closed by access.json never reaches
// the index.
func TestTranscriptOfAClosedChatIsNotIndexed(t *testing.T) {
	f := newSemanticFixture(t)
	f.seedVoiceConversation()
	if err := f.db.PutTranscription(context.Background(), store.Transcription{ChatID: 1, MessageID: 4, TranscriptionID: 1, Text: messageText(4, "voice")}); err != nil {
		t.Fatal(err)
	}
	f.writeAccess(`{"block":[1]}`)
	f.run()
	for _, m := range f.list() {
		if m.ChatID == 1 {
			t.Errorf("a fragment of a closed chat is indexed: %+v", m)
		}
	}
}
