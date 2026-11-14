package store

import (
	"context"
	"testing"
)

func TestPersonDisplayNameMerge(t *testing.T) {
	for _, tc := range []struct{ first, last, display, wantFirst, wantLast string }{
		{"Ivan", "Petrov", "Ivan Petrov", "Ivan", "Petrov"}, {"Ivan", "Petrov", "New Name", "New Name", ""}, {"Ivan", "", "Ivan", "Ivan", ""}, {"", "", "First Last", "First Last", ""},
	} {
		old := Person{FirstName: stringPtr(tc.first), LastName: stringPtr(tc.last)}
		got := mergePerson(old, Person{DisplayName: stringPtr(tc.display)}, true, 1)
		if textValue(got.FirstName) != tc.wantFirst || textValue(got.LastName) != tc.wantLast {
			t.Errorf("%+v: %+v", tc, got)
		}
	}
	old := mergePerson(Person{}, Person{DisplayName: stringPtr("Ivan Petrov")}, false, 1)
	got := mergePerson(old, Person{FirstName: stringPtr("Ivan"), LastName: stringPtr("Petrov")}, true, 2)
	if textValue(got.FirstName) != "Ivan" || textValue(got.LastName) != "Petrov" {
		t.Fatal(got)
	}
}
func TestContactsReconcileAndFilter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	p := []Person{{ID: 1, FirstName: stringPtr("Ёлка")}, {ID: 2, FirstName: stringPtr("елка")}, {ID: 3, FirstName: stringPtr("Other")}}
	if err := db.ReplaceContacts(ctx, p, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceContacts(ctx, []Person{p[0], {ID: 4, FirstName: stringPtr("Hidden")}}, ChatFilter{Block: []int64{3, 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetPerson(ctx, 4); err != ErrNotFound {
		t.Fatalf("hidden person persisted: %v", err)
	}
	x, err := db.GetPerson(ctx, 2)
	if err != nil || *x.IsContact {
		t.Fatalf("removed: %+v %v", x, err)
	}
	x, err = db.GetPerson(ctx, 3)
	if err != nil || !*x.IsContact {
		t.Fatalf("hidden modified: %+v %v", x, err)
	}
	for _, fn := range []func() ([]Person, error){func() ([]Person, error) {
		p, _, e := db.ListContacts(ctx, 10, 0, ChatFilter{Block: []int64{3}})
		return p, e
	}, func() ([]Person, error) { return db.SearchContacts(ctx, "ЕЛКА", 10, ChatFilter{Block: []int64{3}}) }, func() ([]Person, error) {
		return db.SearchContacts(ctx, `"ЕЛКА" OR`, 10, ChatFilter{Block: []int64{3}})
	}} {
		items, e := fn()
		if e != nil {
			t.Fatal(e)
		}
		for _, p := range items {
			if p.ID != 1 {
				t.Fatalf("unexpected contact %+v", p)
			}
		}
	}
	hits, err := db.Search(ctx, SearchOptions{Query: "елка", Scopes: []string{"people"}, ChatFilter: ChatFilter{Block: []int64{1}}})
	if err != nil || len(hits) != 1 || hits[0].ID != 2 {
		t.Fatalf("filtered search: %+v %v", hits, err)
	}
	if err = db.SetContact(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	x, err = db.GetPerson(ctx, 1)
	if err != nil || *x.IsContact || textValue(x.FirstName) != "Ёлка" {
		t.Fatalf("set contact %+v %v", x, err)
	}
}
