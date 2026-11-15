package web

import (
	"fmt"
	"strings"
)

// logSafe formats v for a log line with CR and LF escaped, so a value that
// carries request data (an id, a query value, an error quoting one) cannot
// start a forged log entry. Use it for every such argument of log.Printf.
func logSafe(v any) string {
	s := fmt.Sprint(v)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return strings.ReplaceAll(s, "\n", `\n`)
}
