package store

import (
	"context"
	"errors"
	"testing"
)

func TestFindByUsername(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: -10, Type: "group", Title: "Team", Username: stringPtr("TeamNews")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{{ID: 42, Username: stringPtr("DurovUser")}}); err != nil {
		t.Fatal(err)
	}
	chat, err := db.FindChatByUsername(ctx, "teamnews")
	if err != nil || chat.ID != -10 {
		t.Fatalf("chat=%+v err=%v", chat, err)
	}
	person, err := db.FindPersonByUsername(ctx, "DUROVUSER")
	if err != nil || person.ID != 42 {
		t.Fatalf("person=%+v err=%v", person, err)
	}
	for _, fn := range []func() error{func() error { _, err := db.FindChatByUsername(ctx, "unknown"); return err }, func() error { _, err := db.FindPersonByUsername(ctx, "unknown"); return err }} {
		if err := fn(); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing username error=%v", err)
		}
	}
}
