package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestContactDeletedColumn(t *testing.T) {
	d := &daemon{}
	for _, deleted := range []bool{false, true} {
		input := rpc.Contact{ID: 1, Deleted: deleted}
		person := d.personFromContact(context.Background(), input)
		if person.Deleted == nil || *person.Deleted != deleted || len(person.Raw) != 0 {
			t.Fatalf("person = %+v", person)
		}
		person.Raw = json.RawMessage(`{"deleted":true}`)
		output := contactFromPerson(person)
		if output.Deleted != deleted {
			t.Fatalf("deleted = %v, want %v", output.Deleted, deleted)
		}
	}
	if got := contactFromPerson(store.Person{Raw: json.RawMessage(`{"deleted":true}`)}); got.Deleted {
		t.Fatal("contact read deleted from raw")
	}
}
