package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func messageIDs(hits []SearchHit) []int64 {
	ids := make([]int64, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	return ids
}

func searchMessageIDs(t *testing.T, db *DB, options SearchOptions) []int64 {
	t.Helper()
	options.Scopes = []string{"messages"}
	hits, err := db.Search(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	return messageIDs(hits)
}

func stemSearchFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{
		{ID: 1, Type: "group", Title: "One"},
		{ID: 2, Type: "group", Title: "Two"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{
		{ChatID: 1, ID: 1, Date: 1, Text: "Доставка задерживается", Raw: raw(`{}`)},
		{ChatID: 1, ID: 2, Date: 2, Text: "Заберу доставку завтра", Raw: raw(`{}`)},
		{ChatID: 1, ID: 3, Date: 3, Text: "Артикул AB-4421-X в наличии, iPhone15 тоже", Raw: raw(`{}`)},
		{ChatID: 2, ID: 4, Date: 4, Text: "Все доставки отменены", Raw: raw(`{}`)},
		{ChatID: 1, ID: 5, Date: 5, Text: "Оплатил вчера", Raw: raw(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSearchFindsWordFormsWithoutPrefix(t *testing.T) {
	db := stemSearchFixture(t)
	got := searchMessageIDs(t, db, SearchOptions{Query: "доставка"})
	// "доставка", "доставку" and "доставки" are different tokens for the exact
	// leg; the stemmed leg joins them.
	if !reflect.DeepEqual(sortedIDs(got), []int64{1, 2, 4}) {
		t.Errorf("доставка found %v, want messages 1, 2 and 4", got)
	}
	if got := searchMessageIDs(t, db, SearchOptions{Query: "оплата"}); !reflect.DeepEqual(got, []int64{5}) {
		t.Errorf("оплата found %v, want [5]", got)
	}
}

func sortedIDs(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestSearchExactLegKeepsNumbersAndNames(t *testing.T) {
	db := stemSearchFixture(t)
	for _, query := range []string{"4421", "ab", "iphone15", "IPHONE15"} {
		if got := searchMessageIDs(t, db, SearchOptions{Query: query}); !reflect.DeepEqual(got, []int64{3}) {
			t.Errorf("%q found %v, want [3]", query, got)
		}
	}
	// A number is not a stem prefix of anything else: iphone15 must not match iphone1.
	if got := searchMessageIDs(t, db, SearchOptions{Query: "iphone1"}); len(got) != 0 {
		t.Errorf("iphone1 found %v, want nothing", got)
	}
}

func TestSearchExactFormRanksAboveStemOnlyForm(t *testing.T) {
	db := stemSearchFixture(t)
	// Message 1 holds the queried form: both legs rank it. Messages 2 and 4 hold
	// other forms and come from the stemmed leg only.
	got := searchMessageIDs(t, db, SearchOptions{Query: "доставка"})
	if len(got) != 3 || got[0] != 1 {
		t.Errorf("доставка order = %v, want message 1 first", got)
	}
	hits, err := db.Search(context.Background(), SearchOptions{Query: "доставка", Scopes: []string{"messages"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Rank < hits[i-1].Rank {
			t.Errorf("rank must not decrease down the list: %v then %v", hits[i-1].Rank, hits[i].Rank)
		}
	}
	if want := -(1.0/61 + 1.0/61); hits[0].Rank > want+1e-12 || hits[0].Rank < want-1e-12 {
		t.Errorf("rank of a message first in both legs = %v, want %v", hits[0].Rank, want)
	}
}

func TestSearchPrefixAppliesToTheLastTermInBothLegs(t *testing.T) {
	db := stemSearchFixture(t)
	// "достав" is a prefix of the word in message 1 (exact leg) and of the stem
	// "доставк" (stemmed leg).
	got := searchMessageIDs(t, db, SearchOptions{Query: "достав", Prefix: true})
	if !reflect.DeepEqual(sortedIDs(got), []int64{1, 2, 4}) {
		t.Errorf("prefix found %v, want 1, 2 and 4", got)
	}
	// Only the last term is a prefix: "оплат" stays a whole stem.
	if got := searchMessageIDs(t, db, SearchOptions{Query: "оплат вчер", Prefix: true}); !reflect.DeepEqual(got, []int64{5}) {
		t.Errorf("two terms with a prefix found %v, want [5]", got)
	}
}

func TestSearchFiltersApplyToBothLegs(t *testing.T) {
	db := stemSearchFixture(t)
	ctx := context.Background()
	chat := int64(1)
	if got := searchMessageIDs(t, db, SearchOptions{Query: "доставка", ChatID: &chat}); !reflect.DeepEqual(sortedIDs(got), []int64{1, 2}) {
		t.Errorf("chat filter found %v, want 1 and 2", got)
	}
	if got := searchMessageIDs(t, db, SearchOptions{Query: "доставка", ChatFilter: ChatFilter{Block: []int64{2}}}); !reflect.DeepEqual(sortedIDs(got), []int64{1, 2}) {
		t.Errorf("closed chat leaked: %v", got)
	}
	if got := searchMessageIDs(t, db, SearchOptions{Query: "доставка", ChatFilter: ChatFilter{Allow: []int64{2}}}); !reflect.DeepEqual(got, []int64{4}) {
		t.Errorf("allow list found %v, want [4]", got)
	}
	if got := searchMessageIDs(t, db, SearchOptions{Query: "доставка", ChatFilter: ChatFilter{Hide: true}}); len(got) != 0 {
		t.Errorf("hidden chats leaked: %v", got)
	}
	// A deleted message is hidden in both legs and comes back with --deleted.
	if _, err := db.MarkMessagesDeleted(ctx, 1, []int{2}); err != nil {
		t.Fatal(err)
	}
	if got := searchMessageIDs(t, db, SearchOptions{Query: "доставка"}); !reflect.DeepEqual(sortedIDs(got), []int64{1, 4}) {
		t.Errorf("deleted message found: %v", got)
	}
	if got := searchMessageIDs(t, db, SearchOptions{Query: "доставка", IncludeDeleted: true}); !reflect.DeepEqual(sortedIDs(got), []int64{1, 2, 4}) {
		t.Errorf("--deleted found %v, want 1, 2 and 4", got)
	}
}

func TestSearchSnippetCentersOnStemOnlyMatch(t *testing.T) {
	db := openTestDB(t)
	text := strings.Repeat("слово ", 40) + "доставку" + strings.Repeat(" слово", 40)
	if err := db.UpsertMessages(context.Background(), []Message{{ChatID: 1, ID: 1, Date: 1, Text: text, Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	hits, err := db.Search(context.Background(), SearchOptions{Query: "доставка", Scopes: []string{"messages"}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search = %+v, %v", hits, err)
	}
	if !strings.Contains(hits[0].Snippet, "доставку") {
		t.Errorf("snippet misses the matched word form: %q", hits[0].Snippet)
	}
}

func TestFuseRRFOrderOnCraftedLegs(t *testing.T) {
	candidate := func(id int64) messageCandidate {
		chat := int64(1)
		return messageCandidate{hit: SearchHit{ID: id, ChatID: &chat}}
	}
	exact := []messageCandidate{candidate(1), candidate(2), candidate(3)}
	stemmed := []messageCandidate{candidate(3), candidate(1), candidate(4)}
	got := fuseRRF(10, exact, stemmed)
	// 1: 1/61+1/62, 3: 1/63+1/61, 2: 1/62, 4: 1/63.
	var ids []int64
	for _, entry := range got {
		ids = append(ids, entry.candidate.hit.ID)
	}
	if want := []int64{1, 3, 2, 4}; !reflect.DeepEqual(ids, want) {
		t.Errorf("order = %v, want %v", ids, want)
	}
	if want := 1.0/61 + 1.0/62; got[0].score < want-1e-12 || got[0].score > want+1e-12 {
		t.Errorf("score of the first = %v, want %v", got[0].score, want)
	}
	if cut := fuseRRF(2, exact, stemmed); len(cut) != 2 || cut[1].candidate.hit.ID != 3 {
		t.Errorf("limit cut = %+v", cut)
	}
	// Equal scores: the exact leg wins.
	tie := fuseRRF(10, []messageCandidate{candidate(7)}, []messageCandidate{candidate(8)})
	if tie[0].candidate.hit.ID != 7 || tie[1].candidate.hit.ID != 8 {
		t.Errorf("tie order = %v, %v, want exact leg first", tie[0].candidate.hit.ID, tie[1].candidate.hit.ID)
	}
}

func TestSearchHitLinks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{
		{ID: -1001000000001, Type: "supergroup", Title: "Public", Username: stringPtr("PubGroup")},
		{ID: -1001000000002, Type: "supergroup", Title: "Forum"},
		{ID: -1001000000003, Type: "channel", Title: "Channel", Username: stringPtr("newsfeed")},
		{ID: 55, Type: "user", Title: "Alice"},
		{ID: 56, Type: "bot", Title: "Helper"},
		{ID: -4242, Type: "group", Title: "Basic"},
	}); err != nil {
		t.Fatal(err)
	}
	topic := int64(9)
	if err := db.UpsertMessages(ctx, []Message{
		{ChatID: -1001000000001, ID: 1, Date: 1, Text: "ссылка", Raw: raw(`{}`)},
		{ChatID: -1001000000002, ID: 2, Date: 2, Text: "ссылка", Raw: raw(`{}`)},
		{ChatID: -1001000000002, ID: 3, Date: 3, Text: "ссылка", TopicID: &topic, Raw: raw(`{}`)},
		{ChatID: -1001000000001, ID: 4, Date: 4, Text: "ссылка", TopicID: &topic, Raw: raw(`{}`)},
		{ChatID: -1001000000003, ID: 5, Date: 5, Text: "ссылка", Raw: raw(`{}`)},
		{ChatID: 55, ID: 6, Date: 6, Text: "ссылка", Raw: raw(`{}`)},
		{ChatID: 56, ID: 7, Date: 7, Text: "ссылка", Raw: raw(`{}`)},
		{ChatID: -4242, ID: 8, Date: 8, Text: "ссылка", Raw: raw(`{}`)},
		{ChatID: 999, ID: 9, Date: 9, Text: "ссылка", Raw: raw(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	hits, err := db.Search(ctx, SearchOptions{Query: "ссылки", Scopes: []string{"messages"}})
	if err != nil || len(hits) != 9 {
		t.Fatalf("Search = %d hits, %v", len(hits), err)
	}
	type want struct {
		link        string
		unavailable string
	}
	wants := map[int64]want{
		1: {link: "https://t.me/pubgroup/1"},
		2: {link: "https://t.me/c/1000000002/2"},
		3: {link: "https://t.me/c/1000000002/9/3"},
		4: {link: "https://t.me/pubgroup/9/4"},
		5: {link: "https://t.me/newsfeed/5"},
		6: {unavailable: "private_chat"},
		7: {unavailable: "private_chat"},
		8: {unavailable: "basic_group"},
		9: {},
	}
	for _, hit := range hits {
		w := wants[hit.ID]
		switch {
		case w.link == "" && hit.Link != nil:
			t.Errorf("message %d: link = %q, want none", hit.ID, *hit.Link)
		case w.link != "" && (hit.Link == nil || *hit.Link != w.link):
			t.Errorf("message %d: link = %v, want %s", hit.ID, hit.Link, w.link)
		}
		if hit.LinkUnavailable != w.unavailable {
			t.Errorf("message %d: link_unavailable = %q, want %q", hit.ID, hit.LinkUnavailable, w.unavailable)
		}
	}
	// Chat and people hits carry no link.
	other, err := db.Search(ctx, SearchOptions{Query: "Alice", Scopes: []string{"chats"}})
	if err != nil || len(other) != 1 || other[0].Link != nil || other[0].LinkUnavailable != "" {
		t.Errorf("chat hit = %+v, %v", other, err)
	}
}
