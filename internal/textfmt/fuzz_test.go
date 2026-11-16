package textfmt

import (
	"fmt"
	"strings"
	"testing"
)

const allowedRel = `rel="noopener noreferrer nofollow"`

// checkTags scans out and reports the first tag outside the allowed set:
// every '<' must open or close p, br, strong, em, ul, ol, li, a (href
// https only plus the exact rel) or span (class="spoiler" tabindex="0"),
// and opening and closing tags must nest properly.
func checkTags(out string) error {
	var stack []string
	for i := 0; i < len(out); i++ {
		if out[i] == '>' {
			return fmt.Errorf("bare '>' at %d", i)
		}
		if out[i] != '<' {
			continue
		}
		end := strings.IndexByte(out[i:], '>')
		if end < 0 {
			return fmt.Errorf("unterminated tag at %d", i)
		}
		tag := out[i+1 : i+end]
		i += end
		if strings.ContainsAny(tag, "<\n\r") {
			return fmt.Errorf("odd tag %q", tag)
		}
		if name, ok := strings.CutPrefix(tag, "/"); ok {
			switch name {
			case "p", "strong", "em", "ul", "ol", "li", "a", "span":
			default:
				return fmt.Errorf("closing tag %q not allowed", tag)
			}
			if len(stack) == 0 || stack[len(stack)-1] != name {
				return fmt.Errorf("closing %q does not match stack %v", tag, stack)
			}
			stack = stack[:len(stack)-1]
			continue
		}
		switch {
		case tag == "br":
			continue
		case tag == "p", tag == "strong", tag == "em", tag == "ul", tag == "ol", tag == "li":
			stack = append(stack, tag)
		case strings.HasPrefix(tag, `a href="https://`) && strings.HasSuffix(tag, `" `+allowedRel):
			href := strings.TrimSuffix(strings.TrimPrefix(tag, `a href="`), `" `+allowedRel)
			if strings.ContainsAny(href, "\"'<> \t") {
				return fmt.Errorf("href %q has unsafe characters", href)
			}
			stack = append(stack, "a")
		case tag == `span class="spoiler" tabindex="0"`:
			stack = append(stack, "span")
		default:
			return fmt.Errorf("tag %q not allowed", tag)
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("unclosed tags %v", stack)
	}
	return nil
}

func TestCheckTagsRejects(t *testing.T) {
	for _, bad := range []string{
		"<script>", "<p onclick=x>", `<a href="http://e.com" ` + allowedRel + `>`,
		`<a href="https://e.com">`, "<p>", "</p>", "<span>", "<img src=x>", "a > b",
		`<span class="spoiler">`, "<p><em></p></em>",
	} {
		if checkTags(bad) == nil {
			t.Errorf("checkTags(%q) accepted", bad)
		}
	}
}

func FuzzRender(f *testing.F) {
	for _, s := range []string{
		"", "plain", "**bold** *it* ||sp||", "- a\n- b\n\n1. c", "[x](https://e.com)",
		"[x](javascript:alert(1))", "<script>alert(1)</script>", "**unclosed", "||unclosed",
		"*a **b* c**", "[||a](https://e.com)||", "[a||b](https://e.com)", "||[a||](https://e.com)",
		"&lt;b&gt; &amp; &#x3C;", "[[x](https://e.com)](https://f.com)", "[x](https://e.com\"onclick=1)",
		"****||||", "- \n1. \n", "a\r\nb", "\x00\x1b<a href=\"https://e.com\">",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := string(Render(s))
		if err := checkTags(out); err != nil {
			t.Fatalf("Render(%q) = %q: %v", s, out, err)
		}
	})
}
