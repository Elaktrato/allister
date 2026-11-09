package provider

import (
	"html"
	"regexp"
	"strings"
)

var (
	brTag  = regexp.MustCompile(`(?i)<br\s*/?>`)
	anyTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// PlainText converts provider HTML to plain text: <br> becomes a newline,
// other tags are removed, entities are unescaped and the result is trimmed.
func PlainText(s string) string {
	s = brTag.ReplaceAllString(s, "\n")
	s = anyTag.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}
