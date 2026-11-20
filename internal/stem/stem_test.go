package stem

import (
	"strings"
	"testing"

	"tg-cli/internal/textnorm"
)

func TestWordFormsShareOneStem(t *testing.T) {
	groups := [][]string{
		{"доставка", "доставки", "доставку", "доставкой", "доставке"},
		{"оплата", "оплатил", "оплаты", "оплату", "оплатой"},
		{"заказ", "заказа", "заказов", "заказу", "заказом"},
		{"running", "runs", "run"},
		{"delivery", "deliveries"},
		{"payment", "payments"},
		{"Ёлка", "ёлки", "елке"},
	}
	for _, group := range groups {
		want := Word(group[0])
		for _, form := range group[1:] {
			if got := Word(form); got != want {
				t.Errorf("Word(%q) = %q, want %q (the stem of %q)", form, got, want, group[0])
			}
		}
	}
}

func TestWordKnownStems(t *testing.T) {
	cases := map[string]string{
		"доставки": "доставк",
		"оплатил":  "оплат",
		"заказов":  "заказ",
		"running":  "run",
		"хорошо":   "хорош",
		"ёлка":     "елк",
	}
	for word, want := range cases {
		if got := Word(word); got != want {
			t.Errorf("Word(%q) = %q, want %q", word, got, want)
		}
	}
}

func TestWordKeepsOtherTokensAsWritten(t *testing.T) {
	for _, token := range []string{"2024", "iphone15", "abc123", "тест1", "4partners", "rfc2616", "naïve", "mixедmix", "", "ab", "я"} {
		if got := Word(token); got != token {
			t.Errorf("Word(%q) = %q, want it unchanged", token, got)
		}
	}
}

func TestTextMixesRussianAndEnglish(t *testing.T) {
	got := Text("Доставка заказов running iPhone15 2024!")
	want := "доставк заказ run iphone15 2024"
	if got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
}

func TestTextEmpty(t *testing.T) {
	for _, input := range []string{"", "   ", "!!! ... ---"} {
		if got := Text(input); got != "" {
			t.Errorf("Text(%q) = %q, want empty", input, got)
		}
		if got := Query(input, false); got != "" {
			t.Errorf("Query(%q) = %q, want empty", input, got)
		}
	}
}

// Snowball is not idempotent in general: "сообщение" gives "сообщен", and
// stemming that again gives "сообщ". The index and the query must each stem
// the raw words exactly once; this test pins the words the contract relies on.
func TestTextIsIdempotentOnCommonStems(t *testing.T) {
	inputs := []string{
		"доставка доставки доставку оплата оплатил заказ заказов",
		"running runs delivery payments order",
		"встреча встречи созвон клиент клиенты",
		"iphone15 2024 ёлка всё",
	}
	for _, input := range inputs {
		once := Text(input)
		if twice := Text(once); twice != once {
			t.Errorf("Text is not idempotent on %q: %q then %q", input, once, twice)
		}
	}
}

func TestTextStemsRawWordsOnce(t *testing.T) {
	if got := Text("сообщение"); got != "сообщен" {
		t.Errorf("Text(сообщение) = %q, want сообщен", got)
	}
}

func TestTermsSplitLikeTextnorm(t *testing.T) {
	inputs := []string{
		"Доставка, оплата; заказ-номер 12 (ёлка) e-mail",
		`"quoted" AND OR NOT near/2 col:text ^start*`,
		"  много   пробелов\tи\nпереносов ",
		"a_b c.d 4partners.io",
	}
	for _, input := range inputs {
		wantCount := len(strings.Fields(strings.ReplaceAll(textnorm.Query(input, false), `"`, "")))
		if got := len(Terms(input)); got != wantCount {
			t.Errorf("Terms(%q) has %d terms, textnorm.Query has %d", input, got, wantCount)
		}
	}
}

func TestQuery(t *testing.T) {
	cases := []struct {
		input  string
		prefix bool
		want   string
	}{
		{"доставки", false, `"доставк"`},
		{"оплатил заказов", false, `"оплат" "заказ"`},
		{"оплатил заказов", true, `"оплат" "заказ"*`},
		{"Ёлка 2024", false, `"елк" "2024"`},
		{`"x" OR col:y NEAR(a b)`, false, `"x" "or" "col" "y" "near" "a" "b"`},
	}
	for _, c := range cases {
		if got := Query(c.input, c.prefix); got != c.want {
			t.Errorf("Query(%q, %v) = %q, want %q", c.input, c.prefix, got, c.want)
		}
	}
}

func TestQueryHasNoFTSSyntax(t *testing.T) {
	got := Query(`a" OR "b* ^c`, true)
	for _, term := range strings.Fields(got) {
		if !strings.HasPrefix(term, `"`) || !(strings.HasSuffix(term, `"`) || strings.HasSuffix(term, `"*`)) {
			t.Errorf("Query term %q is not a quoted token in %q", term, got)
		}
	}
	if strings.Count(got, "*") != 1 || !strings.HasSuffix(got, `"*`) {
		t.Errorf("Query(%q) = %q, want exactly one prefix star on the last term", `a" OR "b* ^c`, got)
	}
}
