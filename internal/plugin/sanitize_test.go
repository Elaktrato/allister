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
		{"nul is a control: one space", "a\x00b", 10, "a b"},
		{"nul run with controls", "a\x00\x00 \x01b", 10, "a b"},
		{"u2028 and u2029 are whitespace", "a\u2028b\u2029c", 10, "a b c"},
		{"nbsp is whitespace", "a\u00a0\u00a0b", 10, "a b"},
		{"bom kept", "a\ufeffb", 10, "a\ufeffb"},
		{"zero width space kept", "a\u200bb", 10, "a\u200bb"},
		{"lone surrogate bytes dropped", "a\xed\xa0\x80b", 10, "ab"},
		{"literal replacement char dropped", "a\ufffdb", 10, "ab"},
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
	long := "https://a.example/" + strings.Repeat("x", 2048-len("https://a.example/"))
	tests := []struct{ name, in, want string }{
		{"https", "https://a.example/x.jpg", "https://a.example/x.jpg"},
		{"http kept", "http://a.example/x", "http://a.example/x"},
		{"trimmed", "  https://a.example/x  ", "https://a.example/x"},
		{"protocol relative", "//cdn.example/x.jpg", "https://cdn.example/x.jpg"},
		{"triple slash", "///x", ""},
		{"user only", "https://user@a.example/x", "https://a.example/x"},
		{"user and password", "https://user:pw@a.example/x?q=1#f", "https://a.example/x?q=1#f"},
		{"userinfo protocol relative", "//u:p@a.example/x", "https://a.example/x"},
		{"upper case scheme and host", "HTTPS://A.Example/Path?Q=A#F", "https://a.example/Path?Q=A#F"},
		{"port kept", "http://A.example:8080/x", "http://a.example:8080/x"},
		{"empty host with port", "http://:80", ""},
		{"empty host", "https://", ""},
		{"empty host with path", "https:///x", ""},
		{"only slashes", "//", ""},
		{"whitespace only host", "https://   /x", ""},
		{"space in host", "https://host name/x", ""},
		{"too long", long + "y", ""},
		{"exactly 2048 runes", long, long},
		{"ftp", "ftp://a.example/x", ""},
		{"javascript", "javascript:alert(1)", ""},
		{"data", "data:image/png;base64,AAAA", ""},
		{"relative", "/relative/path", ""},
		{"empty", "", ""},
		{"idn host", "https://Bücher.example/x", "https://bücher.example/x"},
		{"ipv6", "https://[::1]/x", "https://[::1]/x"},
		{"ipv6 with port", "http://[::1]:8080/x", "http://[::1]:8080/x"},
		{"newline in the middle", "https://a.exam\nple/x", "https://a.example/x"},
		{"nul and tab stripped", "https://a.example/\x00x\ty", "https://a.example/xy"},
		{"newline in scheme", "ht\ntps://a.example/x", "https://a.example/x"},
		{"invalid utf8 stripped", "https://a.example/\xff", "https://a.example/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTTPSURL(tc.in); got != tc.want {
				t.Errorf("HTTPSURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
	for _, in := range []string{"a\u2028b", "a\u2029b", "\u0085", "a\x00b"} {
		if out := SingleLine(in, 10); strings.ContainsAny(out, "\u2028\u2029\u0085\x00") {
			t.Errorf("SingleLine(%q) = %q", in, out)
		}
	}
	if strings.ContainsAny(SingleLine(in, 100), "\n\t\r") {
		t.Error("SingleLine kept whitespace controls")
	}
}
