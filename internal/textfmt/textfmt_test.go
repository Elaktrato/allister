package textfmt

import "testing"

func TestRender(t *testing.T) {
	const rel = `rel="noopener noreferrer nofollow"`
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"blank only", "\n \n", ""},
		{"paragraph", "hello", "<p>hello</p>"},
		{"two paragraphs", "a\n\nb", "<p>a</p><p>b</p>"},
		{"line break", "a\nb", "<p>a<br>b</p>"},
		{"crlf", "a\r\nb\r\n\r\nc", "<p>a<br>b</p><p>c</p>"},
		{"bold", "a **b** c", "<p>a <strong>b</strong> c</p>"},
		{"italic", "a *b* c", "<p>a <em>b</em> c</p>"},
		{"bold in italic", "*a **b** c*", "<p><em>a <strong>b</strong> c</em></p>"},
		{"unordered list", "- a\n- b", "<ul><li>a</li><li>b</li></ul>"},
		{"ordered list", "1. a\n2. b", "<ol><li>a</li><li>b</li></ol>"},
		{"list then paragraph", "- a\n\nb", "<ul><li>a</li></ul><p>b</p>"},
		{"paragraph then list", "intro\n- a", "<p>intro</p><ul><li>a</li></ul>"},
		{"list type switch", "- a\n1. b", "<ul><li>a</li></ul><ol><li>b</li></ol>"},
		{"inline in list", "- **a**", "<ul><li><strong>a</strong></li></ul>"},
		{"dash without space", "-a", "<p>-a</p>"},
		{"link", "[x](https://example.com/a?b=1&c=2)",
			`<p><a href="https://example.com/a?b=1&amp;c=2" ` + rel + `>x</a></p>`},
		{"link with bold text", "[**x**](https://e.com)", `<p><a href="https://e.com" ` + rel + `><strong>x</strong></a></p>`},
		{"javascript link", "[x](javascript:alert(1))", "<p>[x](javascript:alert(1))</p>"},
		{"http link", "[x](http://e.com)", "<p>[x](http://e.com)</p>"},
		{"data link", "[x](data:text/html,a)", "<p>[x](data:text/html,a)</p>"},
		{"relative link", "[x](/a)", "<p>[x](/a)</p>"},
		{"https case", "[x](HTTPS://e.com)", "<p>[x](HTTPS://e.com)</p>"},
		{"link url with space", "[x](https://e.com a)", "<p>[x](https://e.com a)</p>"},
		{"link url quote", `[x](https://e.com/"onclick=1)`, `<p><a href="https://e.com/&#34;onclick=1" ` + rel + `>x</a></p>`},
		{"spoiler", "a ||b|| c", `<p>a <span class="spoiler" tabindex="0">b</span> c</p>`},
		{"spoiler whole paragraph", "||a\nb||", `<p><span class="spoiler" tabindex="0">a<br>b</span></p>`},
		{"bold in spoiler", "||**b**||", `<p><span class="spoiler" tabindex="0"><strong>b</strong></span></p>`},
		{"spoiler in bold", "**||b||**", `<p><strong><span class="spoiler" tabindex="0">b</span></strong></p>`},
		{"spoiler in link", "[||a||](https://e.com)", `<p><a href="https://e.com" ` + rel + `><span class="spoiler" tabindex="0">a</span></a></p>`},
		{"unclosed bold", "a **b", "<p>a **b</p>"},
		{"unclosed italic", "a *b", "<p>a *b</p>"},
		{"unclosed spoiler", "a ||b", "<p>a ||b</p>"},
		{"empty markers", "**** || ****", "<p>**** || ****</p>"},
		{"unclosed link", "[x](https://e.com", "<p>[x](https://e.com</p>"},
		{"script", "<script>alert(1)</script>", "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>"},
		{"entities", "&lt; &amp; &#60; \" '", "<p>&amp;lt; &amp;amp; &amp;#60; &#34; &#39;</p>"},
		{"attribute injection", `<a href="https://e.com" onclick="x">`, "<p>&lt;a href=&#34;https://e.com&#34; onclick=&#34;x&#34;&gt;</p>"},
		{"raw url stays text", "https://e.com", "<p>https://e.com</p>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Render(tt.in)); got != tt.want {
				t.Errorf("Render(%q)\n got %s\nwant %s", tt.in, got, tt.want)
			}
		})
	}
}
