// Package textfmt renders the small formatting language of notes, reviews
// and unit notes into safe HTML. The language is: paragraphs (a blank line
// separates them, a single newline is a line break), **bold**, *italic*,
// "- " and "1. " lists, [text](https://...) links and ||spoiler|| spans.
// Everything else is escaped, so the output holds only the tags p, br,
// strong, em, ul, ol, li, a (https href plus a fixed rel) and span
// (class "spoiler", tabindex 0). Markers without a partner stay literal.
package textfmt

import (
	"html"
	"html/template"
	"strings"
)

const (
	linkOpen    = `<a href="`
	linkOpenEnd = `" rel="noopener noreferrer nofollow">`
	spoilerOpen = `<span class="spoiler" tabindex="0">`
)

// Render converts s to HTML. The result is safe to embed in a page: the
// only markup in it is the allowed tags above, written by this package.
func Render(s string) template.HTML {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var out strings.Builder
	var para []string
	list := "" // "ul", "ol" or "" while no list is open

	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + inline(strings.Join(para, "\n"), false) + "</p>")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			out.WriteString("</" + list + ">")
			list = ""
		}
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			flushPara()
			closeList()
			continue
		}
		if kind, item, ok := listItem(line); ok {
			flushPara()
			if list != kind {
				closeList()
				out.WriteString("<" + kind + ">")
				list = kind
			}
			out.WriteString("<li>" + inline(item, false) + "</li>")
			continue
		}
		closeList()
		para = append(para, line)
	}
	flushPara()
	closeList()
	return template.HTML(out.String()) //nolint:gosec // G203: every byte is escaped text or a tag written above
}

// listItem recognises "- text" and "1. text".
func listItem(line string) (kind, text string, ok bool) {
	if t, found := strings.CutPrefix(line, "- "); found {
		return "ul", strings.TrimSpace(t), true
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && strings.HasPrefix(line[i:], ". ") {
		return "ol", strings.TrimSpace(line[i+2:]), true
	}
	return "", "", false
}

// inline renders the inline markup of s. Every construct is parsed from a
// substring on its own, so the output always nests properly. In link text
// (noLink) a further link stays literal.
func inline(s string, noLink bool) string {
	var out strings.Builder
	lit := 0 // start of the pending literal run
	flush := func(end int) {
		out.WriteString(strings.ReplaceAll(html.EscapeString(s[lit:end]), "\n", "<br>"))
	}
	for i := 0; i < len(s); {
		var markup string
		var n int // bytes consumed from i
		switch {
		case strings.HasPrefix(s[i:], "||"):
			markup, n = wrapped(s, i, "||", spoilerOpen, "</span>", false, noLink)
		case strings.HasPrefix(s[i:], "**"):
			markup, n = wrapped(s, i, "**", "<strong>", "</strong>", true, noLink)
		case s[i] == '*':
			markup, n = italic(s, i, noLink)
		case s[i] == '[' && !noLink:
			markup, n = link(s, i)
		}
		if n == 0 {
			i++
			continue
		}
		flush(i)
		out.WriteString(markup)
		i += n
		lit = i
	}
	flush(len(s))
	return out.String()
}

// wrapped handles a symmetric marker: the span from the marker at i to the
// next occurrence of it. Empty content (and, with trim, content that starts
// or ends in a space) leaves the marker literal.
func wrapped(s string, i int, marker, open, closeTag string, trim, noLink bool) (string, int) {
	start := i + len(marker)
	end := strings.Index(s[start:], marker)
	if end <= 0 {
		return "", 0
	}
	inner := s[start : start+end]
	if trim && (isSpace(inner[0]) || isSpace(inner[len(inner)-1])) {
		return "", 0
	}
	return open + inline(inner, noLink) + closeTag, len(marker) + end + len(marker)
}

// italic handles *text*: the closer is a single * that is not part of a **.
func italic(s string, i int, noLink bool) (string, int) {
	start := i + 1
	for j := start; j < len(s); j++ {
		if s[j] != '*' {
			continue
		}
		if j+1 < len(s) && s[j+1] == '*' {
			j++ // skip a whole ** pair
			continue
		}
		inner := s[start:j]
		if inner == "" || isSpace(inner[0]) || isSpace(inner[len(inner)-1]) {
			return "", 0
		}
		return "<em>" + inline(inner, noLink) + "</em>", j + 1 - i
	}
	return "", 0
}

// link handles [text](https://url). Any other target stays literal text.
func link(s string, i int) (string, int) {
	mid := strings.Index(s[i:], "](")
	if mid <= 1 {
		return "", 0
	}
	textEnd := i + mid
	urlStart := textEnd + 2
	urlLen := strings.IndexByte(s[urlStart:], ')')
	if urlLen < 0 {
		return "", 0
	}
	url := s[urlStart : urlStart+urlLen]
	if !strings.HasPrefix(url, "https://") || len(url) == len("https://") {
		return "", 0
	}
	for k := 0; k < len(url); k++ {
		if url[k] <= ' ' || url[k] == 0x7f {
			return "", 0
		}
	}
	return linkOpen + html.EscapeString(url) + linkOpenEnd + inline(s[i+1:textEnd], true) + "</a>",
		urlStart + urlLen + 1 - i
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' }
