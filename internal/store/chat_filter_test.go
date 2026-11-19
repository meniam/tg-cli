package store

import (
	"context"
	"testing"
)

func TestChatFilterHidesChatOwnedRowsBeforeLimit(t *testing.T) {
	db := searchFixture(t)
	ctx := context.Background()
	if _, err := db.AddNote(ctx, Note{Kind: "person", TargetID: intPtr(7), Body: "Елка person note", Author: "owner"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		filter      ChatFilter
		visibleChat int64
	}{
		{"allow", ChatFilter{Allow: []int64{2}}, 2},
		{"block", ChatFilter{Block: []int64{1}}, 2},
		{"block wins", ChatFilter{Allow: []int64{1, 2}, Block: []int64{1}}, 2},
		{"hide", ChatFilter{Hide: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := db.Search(ctx, SearchOptions{Query: "елка", ChatFilter: tc.filter})
			if err != nil {
				t.Fatal(err)
			}
			var people, personNotes int
			for _, hit := range hits {
				if hit.Scope == "people" {
					people++
				}
				if hit.Scope == "notes" && hit.ChatID == nil {
					personNotes++
				}
				id := int64(0)
				if hit.Scope == "chats" {
					id = hit.ID
				}
				if hit.ChatID != nil {
					id = *hit.ChatID
				}
				if id != 0 && id != tc.visibleChat {
					t.Errorf("hidden chat leaked: %+v", hit)
				}
			}
			wantPeople := 0
			if tc.name == "block" {
				wantPeople = 1
			}
			if people != wantPeople || personNotes != 1 {
				t.Errorf("people=%d personNotes=%d hits=%+v", people, personNotes, hits)
			}
			limited, err := db.Search(ctx, SearchOptions{Query: "елка", Scopes: []string{"messages"}, Limit: 1, ChatFilter: tc.filter})
			if err != nil {
				t.Fatal(err)
			}
			if tc.visibleChat != 0 && (len(limited) != 1 || limited[0].ChatID == nil || *limited[0].ChatID != tc.visibleChat) {
				t.Errorf("filtered page = %+v", limited)
			}
			if tc.visibleChat == 0 && len(limited) != 0 {
				t.Errorf("a hidden filter returned a page of messages: %+v", limited)
			}
			notes, err := db.ListNotes(ctx, NoteFilter{ChatFilter: tc.filter})
			if err != nil {
				t.Fatal(err)
			}
			var foundPerson bool
			for _, note := range notes {
				if note.ChatID == nil {
					foundPerson = true
					continue
				}
				if *note.ChatID != tc.visibleChat {
					t.Errorf("hidden note leaked: %+v", note)
				}
			}
			if !foundPerson {
				t.Error("person note was hidden")
			}
		})
	}
}
