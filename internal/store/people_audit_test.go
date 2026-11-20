package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func audit08Contacts(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	people := []Person{
		{ID: 5, FirstName: stringPtr("bob"), LastName: stringPtr("Zed")},
		{ID: 2, FirstName: stringPtr("Bob"), LastName: stringPtr("alpha")},
		{ID: 4, FirstName: stringPtr("Anna")},
		{ID: 3, FirstName: stringPtr("Bob"), LastName: stringPtr("Alpha")},
		{ID: 1, FirstName: stringPtr("Carl")},
	}
	if err := db.ReplaceContacts(context.Background(), people, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func audit08IDs(people []Person) []int64 {
	out := []int64{}
	for _, p := range people {
		out = append(out, p.ID)
	}
	return out
}

func TestAudit08_SetContactUnknownPerson(t *testing.T) {
	db := openTestDB(t)
	if err := db.SetContact(context.Background(), 99, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetContact of unknown person: %v, want ErrNotFound", err)
	}
}

func TestAudit08_ListContactsSortAndPaging(t *testing.T) {
	db := audit08Contacts(t)
	ctx := context.Background()
	// First name, then last name (both case-insensitive), then ID.
	all, more, err := db.ListContacts(ctx, 10, 0, ChatFilter{})
	if err != nil || more {
		t.Fatalf("all: more=%v err=%v", more, err)
	}
	if got, want := audit08IDs(all), []int64{4, 2, 3, 5, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v, want %v", got, want)
	}
	for _, tc := range []struct {
		name          string
		limit, offset int
		want          []int64
		more          bool
	}{
		{"first page", 2, 0, []int64{4, 2}, true},
		{"middle page", 2, 2, []int64{3, 5}, true},
		{"last partial page", 2, 4, []int64{1}, false},
		{"exact end", 3, 2, []int64{3, 5, 1}, false},
		{"offset past the end", 2, 5, []int64{}, false},
		{"offset far past the end", 2, 50, []int64{}, false},
	} {
		page, more, err := db.ListContacts(ctx, tc.limit, tc.offset, ChatFilter{})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := audit08IDs(page); !reflect.DeepEqual(got, tc.want) || more != tc.more {
			t.Errorf("%s: ids=%v more=%v, want %v more=%v", tc.name, got, more, tc.want, tc.more)
		}
	}
	filtered, more, err := db.ListContacts(ctx, 10, 0, ChatFilter{Block: []int64{2, 4}})
	if err != nil || more || !reflect.DeepEqual(audit08IDs(filtered), []int64{3, 5, 1}) {
		t.Errorf("filtered ids=%v more=%v err=%v", audit08IDs(filtered), more, err)
	}
}

func TestAudit08_ListContactsRejectsBadBounds(t *testing.T) {
	db := audit08Contacts(t)
	for _, tc := range []struct {
		name          string
		limit, offset int
	}{
		{"zero limit", 0, 0}, {"negative limit", -1, 0}, {"limit above 1000", 1001, 0}, {"negative offset", 10, -1},
	} {
		if _, _, err := db.ListContacts(context.Background(), tc.limit, tc.offset, ChatFilter{}); !errors.Is(err, ErrInvalidSearchOptions) {
			t.Errorf("%s: %v, want ErrInvalidSearchOptions", tc.name, err)
		}
	}
	if _, _, err := db.ListContacts(context.Background(), 1000, 0, ChatFilter{}); err != nil {
		t.Errorf("limit 1000 must be accepted: %v", err)
	}
}

func TestAudit08_SearchContactsErrorsAndLimit(t *testing.T) {
	db := audit08Contacts(t)
	ctx := context.Background()
	for _, query := range []string{"", "   ", `"`, "!!!"} {
		if _, err := db.SearchContacts(ctx, query, 10, ChatFilter{}); !errors.Is(err, ErrEmptyQuery) {
			t.Errorf("query %q: %v, want ErrEmptyQuery", query, err)
		}
	}
	for _, limit := range []int{0, -1, 101} {
		if _, err := db.SearchContacts(ctx, "bob", limit, ChatFilter{}); !errors.Is(err, ErrInvalidSearchOptions) {
			t.Errorf("limit %d: %v, want ErrInvalidSearchOptions", limit, err)
		}
	}
	found, err := db.SearchContacts(ctx, "bob", 100, ChatFilter{})
	if err != nil || len(found) != 3 {
		t.Fatalf("bob: %v %v, want 3 contacts", audit08IDs(found), err)
	}
	one, err := db.SearchContacts(ctx, "bob", 1, ChatFilter{})
	if err != nil || len(one) != 1 {
		t.Fatalf("limit 1: %v %v", audit08IDs(one), err)
	}
}
