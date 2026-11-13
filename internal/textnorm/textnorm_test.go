package textnorm

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ input, want string }{
		{"ПрИвЕт HELLO", "привет hello"},
		{"ёлка ЁЛКА ЕЛКА", "елка елка елка"},
		{"  Latin\tКириллица\n42  ", "latin кириллица 42"},
		{"Cafe\u0301 ☕\u00a0Привет", "café ☕ привет"},
		{"", ""},
		{" \t\n\u2003 ", ""},
	}
	for _, tc := range cases {
		if got := Normalize(tc.input); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.input, got, tc.want)
		}
		if got := Normalize(Normalize(tc.input)); got != Normalize(tc.input) {
			t.Errorf("Normalize is not idempotent for %q: %q", tc.input, got)
		}
	}
}

func TestQueryNeutralizesSyntax(t *testing.T) {
	cases := []struct {
		input  string
		prefix bool
		want   string
	}{
		{`"`, false, ""},
		{`a OR b`, false, `"a" "or" "b"`},
		{`NEAR(a b)`, false, `"near" "a" "b"`},
		{`title:secret`, false, `"title" "secret"`},
		{`-x`, false, `"x"`},
		{`a*`, false, `"a"`},
		{`(a)`, false, `"a"`},
		{`"unbalanced`, false, `"unbalanced"`},
		{`a AND b NOT c`, true, `"a" "and" "b" "not" "c"*`},
		{`Ёлка 😀 2026`, true, `"елка" "2026"*`},
		{" \t\n", true, ""},
	}
	for _, tc := range cases {
		if got := Query(tc.input, tc.prefix); got != tc.want {
			t.Errorf("Query(%q, %t) = %q, want %q", tc.input, tc.prefix, got, tc.want)
		}
	}
}

func TestQueryExpressionsRunInFTS5(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE docs USING fts5(body)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO docs(body) VALUES(?)`, Normalize("Ёлка на столе")); err != nil {
		t.Fatal(err)
	}
	inputs := []string{`"`, `a OR b`, `NEAR(a b)`, `title:secret`, `-x`, `a*`, `(a)`, `"unbalanced`, `a AND b NOT c`, `ЁЛКА`, `елка*`, `😀`}
	for _, input := range inputs {
		for _, prefix := range []bool{false, true} {
			query := Query(input, prefix)
			if query == "" {
				continue
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM docs WHERE docs MATCH ?`, query).Scan(&count); err != nil {
				t.Errorf("MATCH %q from %q: %v", query, input, err)
			}
			if strings.EqualFold(input, "ЁЛКА") && count != 1 {
				t.Errorf("Cyrillic MATCH %q count = %d, want 1", query, count)
			}
		}
	}
}
