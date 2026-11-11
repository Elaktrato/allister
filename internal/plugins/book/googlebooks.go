package book

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

const (
	googleBooksDefaultURL = "https://www.googleapis.com/books/v1"
	googleBooksMaxBody    = 8 << 20
)

type googleBooksClient struct {
	base, key string
	hc        *http.Client
}

func newGoogleBooksClient(base, key string, hc *http.Client) *googleBooksClient {
	if base == "" {
		base = googleBooksDefaultURL
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &googleBooksClient{base: strings.TrimRight(base, "/"), key: key, hc: hc}
}

type gbIdentifier struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

type gbVolumeInfo struct {
	Title         string         `json:"title"`
	Subtitle      string         `json:"subtitle"`
	Authors       []string       `json:"authors"`
	Publisher     string         `json:"publisher"`
	PublishedDate string         `json:"publishedDate"`
	Description   string         `json:"description"`
	PageCount     int            `json:"pageCount"`
	Categories    []string       `json:"categories"`
	Language      string         `json:"language"`
	InfoLink      string         `json:"infoLink"`
	CanonicalLink string         `json:"canonicalVolumeLink"`
	PreviewLink   string         `json:"previewLink"`
	IndustryIDs   []gbIdentifier `json:"industryIdentifiers"`
	ImageLinks    struct {
		SmallThumbnail string `json:"smallThumbnail"`
		Thumbnail      string `json:"thumbnail"`
		Large          string `json:"large"`
	} `json:"imageLinks"`
}

type gbVolume struct {
	ID         string       `json:"id"`
	VolumeInfo gbVolumeInfo `json:"volumeInfo"`
}

type gbVolumes struct {
	Items []gbVolume `json:"items"`
}

func (c *googleBooksClient) get(ctx context.Context, path string, values url.Values, out any) error {
	if values == nil {
		values = url.Values{}
	}
	if c.key != "" {
		values.Set("key", c.key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+values.Encode(), nil)
	if err != nil {
		return fmt.Errorf("googlebooks: %w", plugin.ErrUnavailable)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Never include the URL in this error: it carries the API key.
		return fmt.Errorf("googlebooks: %w", plugin.ErrUnavailable)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return plugin.ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusForbidden:
		return plugin.ErrRateLimited
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout:
		return plugin.ErrUnavailable
	default:
		return fmt.Errorf("%w: googlebooks status %d", plugin.ErrBadResponse, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, googleBooksMaxBody+1))
	if err != nil {
		return fmt.Errorf("googlebooks: %w", plugin.ErrUnavailable)
	}
	if len(body) > googleBooksMaxBody {
		return fmt.Errorf("%w: googlebooks response too large", plugin.ErrBadResponse)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: googlebooks: %v", plugin.ErrBadResponse, err)
	}
	return nil
}

func (c *googleBooksClient) search(ctx context.Context, q string) ([]plugin.ItemSummary, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []plugin.ItemSummary{}, nil
	}
	v := url.Values{"q": {q}, "maxResults": {"20"}, "printType": {"books"}}
	var out gbVolumes
	if err := c.get(ctx, "/volumes", v, &out); err != nil {
		return nil, err
	}
	res := make([]plugin.ItemSummary, 0, len(out.Items))
	for _, item := range out.Items {
		id := plugin.SingleLine(item.ID, maxIDRunes)
		if id == "" {
			continue
		}
		info := item.VolumeInfo
		fields := map[string]plugin.FieldValue{}
		if len(info.Authors) > 0 {
			if a := plugin.SingleLine(info.Authors[0], maxNameRunes); a != "" {
				fields["author"] = plugin.FieldValue{Text: a}
			}
		}
		if info.PageCount > 0 {
			pages := float64(info.PageCount)
			fields["pages"] = plugin.FieldValue{Num: &pages}
		}
		if y, _ := bookDate(info.PublishedDate); y > 0 {
			year := float64(y)
			fields["year"] = plugin.FieldValue{Num: &year}
		}
		if isbn := gbISBN(info); isbn != "" {
			fields["isbn"] = plugin.FieldValue{Text: isbn}
		}
		res = append(res, plugin.ItemSummary{
			Source: SourceGoogleBooks, ID: id, Kind: KindBook,
			Titles:   []plugin.Title{{Lang: gbLanguage(info.Language), Text: plugin.SingleLine(info.Title, maxTitleRunes)}},
			Fields:   fields,
			CoverURL: gbCover(info),
			URL:      gbExternalURL(id, info),
		})
	}
	return res, nil
}

func (c *googleBooksClient) details(ctx context.Context, id string) (plugin.ItemData, error) {
	var v gbVolume
	if err := c.get(ctx, "/volumes/"+url.PathEscape(id), nil, &v); err != nil {
		return plugin.ItemData{}, err
	}
	return gbData(v)
}

func (c *googleBooksClient) lookupISBN(ctx context.Context, isbn string) (plugin.ItemData, error) {
	isbn = canonicalISBN(isbn)
	if isbn == "" {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	var out gbVolumes
	v := url.Values{"q": {"isbn:" + isbn}, "maxResults": {"5"}, "printType": {"books"}}
	if err := c.get(ctx, "/volumes", v, &out); err != nil {
		return plugin.ItemData{}, err
	}
	for _, item := range out.Items {
		if gbISBN(item.VolumeInfo) == isbn {
			return gbData(item)
		}
	}
	return plugin.ItemData{}, plugin.ErrNotFound
}

func (c *googleBooksClient) people(ctx context.Context, id string) ([]plugin.Credit, error) {
	var v gbVolume
	if err := c.get(ctx, "/volumes/"+url.PathEscape(id), nil, &v); err != nil {
		return nil, err
	}
	return gbCredits(v.VolumeInfo.Authors), nil
}

type googleBooksSource struct{ c *googleBooksClient }

func (s *googleBooksSource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindBook {
		return nil, plugin.ErrNotFound
	}
	return s.c.search(ctx, q)
}

func (s *googleBooksSource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindBook {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.c.details(ctx, id)
}

func (s *googleBooksSource) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if kind != KindBook {
		return nil, plugin.ErrNotFound
	}
	credits, err := s.c.people(ctx, id)
	if err != nil {
		return nil, err
	}
	return bookCredits(bookRecord{Credits: credits}), nil
}

func (s *googleBooksSource) Recommendations(context.Context, string, string) ([]plugin.ItemSummary, error) {
	return nil, nil
}

func (s *googleBooksSource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindBook {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	if idType != "isbn" {
		return plugin.ItemData{}, fmt.Errorf("googlebooks: cannot look up %q ids", idType)
	}
	return s.c.lookupISBN(ctx, value)
}

func gbData(v gbVolume) (plugin.ItemData, error) {
	info := v.VolumeInfo
	id := plugin.SingleLine(v.ID, maxIDRunes)
	if id == "" || plugin.SingleLine(info.Title, maxTitleRunes) == "" {
		return plugin.ItemData{}, fmt.Errorf("%w: googlebooks volume has no title", plugin.ErrBadResponse)
	}
	isbn := gbISBN(info)
	r := bookRecord{
		Source: SourceGoogleBooks, ID: id, Title: info.Title, Subtitle: info.Subtitle,
		ISBN: isbn, Publisher: info.Publisher, Language: gbLanguage(info.Language),
		Synopsis: anilist.PlainText(info.Description), CoverURL: gbCover(info),
		ExternalURL: gbExternalURL(id, info), Pages: info.PageCount,
		ExternalIDs: map[string]string{},
	}
	r.Year, r.PublishedOn = bookDate(info.PublishedDate)
	r.Genres = gbGenres(info.Categories)
	if len(info.Authors) > 0 {
		r.Author = info.Authors[0]
	}
	if isbn != "" {
		r.ExternalIDs["isbn"] = isbn
	}
	if len(r.ExternalIDs) == 0 {
		r.ExternalIDs = nil
	}
	return bookData(r), nil
}

func gbCredits(authors []string) []plugin.Credit {
	out := make([]plugin.Credit, 0, len(authors))
	for _, name := range authors {
		name = plugin.SingleLine(name, maxNameRunes)
		if name == "" {
			continue
		}
		out = append(out, plugin.Credit{Role: "author", Person: plugin.PersonRef{
			ID: googleBooksPersonID(name), Name: name,
		}})
	}
	return out
}

func googleBooksPersonID(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(name), " "))))
	return fmt.Sprintf("gb-%x", sum[:8])
}

func gbISBN(info gbVolumeInfo) string {
	var fallback string
	for _, id := range info.IndustryIDs {
		v := canonicalISBN(id.Identifier)
		if v == "" {
			continue
		}
		if id.Type == "ISBN_13" {
			return v
		}
		if fallback == "" {
			fallback = v
		}
	}
	return fallback
}

func normaliseISBN(s string) string {
	s = strings.ToUpper(strings.Trim(strings.TrimSpace(s), `="' `))
	s = strings.NewReplacer("-", "", " ", "", "\u2010", "", "\u2011", "").Replace(s)
	if len(s) != 10 && len(s) != 13 {
		return ""
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r == 'X') {
			return ""
		}
	}
	return s
}

func gbLanguage(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "en"
	}
	return s
}

func gbCover(info gbVolumeInfo) string {
	for _, u := range []string{info.ImageLinks.Large, info.ImageLinks.Thumbnail, info.ImageLinks.SmallThumbnail} {
		if u = plugin.HTTPSURL(u); u != "" {
			return strings.Replace(u, "http://", "https://", 1)
		}
	}
	return ""
}

func gbExternalURL(id string, info gbVolumeInfo) string {
	for _, u := range []string{info.InfoLink, info.CanonicalLink, info.PreviewLink} {
		if u = plugin.HTTPSURL(u); u != "" {
			return strings.Replace(u, "http://", "https://", 1)
		}
	}
	return "https://books.google.com/books?id=" + url.QueryEscape(id)
}

func gbGenres(categories []string) []string {
	var out []string
	for _, category := range categories {
		parts := strings.Split(category, "/")
		last := ""
		for i := len(parts) - 1; i >= 0; i-- {
			if p := strings.TrimSpace(parts[i]); p != "" && !strings.EqualFold(p, "General") {
				last = p
				break
			}
		}
		if last == "" {
			last = strings.TrimSpace(category)
		}
		if last != "" {
			out = append(out, last)
		}
	}
	return cleanTerms(out, maxGenres)
}
