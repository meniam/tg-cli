package rpc

import "strings"

const MethodMessageTranslate = "message.translate"

type TranslateParams struct {
	Chat   string `json:"chat"`
	IDs    []int  `json:"ids"`
	ToLang string `json:"to_lang"`
}

type TranslationItem struct {
	ID   int    `json:"id"`
	Text string `json:"text" untrusted:"true"`
}

type TranslateResult struct {
	ChatID int64             `json:"chat_id"`
	ToLang string            `json:"to_lang"`
	Items  []TranslationItem `json:"items"`
}

// ValidateTranslate normalizes and validates a translation request.
func ValidateTranslate(p *TranslateParams) error {
	p.ToLang = strings.ToLower(p.ToLang)
	if len(p.ToLang) != 2 || p.ToLang[0] < 'a' || p.ToLang[0] > 'z' || p.ToLang[1] < 'a' || p.ToLang[1] > 'z' {
		return NewError(CodeInvalidParams, "target language must be two Latin letters")
	}
	if len(p.IDs) < 1 || len(p.IDs) > 20 {
		return NewError(CodeInvalidParams, "provide between 1 and 20 message ids")
	}
	seen := make(map[int]bool, len(p.IDs))
	for _, id := range p.IDs {
		if id <= 0 || seen[id] {
			return NewError(CodeInvalidParams, "message ids must be positive and unique")
		}
		seen[id] = true
	}
	return nil
}
