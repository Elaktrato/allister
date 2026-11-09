package web

import (
	"html/template"
	"net/url"
	"time"

	"github.com/allister/allister/internal/covers"
	"github.com/allister/allister/internal/provider"
	"github.com/allister/allister/internal/store"
	"github.com/allister/allister/internal/titles"
)

// templateFuncs is the one FuncMap shared by every page template set.
var templateFuncs = template.FuncMap{
	// navMarker marks where base.html's navigation goes (see executeFor).
	"navMarker": func() template.HTML { return template.HTML(navMarker) },
	// coverSrc maps a stored image value to a URL (see covers.Src).
	"coverSrc": covers.Src,
	// title resolves a display title for the preferred language.
	"title": func(ts []provider.Title, lang string) string { return titles.Resolve(ts, lang) },
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
	// statusLabel is the display name of a list status.
	"statusLabel": func(s store.Status) string { return statusLabels[s] },
}

var statusLabels = map[store.Status]string{
	store.Watching:    "Watching",
	store.PlanToWatch: "Plan to watch",
	store.Paused:      "Paused",
	store.Completed:   "Completed",
	store.Dropped:     "Dropped",
}

// statusOrder is the order statuses are offered in forms.
var statusOrder = []store.Status{store.PlanToWatch, store.Watching, store.Completed, store.Paused, store.Dropped}

func langLabel(tag string) string {
	for _, o := range baseLangOptions {
		if o.Value == tag {
			return o.Label
		}
	}
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
