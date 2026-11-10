package web

import (
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/covers"
)

// templateFuncs is the one FuncMap shared by every page template set.
var templateFuncs = template.FuncMap{
	// navMarker marks where base.html's navigation goes (see executeFor).
	"navMarker": func() template.HTML { return template.HTML(navMarker) },
	// appearanceMarker marks where base.html's appearance stylesheet link
	// goes (see fillMarkers).
	"appearanceMarker": func() template.HTML { return template.HTML(appearanceMarker) },
	// settingsTabsMarker marks where a settings page's section tabs go.
	"settingsTabsMarker": func() template.HTML { return template.HTML(settingsTabsMarker) },
	// dialogStatuses is the dialog slot's data-swap-statuses value: the
	// statuses app.js allows the slot to swap (dialogSwapStatuses).
	"dialogStatuses": dialogStatuses,
	// coverSrc maps a stored image value to a URL (see covers.Src).
	"coverSrc": covers.Src,
	// langLabel is the human label of a language tag.
	"langLabel": langLabel,
	// date formats a calendar day as 2006-01-02 ("" for nil).
	"date": func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format("2006-01-02")
	},
	// httpsURL returns u only when it is an absolute https URL, else "".
	"httpsURL": httpsURL,
}

func langLabel(tag string) string {
	if n, ok := langNames[tag]; ok {
		return n
	}
	return tag
}

func httpsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return raw
}

// httpURL returns raw only when it is an absolute http(s) URL, else "".
func httpURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return raw
}

// fmtDay formats a calendar day as 2006-01-02 ("" for nil).
func fmtDay(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// dialogSwapStatuses are the error statuses with which the dialog handlers
// (add, match, list entry edit) answer an htmx request with a re-rendered
// dialog. htmx 2 swaps no 4xx or 5xx answer by itself, so every dialog slot
// allows exactly these (see dialogStatuses).
var dialogSwapStatuses = []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadGateway, http.StatusServiceUnavailable}

// dialogStatuses returns "409 422 ..." for dialogSwapStatuses: the value
// of the dialog slot's data-swap-statuses, which app.js consults.
func dialogStatuses() string {
	parts := make([]string, len(dialogSwapStatuses))
	for i, c := range dialogSwapStatuses {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, " ")
}
