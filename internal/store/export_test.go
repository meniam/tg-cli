package store

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"
)

func exportFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	messages := make([]Message, 0, 9)
	for _, id := range []int{7, 2, 5, 1, 4, 6, 3} {
		messages = append(messages, Message{ChatID: 42, ID: id, Date: int64(100 + id), Text: "saved", Raw: raw(`{}`)})
	}
	messages = append(messages,
		Message{ChatID: 77, ID: 4, Date: 104, Text: "other chat", Raw: raw(`{}`)},
		Message{ChatID: 88, ID: 9, Date: 109, Text: "single", Raw: raw(`{}`)},
	)
	if err := db.UpsertMessages(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkMessagesDeleted(context.Background(), 42, []int{3}); err != nil {
		t.Fatal(err)
	}
	return db
}

func exportIDs(messages []Message) []int {
	ids := make([]int, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

func TestExportMessagesFiltersAndEdges(t *testing.T) {
	db := exportFixture(t)
	for _, tc := range []struct {
		name    string
		query   ExportQuery
		want    []int
		last    bool
		deleted bool
	}{
		{"default hides deleted", ExportQuery{ChatID: 42, Limit: 10}, []int{1, 2, 4, 5, 6, 7}, true, false},
		{"include deleted", ExportQuery{ChatID: 42, IncludeDeleted: true, Limit: 10}, []int{1, 2, 3, 4, 5, 6, 7}, true, true},
		{"inclusive date range", ExportQuery{ChatID: 42, Since: time.Unix(103, 0), Until: time.Unix(105, 0), IncludeDeleted: true, Limit: 10}, []int{3, 4, 5}, true, true},
		{"date range hides deleted", ExportQuery{ChatID: 42, Since: time.Unix(103, 0), Until: time.Unix(105, 0), Limit: 10}, []int{4, 5}, true, false},
		{"since boundary", ExportQuery{ChatID: 42, Since: time.Unix(106, 0), Limit: 10}, []int{6, 7}, true, false},
		{"until boundary", ExportQuery{ChatID: 42, Until: time.Unix(102, 0), Limit: 10}, []int{1, 2}, true, false},
		{"cursor with more", ExportQuery{ChatID: 42, After: 4, IncludeDeleted: true, Limit: 2}, []int{5, 6}, false, false},
		{"cursor at last page", ExportQuery{ChatID: 42, After: 5, IncludeDeleted: true, Limit: 2}, []int{6, 7}, true, false},
		{"after final id", ExportQuery{ChatID: 42, After: 7, Limit: 10}, nil, true, false},
		{"empty chat", ExportQuery{ChatID: 99, Limit: 10}, nil, true, false},
		{"one message", ExportQuery{ChatID: 88, Limit: 1}, []int{9}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := db.ExportMessages(context.Background(), tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := exportIDs(page.Messages); !slices.Equal(got, tc.want) || page.Last != tc.last {
				t.Fatalf("ids=%v last=%v, want %v last=%v", got, page.Last, tc.want, tc.last)
			}
			foundDeleted := false
			for _, message := range page.Messages {
				if message.ChatID != tc.query.ChatID {
					t.Fatalf("other chat in page: %+v", message)
				}
				if message.DeletedAt != nil {
					if message.ID != 3 {
						t.Fatalf("unexpected deletion marker: %+v", message)
					}
					foundDeleted = true
				}
			}
			if foundDeleted != tc.deleted {
				t.Fatalf("deleted marker found=%v want=%v", foundDeleted, tc.deleted)
			}
		})
	}
}

func TestExportMessagesPageBoundaries(t *testing.T) {
	for _, limit := range []int{1, 2, 7} {
		t.Run("limit="+strconv.Itoa(limit), func(t *testing.T) {
			db := exportFixture(t)
			q := ExportQuery{ChatID: 42, IncludeDeleted: true, Limit: limit}
			var got []int
			for {
				page, err := db.ExportMessages(context.Background(), q)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, exportIDs(page.Messages)...)
				if page.Last {
					break
				}
				if len(page.Messages) == 0 {
					t.Fatal("non-final page is empty")
				}
				q.After = page.Messages[len(page.Messages)-1].ID
			}
			if !slices.Equal(got, []int{1, 2, 3, 4, 5, 6, 7}) {
				t.Fatalf("limit=%d ids=%v", limit, got)
			}
		})
	}
}

func TestExportMessagesRejectsInvalidQueryAndCancellation(t *testing.T) {
	db := exportFixture(t)
	for _, tc := range []struct {
		name  string
		query ExportQuery
	}{
		{"zero limit", ExportQuery{ChatID: 42}},
		{"negative limit", ExportQuery{ChatID: 42, Limit: -1}},
		{"too large limit", ExportQuery{ChatID: 42, Limit: MaxExportPageSize + 1}},
		{"negative cursor", ExportQuery{ChatID: 42, After: -1, Limit: 1}},
		{"reversed dates", ExportQuery{ChatID: 42, Since: time.Unix(105, 0), Until: time.Unix(104, 0), Limit: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.ExportMessages(context.Background(), tc.query); err == nil {
				t.Fatal("invalid query was accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ExportMessages(ctx, ExportQuery{ChatID: 42, Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
}

func TestExportMessagesThreeThousandRows(t *testing.T) {
	db := openTestDB(t)
	const total = 3000
	messages := make([]Message, total)
	for i := range messages {
		id := total - i
		messages[i] = Message{ChatID: 42, ID: id, Date: int64(100 + id), Raw: raw(`{}`)}
	}
	if err := db.UpsertMessages(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	q := ExportQuery{ChatID: 42, Limit: MaxExportPageSize}
	count := 0
	for {
		page, err := db.ExportMessages(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Messages {
			count++
			if message.ID != count {
				t.Fatalf("message %d has id %d", count, message.ID)
			}
		}
		if page.Last {
			break
		}
		if len(page.Messages) != MaxExportPageSize {
			t.Fatalf("short non-final page: %d", len(page.Messages))
		}
		q.After = page.Messages[len(page.Messages)-1].ID
	}
	if count != total {
		t.Fatalf("exported %d messages, want %d", count, total)
	}
}
