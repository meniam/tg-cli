package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func searchFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "Ёлка"}, {ID: 2, Type: "group", Title: "Secondary"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{{ID: 7, FirstName: stringPtr("Елка"), LastName: stringPtr("Person")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 11, Date: 100, Text: "Строгая Ёлка", Raw: raw(`{}`)}, {ChatID: 2, ID: 12, Date: 200, Text: "Елка", Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	for _, note := range []Note{
		{Kind: "chat", ChatID: intPtr(1), Body: "Моя Елка", Author: "owner"},
		{Kind: "message", ChatID: intPtr(1), TargetID: intPtr(11), Body: "Ёлка от AI", Author: "ai", Model: stringPtr("model-x")},
		{Kind: "chat", ChatID: intPtr(2), Body: "Елка в другом чате", Author: "owner"},
	} {
		if _, err := db.AddNote(ctx, note); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestSearchScopesFiltersAndSnippets(t *testing.T) {
	db := searchFixture(t)
	ctx := context.Background()
	hits, err := db.Search(ctx, SearchOptions{Query: "ЁЛКА"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"chats", "people", "messages", "messages", "notes", "notes", "notes"}
	if len(hits) != len(want) {
		t.Fatalf("hits = %+v, want scopes %v", hits, want)
	}
	for i, hit := range hits {
		if hit.Scope != want[i] {
			t.Errorf("hit[%d].Scope = %s, want %s", i, hit.Scope, want[i])
		}
		if i > 0 && hit.Scope == hits[i-1].Scope && hit.Rank < hits[i-1].Rank {
			t.Errorf("rank order in %s: %f before %f", hit.Scope, hits[i-1].Rank, hit.Rank)
		}
	}
	if hits[0].Title != "Ёлка" || !strings.Contains(hits[0].Snippet, "Ёлка") || hits[1].Title != "Елка Person" || !strings.Contains(hits[1].Snippet, "Елка") {
		t.Errorf("chat/person hits lost original text: %+v %+v", hits[0], hits[1])
	}
	for _, hit := range hits {
		if hit.Scope == "messages" && hit.ID == 11 {
			if hit.ChatID == nil || *hit.ChatID != 1 || hit.Date != 100 || hit.Title != "" || !strings.Contains(hit.Snippet, "Ёлка") {
				t.Errorf("message hit = %+v", hit)
			}
		}
		if hit.Scope == "notes" && hit.Author == "ai" {
			if hit.ChatID == nil || *hit.ChatID != 1 || hit.NoteKind != "message" || hit.Model == nil || *hit.Model != "model-x" || !strings.Contains(hit.Snippet, "Ёлка") {
				t.Errorf("AI note hit = %+v", hit)
			}
		}
	}
	cases := []struct {
		name    string
		options SearchOptions
		want    int
	}{
		{"prefix", SearchOptions{Query: "ЕЛ*", Prefix: true}, 7},
		{"owner notes only", SearchOptions{Query: "елка", Scopes: []string{"notes"}, Author: "owner"}, 2},
		{"author does not hide Telegram", SearchOptions{Query: "елка", Author: "owner"}, 6},
		{"chat restricts messages", SearchOptions{Query: "елка", Scopes: []string{"messages"}, ChatID: intPtr(1)}, 1},
		{"chat restricts notes", SearchOptions{Query: "елка", Scopes: []string{"notes"}, ChatID: intPtr(1)}, 2},
		{"chat leaves chats and people", SearchOptions{Query: "елка", ChatID: intPtr(1)}, 5},
		{"per scope limit", SearchOptions{Query: "елка", Limit: 1}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := db.Search(ctx, tc.options)
			if err != nil || len(results) != tc.want {
				t.Fatalf("Search = %+v, %v, want %d hits", results, err, tc.want)
			}
		})
	}
	if _, err := db.AddNote(ctx, Note{Kind: "chat", ChatID: intPtr(1), Body: "Не цветок", Author: "owner", Tags: []string{"Секрет"}}); err != nil {
		t.Fatal(err)
	}
	byTag, err := db.Search(ctx, SearchOptions{Query: "СЕКРЕТ", Scopes: []string{"notes"}})
	if err != nil || len(byTag) != 1 || byTag[0].Snippet != "Не цветок" {
		t.Fatalf("tag search = %+v, %v", byTag, err)
	}
	if _, err := db.MarkMessagesDeleted(ctx, 1, []int{11}); err != nil {
		t.Fatal(err)
	}
	live, err := db.Search(ctx, SearchOptions{Query: "елка", Scopes: []string{"messages"}})
	if err != nil || len(live) != 1 || live[0].ID != 12 || live[0].Deleted {
		t.Fatalf("live messages = %+v, %v", live, err)
	}
	all, err := db.Search(ctx, SearchOptions{Query: "елка", Scopes: []string{"messages"}, IncludeDeleted: true})
	if err != nil || len(all) != 2 {
		t.Fatalf("all messages = %+v, %v", all, err)
	}
	var sawDeleted bool
	for _, hit := range all {
		if hit.ID == 11 {
			sawDeleted = hit.Deleted
		}
	}
	if !sawDeleted {
		t.Error("deleted message was not marked in search")
	}
}

func TestSearchValidationAndInjection(t *testing.T) {
	db := searchFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		opt  SearchOptions
		want error
	}{
		{"empty", SearchOptions{Query: " \t "}, ErrEmptyQuery},
		{"punctuation only", SearchOptions{Query: `"()*`}, ErrEmptyQuery},
		{"unknown scope", SearchOptions{Query: "елка", Scopes: []string{"unknown"}}, ErrInvalidSearchOptions},
		{"unknown author", SearchOptions{Query: "елка", Author: "bot"}, ErrInvalidSearchOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Search(ctx, tc.opt); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
	for _, input := range []string{`a OR b`, `NEAR(a b)`, `title:secret`, `"елка"`, `елка*`, `(елка)`, `-x`, `"unbalanced`} {
		t.Run(input, func(t *testing.T) {
			if _, err := db.Search(ctx, SearchOptions{Query: input}); err != nil {
				t.Errorf("unsafe MATCH input %q caused error: %v", input, err)
			}
		})
	}
	if err := db.UpsertChats(ctx, []Chat{
		{ID: 3, Type: "group", Title: "alpha"},
		{ID: 4, Type: "group", Title: "beta"},
		{ID: 5, Type: "group", Title: "secret"},
		{ID: 6, Type: "group", Title: "alpha beta"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"alpha OR beta", "NEAR(alpha beta)", "title:secret", "a*"} {
		hits, err := db.Search(ctx, SearchOptions{Query: input, Scopes: []string{"chats"}})
		if err != nil || len(hits) != 0 {
			t.Errorf("syntax input %q acted as an operator: hits=%+v err=%v", input, hits, err)
		}
	}
}

func TestSearchPerScopeLimitBounds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	batch := make([]Message, 105)
	for i := range batch {
		batch[i] = Message{ChatID: 1, ID: i + 1, Date: int64(i), Text: "common", Raw: raw(`{}`)}
	}
	if err := db.UpsertMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ limit, want int }{{0, 20}, {1000, 100}} {
		hits, err := db.Search(ctx, SearchOptions{Query: "common", Scopes: []string{"messages"}, Limit: tc.limit})
		if err != nil || len(hits) != tc.want {
			t.Errorf("limit %d: got %d hits, err=%v, want %d", tc.limit, len(hits), err, tc.want)
		}
	}
}

func TestSearchSnippetRuneWindow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	text := strings.Repeat("До ", 100) + "Ёлка" + strings.Repeat(" После", 100)
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 1, Date: 1, Text: text, Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	hits, err := db.Search(ctx, SearchOptions{Query: "ЕЛКА", Scopes: []string{"messages"}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search = %+v, %v", hits, err)
	}
	snippet := hits[0].Snippet
	if !utf8.ValidString(snippet) || !strings.Contains(snippet, "Ёлка") || !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") || utf8.RuneCountInString(snippet) > 162 {
		t.Errorf("invalid rune window: %q", snippet)
	}
}

func TestSearchSnippetUsesAboutAndBio(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "Unrelated", About: stringPtr("Там Ёлка")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{{ID: 2, FirstName: stringPtr("Unrelated"), Bio: stringPtr("Здесь Ёлка")}}); err != nil {
		t.Fatal(err)
	}
	hits, err := db.Search(ctx, SearchOptions{Query: "ЕЛКА", Scopes: []string{"people", "chats"}})
	if err != nil || len(hits) != 2 {
		t.Fatalf("Search = %+v, %v", hits, err)
	}
	if hits[0].Scope != "chats" || hits[0].Snippet != "Там Ёлка" || hits[1].Scope != "people" || hits[1].Snippet != "Здесь Ёлка" {
		t.Errorf("source snippets = %+v", hits)
	}
}

func TestSearchWhileAddingNotes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.AddNote(ctx, Note{Kind: "chat", ChatID: intPtr(1), Body: "Ёлка", Author: "owner"}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 100)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			_, err := db.AddNote(ctx, Note{Kind: "chat", ChatID: intPtr(1), Body: "Ёлка", Author: "owner"})
			results <- err
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			_, err := db.Search(ctx, SearchOptions{Query: "елка", Scopes: []string{"notes"}})
			results <- err
		}
	}()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent search/add: %v", err)
		}
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM notes`); n != 51 {
		t.Errorf("notes after concurrent writes = %d", n)
	}
	checkFTSIntegrity(t, db.write)
}
