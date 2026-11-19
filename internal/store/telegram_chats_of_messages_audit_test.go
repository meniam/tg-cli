package store

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestAudit08_ChatsOfMessages(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	const channel = int64(-1000000000001)
	msgs := []Message{
		{ChatID: 10, ID: 1, Date: 1, Text: "a", Raw: raw(`{}`)},
		{ChatID: 11, ID: 1, Date: 1, Text: "b", Raw: raw(`{}`)},
		{ChatID: 10, ID: 2, Date: 2, Text: "c", Raw: raw(`{}`)},
		{ChatID: -5, ID: 3, Date: 3, Text: "group", Raw: raw(`{}`)},
		{ChatID: channel, ID: 4, Date: 4, Text: "channel", Raw: raw(`{}`)},
		{ChatID: 12, ID: 9, Date: 5, Text: "other", Raw: raw(`{}`)},
	}
	if err := db.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ids  []int
		want []int64
	}{
		{"empty list", nil, nil},
		{"unknown id", []int{777}, nil},
		{"id shared by two chats", []int{1}, []int64{10, 11}},
		{"distinct chats", []int{1, 2}, []int64{10, 11}},
		{"group chat", []int{3}, []int64{-5}},
		{"channel is excluded", []int{4}, nil},
		{"channel mixed with a chat", []int{3, 4}, []int64{-5}},
	} {
		got, err := db.ChatsOfMessages(ctx, tc.ids)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
