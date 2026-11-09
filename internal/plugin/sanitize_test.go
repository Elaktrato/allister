package plugin

import (
	"strings"
	"testing"
)

func TestSingleLine(t *testing.T) {
	tests := []struct {
		name, in string
		max      int
		want     string
	}{
		{"plain", "hello", 10, "hello"},
		{"trim", "  hello  ", 10, "hello"},
		{"nul removed", "a\x00b", 10, "ab"},
		{"tab to space", "a\tb", 10, "a b"},
		{"newline to space", "a\nb", 10, "a b"},
		{"crlf one space", "a\r\nb", 10, "a b"},
		{"run of whitespace and controls", "a \t\n\x01 \x7f b", 10, "a b"},
		{"c1 control", "a\u0085b\u009fc", 10, "a b c"},
		{"invalid utf8 dropped", "a\xffb", 10, "ab"},
		{"zwj kept", "👨‍👩", 10, "👨‍👩"},
		{"emoji kept", "x😀y", 10, "x😀y"},
		{"truncate at rune boundary", "日本語テキスト", 3, "日本語"},
		{"truncate then trim", "ab  cd", 3, "ab"},
		{"empty", " \t ", 5, ""},
		{"zero max", "abc", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SingleLine(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("SingleLine(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestMultiLine(t *testing.T) {
	tests := []struct {
		name, in string
		max      int
		want     string
	}{
		{"plain", "a\nb", 10, "a\nb"},
		{"keeps tab", "a\tb", 10, "a\tb"},
		{"crlf", "a\r\nb", 10, "a\nb"},
		{"cr", "a\rb", 10, "a\nb"},
		{"nul removed", "a\x00b", 10, "ab"},
		{"c0 removed", "a\x01\x1bb", 10, "ab"},
		{"c1 removed", "a\u0085b\u009f", 10, "ab"},
		{"invalid utf8", "a\xc3(b", 10, "a(b"},
		{"trim", "\n  a  \n", 10, "a"},
		{"zwj kept", "👨‍👩\n😀", 10, "👨‍👩\n😀"},
		{"truncate runes then trim", "日本語\nテキスト", 4, "日本語"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MultiLine(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("MultiLine(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestHTTPSURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://a.example/x.jpg", "https://a.example/x.jpg"},
		{"http://a.example/x", "http://a.example/x"},
		{"  https://a.example/x  ", "https://a.example/x"},
		{"//cdn.example/x.jpg", "https://cdn.example/x.jpg"},
		{"javascript:alert(1)", ""},
		{"ftp://a.example/x", ""},
		{"data:image/png;base64,AAAA", ""},
		{"https:///x", ""},
		{"https://", ""},
		{"//", ""},
		{"", ""},
		{"/relative/path", ""},
		{"https://a.example/\x00x\ty", "https://a.example/xy"},
		{"ht\ntps://a.example/x", "https://a.example/x"},
		{"https://a.example/\xff", "https://a.example/"},
		{"https://a b.example/x", ""},
	}
	for _, tc := range tests {
		if got := HTTPSURL(tc.in); got != tc.want {
			t.Errorf("HTTPSURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizersNeverEmitControls(t *testing.T) {
	in := "a\x00\x01\t\n\r\u0085\u009f\xffb" + strings.Repeat("é", 5)
	for _, s := range []string{SingleLine(in, 100), MultiLine(in, 100)} {
		for _, r := range s {
			if r == 0 || (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7f && r <= 0x9f) || r == 0xFFFD {
				t.Errorf("control rune %U in %q", r, s)
			}
		}
	}
	if strings.ContainsAny(SingleLine(in, 100), "\n\t\r") {
		t.Error("SingleLine kept whitespace controls")
	}
}
