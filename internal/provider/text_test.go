package provider

import "testing"

func TestPlainTextStripsHTML(t *testing.T) {
	cases := map[string]string{
		"a<br>b<i>c</i>&amp;":                 "a\nbc&",
		"a<BR/>b":                             "a\nb",
		"a<br />b<Br>c":                       "a\nb\nc",
		"<p>x <b><i>y</i></b> z</p>":          "x y z",
		"Tom &amp; Jerry &quot;q&quot; &#39;": "Tom & Jerry \"q\" '",
		"  plain text, 1 < 2 stays?  ":        "plain text, 1 < 2 stays?",
		"plain":                               "plain",
		"":                                    "",
		"<p>a</p>\n":                          "a",
	}
	for in, want := range cases {
		if got := PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}
