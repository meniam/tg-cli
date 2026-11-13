package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func validChatNote() Note {
	return Note{Kind: "chat", ChatID: intPtr(1), Body: "A useful note", Author: "owner"}
}

func TestAddNoteValidation(t *testing.T) {
	base := validChatNote()
	cases := []struct {
		name  string
		note  Note
		valid bool
	}{
		{"chat", base, true},
		{"person", Note{Kind: "person", TargetID: intPtr(2), Body: "ok", Author: "owner"}, true},
		{"message", Note{Kind: "message", ChatID: intPtr(1), TargetID: intPtr(3), Body: "ok", Author: "owner"}, true},
		{"ai model", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "ai", Model: stringPtr("model-x")}, true},
		{"body at limit", Note{Kind: "chat", ChatID: intPtr(1), Body: strings.Repeat("x", 16*1024), Author: "owner"}, true},
		{"twenty tags", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "owner", Tags: makeTags(20)}, true},
		{"tag at limit", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "owner", Tags: []string{strings.Repeat("ё", 64)}}, true},
		{"unknown kind", Note{Kind: "folder", ChatID: intPtr(1), Body: "ok", Author: "owner"}, false},
		{"chat missing id", Note{Kind: "chat", Body: "ok", Author: "owner"}, false},
		{"chat extra target", Note{Kind: "chat", ChatID: intPtr(1), TargetID: intPtr(2), Body: "ok", Author: "owner"}, false},
		{"person missing id", Note{Kind: "person", Body: "ok", Author: "owner"}, false},
		{"person extra chat", Note{Kind: "person", ChatID: intPtr(1), TargetID: intPtr(2), Body: "ok", Author: "owner"}, false},
		{"message missing chat", Note{Kind: "message", TargetID: intPtr(2), Body: "ok", Author: "owner"}, false},
		{"message missing target", Note{Kind: "message", ChatID: intPtr(1), Body: "ok", Author: "owner"}, false},
		{"blank body", Note{Kind: "chat", ChatID: intPtr(1), Body: " \t ", Author: "owner"}, false},
		{"oversized body", Note{Kind: "chat", ChatID: intPtr(1), Body: strings.Repeat("ё", 8193), Author: "owner"}, false},
		{"unknown author", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "bot"}, false},
		{"owner model", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "owner", Model: stringPtr("model-x")}, false},
		{"too many tags", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "owner", Tags: makeTags(21)}, false},
		{"blank tag", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "owner", Tags: []string{"  "}}, false},
		{"oversized tag", Note{Kind: "chat", ChatID: intPtr(1), Body: "ok", Author: "owner", Tags: []string{strings.Repeat("ё", 65)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			setClock(db, 123)
			got, err := db.AddNote(context.Background(), tc.note)
			if tc.valid {
				if err != nil || got.ID == 0 || got.CreatedAt != 123 || got.UpdatedAt != 123 {
					t.Fatalf("AddNote = %+v, %v", got, err)
				}
				if n := schemaCount(t, db.read, `SELECT count(*) FROM notes`); n != 1 {
					t.Errorf("stored rows = %d, want 1", n)
				}
			} else {
				if !errors.Is(err, ErrInvalidNote) {
					t.Fatalf("error = %v, want ErrInvalidNote", err)
				}
				if n := schemaCount(t, db.read, `SELECT count(*) FROM notes`); n != 0 {
					t.Errorf("invalid note wrote %d rows", n)
				}
			}
		})
	}
}

func makeTags(count int) []string {
	tags := make([]string, count)
	for i := range tags {
		tags[i] = string(rune('a' + i))
	}
	return tags
}

func TestNoteTagsGetListAndDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	input := Note{Kind: "chat", ChatID: intPtr(1), Body: "  Памятка  ", Author: "owner", Tags: []string{" ЁЛКА ", "елка", "  Work   Task  "}}
	first, err := db.AddNote(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Body != "Памятка" || len(first.Tags) != 2 || first.Tags[0] != "work task" || first.Tags[1] != "елка" {
		t.Fatalf("stored note = %+v", first)
	}
	got, err := db.GetNote(ctx, first.ID)
	if err != nil || got.Body != first.Body || len(got.Tags) != 2 || got.Tags[1] != "елка" {
		t.Fatalf("GetNote = %+v, %v", got, err)
	}
	if n := ftsCount(t, db.read, "notes_fts", "ЕЛКА"); n != 1 {
		t.Errorf("tag FTS matches = %d, want 1", n)
	}
	setClock(db, 200)
	person, err := db.AddNote(ctx, Note{Kind: "person", TargetID: intPtr(2), Body: "person", Author: "ai", Model: stringPtr("model-x"), Tags: []string{"work task"}})
	if err != nil {
		t.Fatal(err)
	}
	setClock(db, 300)
	message, err := db.AddNote(ctx, Note{Kind: "message", ChatID: intPtr(1), TargetID: intPtr(3), Body: "message", Author: "owner", Tags: []string{"other"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		filter NoteFilter
		want   []int64
	}{
		{"all", NoteFilter{}, []int64{message.ID, person.ID, first.ID}},
		{"kind", NoteFilter{Kind: "person"}, []int64{person.ID}},
		{"chat", NoteFilter{ChatID: intPtr(1)}, []int64{message.ID, first.ID}},
		{"target", NoteFilter{TargetID: intPtr(3)}, []int64{message.ID}},
		{"author", NoteFilter{Author: "owner"}, []int64{message.ID, first.ID}},
		{"tag", NoteFilter{Tag: " ЁЛКА "}, []int64{first.ID}},
		{"page", NoteFilter{BeforeID: message.ID, Limit: 1}, []int64{person.ID}},
		{"limit", NoteFilter{Limit: 1}, []int64{message.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notes, err := db.ListNotes(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(notes) != len(tc.want) {
				t.Fatalf("notes = %+v, want IDs %v", notes, tc.want)
			}
			for i, note := range notes {
				if note.ID != tc.want[i] {
					t.Errorf("note[%d].ID = %d, want %d", i, note.ID, tc.want[i])
				}
			}
		})
	}
	if err := db.DeleteNote(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetNote(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted note error = %v", err)
	}
	if err := db.DeleteNote(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second deletion error = %v", err)
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM note_tags WHERE note_id=?`, first.ID); n != 0 {
		t.Errorf("deleted note retained %d tags", n)
	}
	if n := ftsCount(t, db.read, "notes_fts", "елка"); n != 0 {
		t.Errorf("deleted note retained %d FTS matches", n)
	}
	checkFTSIntegrity(t, db.write)
}

func TestNoteListLimitsAndDeleteByAuthor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for i := 0; i < 510; i++ {
		author := "owner"
		if i%2 == 0 {
			author = "ai"
		}
		if _, err := db.AddNote(ctx, Note{Kind: "chat", ChatID: intPtr(1), Body: "Bulk Ёлка", Author: author, Tags: []string{"keep"}}); err != nil {
			t.Fatal(err)
		}
	}
	if notes, err := db.ListNotes(ctx, NoteFilter{}); err != nil || len(notes) != 50 {
		t.Fatalf("default limit: %d, %v", len(notes), err)
	}
	if notes, err := db.ListNotes(ctx, NoteFilter{Limit: 1000}); err != nil || len(notes) != 500 {
		t.Fatalf("maximum limit: %d, %v", len(notes), err)
	}
	count, err := db.DeleteNotesByAuthor(ctx, "ai")
	if err != nil || count != 255 {
		t.Fatalf("deleted AI notes = %d, %v", count, err)
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM notes WHERE author='owner'`); n != 255 {
		t.Errorf("owner notes remaining = %d", n)
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM note_tags`); n != 255 {
		t.Errorf("tags remaining = %d", n)
	}
	if n := ftsCount(t, db.read, "notes_fts", "ЕЛКА"); n != 255 {
		t.Errorf("owner FTS matches = %d", n)
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM notes WHERE author='ai'`); n != 0 {
		t.Errorf("AI notes remaining = %d", n)
	}
	if count, err := db.DeleteNotesByAuthor(ctx, "ai"); err != nil || count != 0 {
		t.Errorf("second author delete = %d, %v", count, err)
	}
	if _, err := db.DeleteNotesByAuthor(ctx, "unknown"); !errors.Is(err, ErrInvalidNote) {
		t.Errorf("invalid author error = %v", err)
	}
	checkFTSIntegrity(t, db.write)
}
