package store

import (
	"context"
	"reflect"
	"testing"
)

func seedVoiceMessage(t *testing.T, db *DB, chat int64, id int, media string, out bool) {
	t.Helper()
	m := Message{ChatID: chat, ID: id, Date: 1000 + int64(id), Out: &out, Raw: []byte(`{}`)}
	if media != "" {
		m.MediaType = &media
	}
	if err := db.UpsertMessages(context.Background(), []Message{m}); err != nil {
		t.Fatal(err)
	}
}

func candidateKeys(list []VoiceCandidate) [][2]int64 {
	keys := make([][2]int64, 0, len(list))
	for _, c := range list {
		keys = append(keys, [2]int64{c.ChatID, int64(c.MessageID)})
	}
	return keys
}

// TestVoiceSourceChatRule: the rule data of a chat comes from chats and
// voice_chats, and a chat nobody knows is neither personal nor marked.
func TestVoiceSourceChatRule(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "user"}, {ID: 2, Type: "bot"}, {ID: 3, Type: "supergroup"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetVoiceChat(ctx, 3, true, db.now()); err != nil {
		t.Fatal(err)
	}
	for chat, want := range map[int64]VoiceChatRule{
		1: {Private: true}, 2: {}, 3: {Marked: true, Video: true}, 99: {},
	} {
		got, err := db.VoiceChatRule(ctx, chat)
		if err != nil || got != want {
			t.Errorf("chat %d: rule %+v err %v, want %+v", chat, got, err, want)
		}
	}
	// A new mark with another video flag updates the flag and nothing else.
	if err := db.SetVoiceChat(ctx, 3, false, db.now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.VoiceChatRule(ctx, 3); got != (VoiceChatRule{Marked: true}) {
		t.Errorf("after the second mark: %+v", got)
	}
}

// TestVoiceSourceCandidates: a candidate has no finished text and no queue row,
// is not deleted and not the owner's, and the filter and kinds apply.
func TestVoiceSourceCandidates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: -5, Type: "supergroup"}, {ID: 10, Type: "user"}, {ID: 11, Type: "user"}, {ID: 12, Type: "bot"}}); err != nil {
		t.Fatal(err)
	}
	seedVoiceMessage(t, db, 10, 1, VoiceKindVoice, false)     // candidate
	seedVoiceMessage(t, db, 10, 2, VoiceKindVoice, true)      // own
	seedVoiceMessage(t, db, 10, 3, VoiceKindVoice, false)     // deleted below
	seedVoiceMessage(t, db, 10, 4, VoiceKindVideo, false)     // a video
	seedVoiceMessage(t, db, 10, 5, VoiceKindVideoNote, false) // candidate
	seedVoiceMessage(t, db, 10, 6, VoiceKindVoice, false)     // has a text
	seedVoiceMessage(t, db, 10, 7, VoiceKindVoice, false)     // text pending: still a candidate
	seedVoiceMessage(t, db, 10, 8, VoiceKindVoice, false)     // has a queue row
	seedVoiceMessage(t, db, 10, 9, "photo", false)            // not voice
	seedVoiceMessage(t, db, 10, 10, "", false)                // no media
	seedVoiceMessage(t, db, 11, 1, VoiceKindVoice, false)     // candidate, another chat
	seedVoiceMessage(t, db, 12, 1, VoiceKindVoice, false)     // a bot chat
	seedVoiceMessage(t, db, -5, 1, VoiceKindVoice, false)     // a group
	seedVoiceMessage(t, db, -5, 2, VoiceKindVideo, false)
	if _, err := db.MarkMessagesDeleted(ctx, 10, []int{3}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutTranscription(ctx, Transcription{ChatID: 10, MessageID: 6, Text: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutTranscription(ctx, Transcription{ChatID: 10, MessageID: 7, Pending: true}); err != nil {
		t.Fatal(err)
	}
	mustEnqueue(t, db, voiceJob(10, 8, VoiceKindVoice, VoiceSourceSync, VoicePrioritySync), false)

	audio := []string{VoiceKindVoice, VoiceKindVideoNote}
	page := func(q VoiceCandidateQuery) [][2]int64 {
		t.Helper()
		if q.Limit == 0 {
			q.Limit = MaxExportPageSize
		}
		got, err := db.VoiceCandidates(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return candidateKeys(got)
	}
	// Personal chats only: the bot and the group are out.
	want := [][2]int64{{10, 1}, {10, 5}, {10, 7}, {11, 1}}
	if got := page(VoiceCandidateQuery{Private: true, Kinds: audio}); !reflect.DeepEqual(got, want) {
		t.Errorf("personal = %v, want %v", got, want)
	}
	// One chat, with video added.
	want = [][2]int64{{10, 1}, {10, 4}, {10, 5}, {10, 7}}
	if got := page(VoiceCandidateQuery{ChatID: 10, Kinds: append(audio, VoiceKindVideo)}); !reflect.DeepEqual(got, want) {
		t.Errorf("one chat with video = %v, want %v", got, want)
	}
	// A group chat by id, no personal restriction.
	want = [][2]int64{{-5, 1}, {-5, 2}}
	if got := page(VoiceCandidateQuery{ChatID: -5, Kinds: append(audio, VoiceKindVideo)}); !reflect.DeepEqual(got, want) {
		t.Errorf("group = %v, want %v", got, want)
	}
	// The filter closes chats before the limit.
	want = [][2]int64{{11, 1}}
	if got := page(VoiceCandidateQuery{Private: true, Kinds: audio, Filter: ChatFilter{Block: []int64{10}}}); !reflect.DeepEqual(got, want) {
		t.Errorf("blocked = %v, want %v", got, want)
	}
	if got := page(VoiceCandidateQuery{Private: true, Kinds: audio, Filter: ChatFilter{Allow: []int64{11}}}); !reflect.DeepEqual(got, want) {
		t.Errorf("allowed = %v, want %v", got, want)
	}
	if got := page(VoiceCandidateQuery{Private: true, Kinds: audio, Filter: ChatFilter{Hide: true}}); len(got) != 0 {
		t.Errorf("hidden = %v", got)
	}
	// Paging by key walks every candidate once, across chats, with no ceiling on chats.
	var walked [][2]int64
	var after *VoiceCandidate
	for {
		got, err := db.VoiceCandidates(ctx, VoiceCandidateQuery{Private: true, Kinds: audio, After: after, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, candidateKeys(got)...)
		if len(got) < 2 {
			break
		}
		after = &got[len(got)-1]
	}
	if want := [][2]int64{{10, 1}, {10, 5}, {10, 7}, {11, 1}}; !reflect.DeepEqual(walked, want) {
		t.Errorf("walked = %v, want %v", walked, want)
	}
	// A queued row hides the message, whatever the state.
	mustEnqueue(t, db, voiceJob(11, 1, VoiceKindVoice, VoiceSourceBackfill, VoicePriorityBackfill), false)
	if got := page(VoiceCandidateQuery{Private: true, Kinds: audio}); len(got) != 3 {
		t.Errorf("after queueing one = %v", got)
	}
}

func TestVoiceSourceCandidatesRejectBadQueries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for name, q := range map[string]VoiceCandidateQuery{
		"no limit":   {Kinds: []string{VoiceKindVoice}},
		"big limit":  {Kinds: []string{VoiceKindVoice}, Limit: MaxExportPageSize + 1},
		"no kinds":   {Limit: 10},
		"bad kind":   {Kinds: []string{"photo"}, Limit: 10},
		"empty kind": {Kinds: []string{""}, Limit: 10},
	} {
		if _, err := db.VoiceCandidates(ctx, q); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestVoiceSourcePrivateChatCount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 10, Type: "user"}, {ID: 11, Type: "user"}, {ID: 13, Type: "user"}, {ID: 12, Type: "bot"}, {ID: -5, Type: "group"}}); err != nil {
		t.Fatal(err)
	}
	for _, chat := range []int64{10, 11, 12, -5} {
		seedVoiceMessage(t, db, chat, 1, VoiceKindVoice, false)
	}
	for name, tc := range map[string]struct {
		filter ChatFilter
		want   int
	}{
		"all":     {ChatFilter{}, 2},
		"blocked": {ChatFilter{Block: []int64{10}}, 1},
		"allowed": {ChatFilter{Allow: []int64{-5}}, 0},
		"hidden":  {ChatFilter{Hide: true}, 0},
	} {
		if got, err := db.VoicePrivateChats(ctx, tc.filter); err != nil || got != tc.want {
			t.Errorf("%s: %d %v, want %d", name, got, err, tc.want)
		}
	}
}
