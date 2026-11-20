package store

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// mediaSearchFixture stores one message per attachment type, all with the same
// caption word, plus a plain text message with it and a message of a sticker,
// a type the filter does not offer.
func mediaSearchFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "Open"}, {ID: 2, Type: "group", Title: "Closed"}}); err != nil {
		t.Fatal(err)
	}
	var messages []Message
	for i, media := range []string{"voice", "video_note", "video", "photo", "document", "gif", "audio", "sticker"} {
		messages = append(messages, Message{ChatID: 1, ID: i + 1, Date: int64(100 + i), Text: "квартальный отчёт", MediaType: strPtr(media), Raw: raw(`{}`)})
	}
	messages = append(messages,
		Message{ChatID: 1, ID: 20, Date: 120, Text: "квартальный отчёт без вложения", Raw: raw(`{}`)},
		Message{ChatID: 2, ID: 30, Date: 130, Text: "квартальный отчёт", MediaType: strPtr("photo"), Raw: raw(`{}`)},
	)
	if err := db.UpsertMessages(ctx, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func searchedIDs(hits []SearchHit) []int64 {
	var ids []int64
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func TestSearchMediaFilterKeepsOneAttachmentType(t *testing.T) {
	db := mediaSearchFixture(t)
	ctx := context.Background()
	want := map[string][]int64{
		"voice": {1}, "video_note": {2}, "video": {3}, "document": {5}, "gif": {6}, "audio": {7},
		// Message 30 sits in the closed chat and is hidden by the filter below.
		"photo": {4},
	}
	closed := ChatFilter{Allow: []int64{1}}
	for _, media := range SearchMediaTypes {
		hits, err := db.Search(ctx, SearchOptions{Query: "отчёт", Scopes: []string{"messages"}, MediaType: media, ChatFilter: closed})
		if err != nil {
			t.Fatalf("%s: %v", media, err)
		}
		if got := searchedIDs(hits); !reflect.DeepEqual(got, want[media]) {
			t.Errorf("media %s found %v, want %v", media, got, want[media])
		}
		for _, hit := range hits {
			if hit.MediaType != media {
				t.Errorf("media %s: hit %d reports media_type %q", media, hit.ID, hit.MediaType)
			}
		}
	}
}

func TestSearchMediaFilterOffReportsTypeOfEveryHit(t *testing.T) {
	db := mediaSearchFixture(t)
	hits, err := db.Search(context.Background(), SearchOptions{Query: "отчёт", Scopes: []string{"messages"}, ChatID: intPtr(1)})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for _, hit := range hits {
		got[hit.ID] = hit.MediaType
	}
	want := map[int64]string{1: "voice", 2: "video_note", 3: "video", 4: "photo", 5: "document", 6: "gif", 7: "audio", 8: "sticker", 20: ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("media types = %v, want %v", got, want)
	}
}

func TestSearchMediaFilterHidesClosedChatsAndRejectsUnknownType(t *testing.T) {
	db := mediaSearchFixture(t)
	ctx := context.Background()
	// Without the access filter the photo of the other chat shows up, with it
	// the chat is not seen: the filter must not widen what the lists allow.
	open, err := db.Search(ctx, SearchOptions{Query: "отчёт", Scopes: []string{"messages"}, MediaType: "photo"})
	if err != nil || !reflect.DeepEqual(searchedIDs(open), []int64{4, 30}) {
		t.Fatalf("unfiltered photo search = %v, %v", searchedIDs(open), err)
	}
	hidden, err := db.Search(ctx, SearchOptions{Query: "отчёт", Scopes: []string{"messages"}, MediaType: "photo", ChatFilter: ChatFilter{Allow: []int64{1}}})
	if err != nil || !reflect.DeepEqual(searchedIDs(hidden), []int64{4}) {
		t.Fatalf("access-limited photo search = %v, %v", searchedIDs(hidden), err)
	}
	for _, bad := range []string{"sticker", "Photo", "photo OR voice", "link"} {
		if _, err := db.Search(ctx, SearchOptions{Query: "отчёт", MediaType: bad}); !errors.Is(err, ErrInvalidSearchOptions) {
			t.Errorf("media %q: error = %v, want ErrInvalidSearchOptions", bad, err)
		}
	}
}
