package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type statsFake struct {
	*fakeTelegram
	calls int
}

func (f *statsFake) ChatStats(context.Context, chatref.Ref, string) (rpc.ChatStats, error) {
	f.calls++
	return rpc.ChatStats{ChatID: 77, TopPosters: []rpc.StatsPerson{{UserID: 5, Name: "visible"}, {UserID: 6, Name: "blocked"}}}, nil
}
func (f *statsFake) ChatBoosts(context.Context, chatref.Ref) (rpc.ChatBoosts, error) {
	f.calls++
	return rpc.ChatBoosts{ChatID: 77, Level: 2}, nil
}
func TestStatsAccessAndType(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &statsFake{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"block":[6]}`)
	v, err := d.chatStats(context.Background(), params(t, rpc.ChatStatsParams{Chat: "77"}))
	if err != nil || len(v.(rpc.ChatStats).TopPosters) != 1 {
		t.Fatalf("stats: %+v %v", v, err)
	}
	if person, err := db.GetPerson(context.Background(), 5); err != nil || person.FirstName == nil || *person.FirstName != "visible" {
		t.Fatalf("saved person: %+v %v", person, err)
	}
	if _, err := db.GetPerson(context.Background(), 6); err == nil {
		t.Fatal("blocked person was stored")
	}
	if _, err := d.chatBoosts(context.Background(), params(t, rpc.ChatRefParams{Chat: "77"})); err != nil {
		t.Fatal(err)
	}
	base.resolved = &rpc.Chat{ID: 77, Type: "group"}
	if _, err := d.chatStats(context.Background(), params(t, rpc.ChatStatsParams{Chat: "77"})); writeCode(t, err) != rpc.CodeInvalidParams || f.calls != 2 {
		t.Fatalf("group: %v calls=%d", err, f.calls)
	}
	writeAccess(t, path, `{"block":[77]}`)
	if _, err := d.chatBoosts(context.Background(), params(t, rpc.ChatRefParams{Chat: "77"})); writeCode(t, err) != rpc.CodeNotFound || f.calls != 2 {
		t.Fatalf("closed: %v calls=%d", err, f.calls)
	}
}
