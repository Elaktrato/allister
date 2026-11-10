package web

import (
	"net/http"
	"net/url"
	"strings"
)

// refuseCrossOrigin wraps h and answers 403 to browser-initiated mutations
// that come from another origin (CSRF). Every method other than GET, HEAD and
// OPTIONS is checked (all GET handlers are read-only; keep it that way):
//
//  1. If Sec-Fetch-Site is present it must be "same-origin" or "none".
//  2. Otherwise, if Origin is present, its host:port must equal the request's
//     Host (or an X-Forwarded-Host entry, if the proxy sent one). "null" and
//     unparsable origins are refused. Only host:port is compared, not the
//     scheme, so a TLS-terminating proxy that forwards plain HTTP still works.
//  3. Requests carrying neither header (curl, tests, very old clients) are
//     allowed. This protects browsers: they send at least one of the two
//     headers on every cross-site POST.
//
// X-Forwarded-Host is honoured only when present. A page on another site
// cannot make a browser set it, so it cannot be used to forge a match.
func refuseCrossOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			h.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false // "null" and garbage
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, fh := range strings.Split(r.Header.Get("X-Forwarded-Host"), ",") {
		if fh = strings.TrimSpace(fh); fh != "" && strings.EqualFold(u.Host, fh) {
			return true
		}
	}
	return false
}
