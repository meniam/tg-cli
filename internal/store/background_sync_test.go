package store

import (
	"context"
	"slices"
	"testing"
)

// backgroundPickFixture stores chats that cover every group and ordering rule:
//
//	group 0 (user):    1 (last message 300), 2 (last 100), 3 (no messages), 4 (last 100, ties with 2)
//	group 1 (folders): 10 pinned and 11 included (channels), 12 only excluded in a folder, 13 bot pinned
//	group 2 (other):   12, 14 (last 500), 15 (last 200), 16 (no messages), 17 bot (last 900)
//	not returned:      5 user complete, 18 channel complete
func backgroundPickFixture(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	chats := []Chat{
		{ID: 1, Type: "user", Title: "Ann"}, {ID: 2, Type: "user", Title: "Bob"},
		{ID: 3, Type: "user", Title: "Cat"}, {ID: 4, Type: "user", Title: "Dan"},
		{ID: 5, Type: "user", Title: "Done user"},
		{ID: 10, Type: "channel", Title: "Pinned"}, {ID: 11, Type: "supergroup", Title: "Included"},
		{ID: 12, Type: "group", Title: "Excluded in folder"}, {ID: 13, Type: "bot", Title: "Pinned bot"},
		{ID: 14, Type: "channel", Title: "Fresh"}, {ID: 15, Type: "group", Title: "Older"},
		{ID: 16, Type: "channel", Title: "Silent"}, {ID: 17, Type: "bot", Title: "Bot"},
		{ID: 18, Type: "channel", Title: "Done channel"},
	}
	if err := db.UpsertChats(ctx, chats); err != nil {
		t.Fatal(err)
	}
	last := map[int64]int{1: 300, 2: 100, 4: 100, 10: 50, 11: 60, 12: 10, 13: 70, 14: 500, 15: 200, 17: 900}
	for chatID, date := range last {
		// The older message is saved too: only the latest date orders the chat.
		for _, d := range []int{date / 2, date} {
			schemaExec(t, db.write, `INSERT INTO messages(chat_id,id,date,text,raw,search_text,first_seen_at,last_seen_at) VALUES(?,?,?,'t','{}','t',1,1)`, chatID, d, d)
		}
	}
	// Chat 4 starts a partial sync, chat 5 and 18 are complete.
	schemaExec(t, db.write, `INSERT INTO sync_state(chat_id,complete) VALUES(4,0),(5,1),(18,1)`)
	schemaExec(t, db.write, `INSERT INTO folders(id,title,first_seen_at,last_seen_at) VALUES(1,'f',1,1)`)
	schemaExec(t, db.write, `INSERT INTO folder_chats(folder_id,chat_id,position,kind) VALUES
		(1,10,0,'pinned'),(1,11,1,'include'),(1,12,2,'exclude'),(1,13,3,'pinned'),(1,1,4,'include')`)
}

func backgroundPickIDs(chats []BackgroundSyncChat) []int64 {
	ids := make([]int64, 0, len(chats))
	for _, chat := range chats {
		ids = append(ids, chat.ID)
	}
	return ids
}

// TestBackgroundPickOrder covers the groups and the order inside them. The bot 13
// is in a folder, so it joins the folder group; the user 1 is in a folder too but
// stays in the people group; a folder `exclude` entry does not make a folder chat.
func TestBackgroundPickOrder(t *testing.T) {
	db := openTestDB(t)
	backgroundPickFixture(t, db)

	got, err := db.ChatsForBackgroundSync(context.Background(), ChatFilter{}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{
		1, 2, 4, 3, // people: newest first, equal dates by id, no messages last
		13, 11, 10, // folders: last message dates 70, 60, 50
		17, 14, 15, 12, 16, // others: 900, 500, 200, 10, none
	}
	if !slices.Equal(backgroundPickIDs(got), want) {
		t.Fatalf("order = %v, want %v", backgroundPickIDs(got), want)
	}
	groups := map[int64]int{}
	for _, chat := range got {
		groups[chat.ID] = chat.Group
	}
	for id, group := range map[int64]int{1: 0, 3: 0, 10: 1, 13: 1, 12: 2, 14: 2, 17: 2} {
		if groups[id] != group {
			t.Errorf("chat %d group = %d, want %d", id, groups[id], group)
		}
	}
	if got[0].Type != "user" || got[0].Title != "Ann" {
		t.Errorf("fields are not filled: %+v", got[0])
	}
}

func TestBackgroundPickCompleteChatIsSkipped(t *testing.T) {
	db := openTestDB(t)
	backgroundPickFixture(t, db)
	got, err := db.ChatsForBackgroundSync(context.Background(), ChatFilter{}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range backgroundPickIDs(got) {
		if id == 5 || id == 18 {
			t.Fatalf("a complete chat was returned: %d", id)
		}
	}
	// A chat that was complete but is reset returns to the list.
	if err := db.ResetSyncState(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	got, err = db.ChatsForBackgroundSync(context.Background(), ChatFilter{}, nil, 100)
	if err != nil || !slices.Contains(backgroundPickIDs(got), 5) {
		t.Fatalf("reset chat is missing: %v err=%v", backgroundPickIDs(got), err)
	}
}

func TestBackgroundPickAccessFilter(t *testing.T) {
	db := openTestDB(t)
	backgroundPickFixture(t, db)
	ctx := context.Background()

	got, err := db.ChatsForBackgroundSync(ctx, ChatFilter{Block: []int64{1, 17}}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range backgroundPickIDs(got) {
		if id == 1 || id == 17 {
			t.Fatalf("a blocked chat was returned: %d", id)
		}
	}
	got, err = db.ChatsForBackgroundSync(ctx, ChatFilter{Allow: []int64{3, 14, 5}}, nil, 100)
	if err != nil || !slices.Equal(backgroundPickIDs(got), []int64{3, 14}) {
		t.Fatalf("allow list: %v err=%v", backgroundPickIDs(got), err)
	}
	got, err = db.ChatsForBackgroundSync(ctx, ChatFilter{Hide: true}, nil, 100)
	if err != nil || len(got) != 0 {
		t.Fatalf("hide: %v err=%v", backgroundPickIDs(got), err)
	}
}

func TestBackgroundPickExclude(t *testing.T) {
	db := openTestDB(t)
	backgroundPickFixture(t, db)
	got, err := db.ChatsForBackgroundSync(context.Background(), ChatFilter{}, []int64{1, 13, 14}, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{2, 4, 3, 11, 10, 17, 15, 12, 16}
	if !slices.Equal(backgroundPickIDs(got), want) {
		t.Fatalf("order = %v, want %v", backgroundPickIDs(got), want)
	}
	// Excluding every candidate leaves an empty, non-nil list.
	all := backgroundPickIDs(mustPick(t, db, ChatFilter{}, nil, 100))
	got, err = db.ChatsForBackgroundSync(context.Background(), ChatFilter{}, all, 100)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("exclude all: %v err=%v", got, err)
	}
}

func TestBackgroundPickLimit(t *testing.T) {
	db := openTestDB(t)
	backgroundPickFixture(t, db)
	if got := backgroundPickIDs(mustPick(t, db, ChatFilter{}, nil, 1)); !slices.Equal(got, []int64{1}) {
		t.Fatalf("limit 1: %v", got)
	}
	if got := backgroundPickIDs(mustPick(t, db, ChatFilter{}, nil, 5)); !slices.Equal(got, []int64{1, 2, 4, 3, 13}) {
		t.Fatalf("limit 5 crosses a group: %v", got)
	}
	// Chats from the first group are taken before the limit cuts the list, and a
	// non-positive limit returns nothing.
	for _, limit := range []int{0, -1} {
		if got := mustPick(t, db, ChatFilter{}, nil, limit); got == nil || len(got) != 0 {
			t.Fatalf("limit %d: %v", limit, got)
		}
	}
}

func mustPick(t *testing.T, db *DB, filter ChatFilter, exclude []int64, limit int) []BackgroundSyncChat {
	t.Helper()
	got, err := db.ChatsForBackgroundSync(context.Background(), filter, exclude, limit)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
