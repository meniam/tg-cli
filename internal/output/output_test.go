package output

import (
	"bytes"
	"testing"
)

func TestSuccessShape(t *testing.T) {
	var b bytes.Buffer
	if err := Success(&b, map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `{"ok":true,"data":{"n":1}}`+"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSuccessNilDataIsNull(t *testing.T) {
	var b bytes.Buffer
	if err := Success(&b, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `{"ok":true,"data":null}`+"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFailureShape(t *testing.T) {
	var b bytes.Buffer
	if err := Failure(&b, "not_authorized", "log in first <now>"); err != nil {
		t.Fatal(err)
	}
	want := `{"ok":false,"error":{"code":"not_authorized","message":"log in first <now>"}}` + "\n"
	if got := b.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLines(t *testing.T) {
	type item struct {
		ID   int    `json:"id"`
		Text string `json:"text"`
	}
	var b bytes.Buffer
	if err := Lines(&b, []item{{1, "a <b>"}, {2, "c"}}, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `{"id":1,"text":"a <b>"}`+"\n"+`{"id":2,"text":"c"}`+"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	b.Reset()
	if err := Lines(&b, []item{{1, "a"}}, "CURSOR"); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `{"id":1,"text":"a"}`+"\n"+`{"next_cursor":"CURSOR"}`+"\n"; got != want {
		t.Errorf("with a cursor: got %q, want %q", got, want)
	}

	b.Reset()
	if err := Lines[item](&b, nil, ""); err != nil || b.Len() != 0 {
		t.Errorf("an empty list prints nothing: %q, %v", b.String(), err)
	}
}
