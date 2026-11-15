// Package redact removes recognizable secrets from diagnostics.
package redact

import "regexp"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api_hash|password|secret|phone)\s*[=:]\s*[^\s,;]+`),
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/@]+@`),
	regexp.MustCompile(`\+[0-9][0-9 ()-]{5,}[0-9]`),
	regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`),
	regexp.MustCompile(`\b[A-Za-z0-9_/-]{64,}={0,2}\b`),
}

// Text masks recognized credentials; callers must not echo login input.
func Text(s string) string {
	for _, p := range patterns {
		s = p.ReplaceAllString(s, "[redacted]")
	}
	return s
}
