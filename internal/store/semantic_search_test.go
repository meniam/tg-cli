package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func wordMatchIDs(leg []WordMatch) []int64 {
	ids := make([]int64, 0, len(leg))
	for _, m := range leg {
		ids = append(ids, m.ID)
	}
	return ids
}

// The legs rank messages: the queried form first in both, other forms only in
// the stemmed leg. A deleted message and a closed chat are in neither.
func TestWordLegsRankAndFilter(t *testing.T) {
	db := stemSearchFixture(t)
	ctx := context.Background()
	exact, stemmed, err := db.WordLegs(ctx, WordLegOptions{Query: "доставка", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wordMatchIDs(exact), []int64{1}) {
		t.Errorf("exact leg = %v, want [1]", wordMatchIDs(exact))
	}
	if !reflect.DeepEqual(sortedIDs(wordMatchIDs(stemmed)), []int64{1, 2, 4}) {
		t.Errorf("stemmed leg = %v, want messages 1, 2 and 4", wordMatchIDs(stemmed))
	}
	if exact[0].ChatID != 1 || exact[0].Date != 1 || exact[0].TopicID != 0 {
		t.Errorf("match fields = %+v", exact[0])
	}

	_, stemmed, err = db.WordLegs(ctx, WordLegOptions{Query: "доставка", Limit: 10, ChatFilter: ChatFilter{Block: []int64{2}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range stemmed {
		if m.ChatID == 2 {
			t.Errorf("a closed chat is in the stemmed leg: %+v", m)
		}
	}
	chat := int64(2)
	_, stemmed, err = db.WordLegs(ctx, WordLegOptions{Query: "доставка", Limit: 10, ChatID: &chat})
	if err != nil || !reflect.DeepEqual(wordMatchIDs(stemmed), []int64{4}) {
		t.Errorf("chat restriction: %v, %v", wordMatchIDs(stemmed), err)
	}

	if _, err := db.MarkMessagesDeleted(ctx, 1, []int{1}); err != nil {
		t.Fatal(err)
	}
	exact, stemmed, err = db.WordLegs(ctx, WordLegOptions{Query: "доставка", Limit: 10})
	if err != nil || len(exact) != 0 || len(wordMatchIDs(stemmed)) != 2 {
		t.Errorf("deleted message: exact=%v stemmed=%v err=%v", wordMatchIDs(exact), wordMatchIDs(stemmed), err)
	}
}

func TestWordLegsRejectEmptyQueryAndReportTopics(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, _, err := db.WordLegs(ctx, WordLegOptions{Query: "  ", Limit: 5}); err != ErrEmptyQuery {
		t.Errorf("empty query error = %v, want ErrEmptyQuery", err)
	}
	topic := int64(9)
	if err := db.UpsertChats(ctx, []Chat{{ID: -1001000000001, Type: "supergroup", Title: "Forum"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: -1001000000001, ID: 5, Date: 50, Text: "tree", TopicID: &topic, Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	exact, _, err := db.WordLegs(ctx, WordLegOptions{Query: "tree", Limit: 5})
	if err != nil || len(exact) != 1 || exact[0].TopicID != 9 {
		t.Errorf("topic of a match: %+v, %v", exact, err)
	}
}

func TestFragmentText(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	topic := int64(3)
	service := "pinned"
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "One"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{
		{ChatID: 1, ID: 10, Date: 100, Text: "first", Raw: raw(`{}`)},
		// Same date as 10 and a higher id: the id breaks the tie.
		{ChatID: 1, ID: 11, Date: 100, Text: "second", Raw: raw(`{}`)},
		{ChatID: 1, ID: 9, Date: 101, Text: "third by date", Raw: raw(`{}`)},
		{ChatID: 1, ID: 12, Date: 102, Text: "deleted one", Raw: raw(`{}`)},
		{ChatID: 1, ID: 13, Date: 103, Text: "service text", Service: &service, Raw: raw(`{}`)},
		{ChatID: 1, ID: 14, Date: 104, Text: "", Raw: raw(`{}`)},
		{ChatID: 1, ID: 15, Date: 105, Text: "other topic", TopicID: &topic, Raw: raw(`{}`)},
		{ChatID: 1, ID: 99, Date: 106, Text: "out of range", Raw: raw(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkMessagesDeleted(ctx, 1, []int{12}); err != nil {
		t.Fatal(err)
	}
	got, err := db.FragmentText(ctx, 1, 0, 9, 20, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if want := "first\nsecond\nthird by date"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if got, _ := db.FragmentText(ctx, 1, 3, 9, 20, 1500); got != "other topic" {
		t.Errorf("topic text = %q", got)
	}
	if got, _ := db.FragmentText(ctx, 1, 0, 12, 12, 1500); got != "" {
		t.Errorf("a fragment of deleted messages gave %q, want empty", got)
	}
	if got, _ := db.FragmentText(ctx, 2, 0, 9, 20, 1500); got != "" {
		t.Errorf("another chat gave %q", got)
	}
}

func TestFragmentTextIsCutToTheLimit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "One"}}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("я", 40)
	var messages []Message
	for i := 1; i <= 5; i++ {
		messages = append(messages, Message{ChatID: 1, ID: i, Date: int64(i), Text: long, Raw: raw(`{}`)})
	}
	if err := db.UpsertMessages(ctx, messages); err != nil {
		t.Fatal(err)
	}
	got, err := db.FragmentText(ctx, 1, 0, 1, 5, 100)
	if err != nil {
		t.Fatal(err)
	}
	runes := []rune(got)
	if len(runes) != 100 || runes[99] != '…' {
		t.Errorf("cut text has %d characters, last %q; want 100 ending with an ellipsis", len(runes), string(runes[len(runes)-1]))
	}
	// A text of exactly the limit is not marked as cut.
	if got, _ := db.FragmentText(ctx, 1, 0, 1, 1, 40); got != long {
		t.Errorf("text of exactly the limit was changed: %q", got)
	}
}

func TestChatLinks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	username := "pubgroup"
	if err := db.UpsertChats(ctx, []Chat{
		{ID: -1001000000001, Type: "supergroup", Title: "Public", Username: &username},
		{ID: 55, Type: "user", Title: "Alice"},
	}); err != nil {
		t.Fatal(err)
	}
	links, err := db.ChatLinks(ctx, []int64{-1001000000001, 55, 777})
	if err != nil {
		t.Fatal(err)
	}
	if links[-1001000000001] != (ChatLink{Type: "supergroup", Username: "pubgroup", Title: "Public"}) || links[55] != (ChatLink{Type: "user", Title: "Alice"}) {
		t.Errorf("links = %+v", links)
	}
	if _, ok := links[777]; ok {
		t.Error("an unknown chat is in the map")
	}
	if empty, err := db.ChatLinks(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no ids: %v, %v", empty, err)
	}
}

// The exported link builder keeps the rules the ordinary search relies on.
func TestMessageLinkKinds(t *testing.T) {
	topic := int64(9)
	cases := []struct {
		name        string
		chatID      int64
		chatType    string
		username    string
		topic       *int64
		link        string
		unavailable string
	}{
		{"public", -1001000000001, "supergroup", "pubgroup", nil, "https://t.me/pubgroup/7", ""},
		{"private channel", -1001000000002, "channel", "", nil, "https://t.me/c/1000000002/7", ""},
		{"forum topic", -1001000000001, "supergroup", "pubgroup", &topic, "https://t.me/pubgroup/9/7", ""},
		{"direct chat", 55, "user", "", nil, "", "private_chat"},
		{"basic group", -4242, "group", "", nil, "", "basic_group"},
		{"unknown chat", 1, "", "", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link, unavailable := MessageLink(tc.chatID, 7, tc.chatType, tc.username, tc.topic)
			if tc.link == "" {
				if link != nil {
					t.Errorf("link = %q, want none", *link)
				}
			} else if link == nil || *link != tc.link {
				t.Errorf("link = %v, want %q", link, tc.link)
			}
			if unavailable != tc.unavailable {
				t.Errorf("unavailable = %q, want %q", unavailable, tc.unavailable)
			}
		})
	}
}
