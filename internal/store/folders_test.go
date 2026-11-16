package store

import (
	"context"
	"testing"

	"tg-cli/internal/rpc"
)

// TestFolderListPinsFolderChats pins the composition of folders: explicit pinned,
// include and exclude peers become rows of folder_chats with their kind, a folder
// of a shared chat list keeps only pinned and include, and a chat the access lists
// close never becomes a row.
func TestFolderListPinsFolderChats(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	folders := []rpc.Folder{
		{
			ID: 2, Kind: "filter", Title: "Work", Position: 0,
			Rules:   &rpc.FolderRules{Contacts: true, ExcludeMuted: true},
			Pinned:  []rpc.FolderPeer{{ID: 1}, {ID: 2}},
			Include: []rpc.FolderPeer{{ID: 1}, {ID: 3}},
			Exclude: []rpc.FolderPeer{{ID: 4}},
		},
		{
			ID: 3, Kind: "chatlist", Title: "Shared", Position: 1,
			Pinned:  []rpc.FolderPeer{{ID: 5}},
			Include: []rpc.FolderPeer{{ID: 6}},
		},
		{Position: 2, Kind: "all"},
	}
	// Chat 4 is closed by the access lists, so it must not reach the table.
	if err := db.ReplaceFolders(ctx, folders, ChatFilter{Block: []int64{4}}); err != nil {
		t.Fatal(err)
	}

	type row struct {
		folderID int64
		chatID   int64
		position int
		kind     string
	}
	read := func() []row {
		t.Helper()
		rows, err := db.read.Query(`SELECT folder_id, chat_id, position, kind FROM folder_chats ORDER BY folder_id, kind, position`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.folderID, &r.chatID, &r.position, &r.kind); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	got := read()
	// Chat 4 is closed, so it is absent even from exclude. Chat 1 is in both pinned
	// and include: the pinned row is written first and the include row is dropped.
	want := []row{
		{2, 3, 1, "include"},
		{2, 1, 0, "pinned"},
		{2, 2, 1, "pinned"},
		{3, 6, 0, "include"},
		{3, 5, 0, "pinned"},
	}
	if len(got) != len(want) {
		t.Fatalf("folder_chats = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// The rules of a folder stay with the folder, not with its chats.
	var rules string
	if err := db.read.QueryRow(`SELECT rules FROM folders WHERE id=2`).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules == "" {
		t.Error("the rules of folder 2 are lost")
	}

	// A chat in both pinned and include keeps one row: the primary key refuses
	// the second insert, so pinned wins.
	both := []rpc.Folder{{ID: 7, Kind: "filter", Title: "Both", Pinned: []rpc.FolderPeer{{ID: 9}}, Include: []rpc.FolderPeer{{ID: 9}}}}
	if err := db.ReplaceFolders(ctx, both, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := db.read.QueryRow(`SELECT kind FROM folder_chats WHERE folder_id=7 AND chat_id=9`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "pinned" {
		t.Errorf("kind = %q, want pinned", kind)
	}
}

// TestFoldersReadComposition covers the read side: the saved rows come back as a
// folder, a closed chat is invisible even in a folder, and the title of a peer
// comes from the stored chat.
func TestFoldersReadComposition(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{
		{ID: 1, Type: "group", Title: "Kept"},
		{ID: 2, Type: "group", Title: "Closed"},
	}); err != nil {
		t.Fatal(err)
	}
	folders := []rpc.Folder{{ID: 2, Kind: "filter", Title: "Work", Pinned: []rpc.FolderPeer{{ID: 1}, {ID: 2}}}}
	if err := db.ReplaceFolders(ctx, folders, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Folders(ctx, ChatFilter{Block: []int64{2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Folders) != 1 || len(got.Folders[0].Pinned) != 1 || got.Folders[0].Pinned[0].ID != 1 || got.Folders[0].Pinned[0].Title != "Kept" {
		t.Fatalf("folders = %+v", got.Folders)
	}
	if got.SyncedAt == nil {
		t.Error("the snapshot timestamp of folders is missing")
	}
}
