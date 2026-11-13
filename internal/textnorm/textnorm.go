// Package textnorm prepares search text and safe FTS5 queries.
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize canonicalizes text for both FTS indexing and queries.
func Normalize(s string) string {
	s = norm.NFC.String(s)
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.Join(strings.Fields(s), " ")
	return norm.NFC.String(s)
}

// Query returns a MATCH expression containing only quoted terms.
func Query(s string, prefix bool) string {
	s = Normalize(s)
	var terms []string
	var term strings.Builder
	flush := func() {
		if term.Len() > 0 {
			terms = append(terms, term.String())
			term.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			term.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	if len(terms) == 0 {
		return ""
	}
	for i := range terms {
		terms[i] = `"` + terms[i] + `"`
	}
	if prefix {
		terms[len(terms)-1] += "*"
	}
	return strings.Join(terms, " ")
}
