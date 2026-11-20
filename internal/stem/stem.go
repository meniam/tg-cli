// Package stem reduces words to Snowball stems for the stemmed full-text index.
//
// Indexing and query parsing share these functions, so a word and its forms
// meet on one stem. Cyrillic words use the Russian stemmer and Latin words the
// English one. Anything else (digits, mixed scripts, accented Latin) stays as
// written: part numbers, versions and names must keep matching literally.
package stem

import (
	"strings"
	"unicode"

	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/russian"

	"tg-cli/internal/textnorm"
)

type script int

const (
	scriptOther script = iota
	scriptCyrillic
	scriptLatin
)

// Word returns the stem of one word. The input is normalized like indexed text
// first, so "Ёлка" and "елка" give the same result.
func Word(word string) string {
	return stemNormalized(textnorm.Normalize(word))
}

// Text returns the normalized text with every word replaced by its stem, words
// joined by single spaces. The tokenization matches textnorm.Query: runs of
// letters and digits are words, everything else separates them.
func Text(text string) string {
	return strings.Join(Terms(text), " ")
}

// Terms returns the stems of the words in text, in order, repeats kept.
func Terms(text string) []string {
	words := splitWords(textnorm.Normalize(text))
	for i, word := range words {
		words[i] = stemNormalized(word)
	}
	return words
}

// Query returns an FTS5 MATCH expression over the stems of the query words, or
// an empty string if the query has no words. Terms are quoted, so FTS syntax in
// the query is inert, exactly as in textnorm.Query. With prefix, the last term
// matches as a prefix of a stem.
func Query(text string, prefix bool) string {
	terms := Terms(text)
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

// splitWords cuts normalized text into runs of letters and digits, the same
// rule textnorm.Query applies.
func splitWords(normalized string) []string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range normalized {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func stemNormalized(word string) string {
	switch scriptOf(word) {
	case scriptCyrillic:
		// stemStopWords=true: stop words are stemmed like any other word, so a
		// form such as "хорошо" meets "хорошего" instead of staying unchanged.
		return russian.Stem(word, true)
	case scriptLatin:
		return english.Stem(word, true)
	default:
		return word
	}
}

func scriptOf(word string) script {
	if word == "" {
		return scriptOther
	}
	result := scriptOther
	for _, r := range word {
		var current script
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			current = scriptCyrillic
		case r >= 'a' && r <= 'z':
			current = scriptLatin
		default:
			return scriptOther
		}
		if result != scriptOther && result != current {
			return scriptOther
		}
		result = current
	}
	return result
}
