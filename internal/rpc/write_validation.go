package rpc

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateReactParams checks only local syntax; Telegram decides availability.
func ValidateReactParams(p ReactParams) error {
	if p.ID <= 0 {
		return NewError(CodeInvalidParams, "message id must be positive")
	}
	if p.Emoji == "" || len(p.Emoji) > 32 || !utf8.ValidString(p.Emoji) || strings.IndexFunc(p.Emoji, unicode.IsSpace) >= 0 {
		return NewError(CodeInvalidParams, "emoji must be nonempty, contain no whitespace, and be at most 32 bytes")
	}
	return nil
}

// ValidatePollParams enforces the agreed CLI limits before any Telegram request.
// The server may apply different limits from its configuration.
func ValidatePollParams(p PollParams) error {
	if strings.TrimSpace(p.Question) == "" || utf8.RuneCountInString(p.Question) > 255 || !utf8.ValidString(p.Question) {
		return NewError(CodeInvalidParams, "poll question must be 1 to 255 characters")
	}
	if len(p.Options) < 2 || len(p.Options) > 10 {
		return NewError(CodeInvalidParams, "poll must have 2 to 10 options")
	}
	seen := make(map[string]struct{}, len(p.Options))
	for _, option := range p.Options {
		if strings.TrimSpace(option) == "" || utf8.RuneCountInString(option) > 100 || !utf8.ValidString(option) {
			return NewError(CodeInvalidParams, "poll options must be 1 to 100 characters")
		}
		key := strings.TrimSpace(option)
		if _, exists := seen[key]; exists {
			return NewError(CodeInvalidParams, "poll options must be unique")
		}
		seen[key] = struct{}{}
	}
	if utf8.RuneCountInString(p.Explanation) > 200 || strings.Count(p.Explanation, "\n") > 2 || !utf8.ValidString(p.Explanation) {
		return NewError(CodeInvalidParams, "quiz explanation must be at most 200 characters and two line feeds")
	}
	if !p.Quiz && (p.Correct != 0 || p.Explanation != "") {
		return NewError(CodeInvalidParams, "correct answer and explanation require a quiz")
	}
	if p.Quiz && (p.Correct < 1 || p.Correct > len(p.Options)) {
		return NewError(CodeInvalidParams, "quiz correct answer must name an existing option")
	}
	if p.Quiz && p.Multiple {
		return NewError(CodeInvalidParams, "quiz cannot allow multiple answers")
	}
	if p.ReplyTo < 0 || p.Topic < 0 {
		return NewError(CodeInvalidParams, "reply and topic ids must not be negative")
	}
	return nil
}
