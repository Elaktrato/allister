package book

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anilist"
)

const (
	openLibraryDefaultURL = "https://openlibrary.org"
	openLibraryUserAgent  = "Allister/1.0 (self-hosted book tracker)"
	openLibraryMaxBody    = 8 << 20
)

type openLibraryClient struct {
	base string
	hc   *http.Client
}

func newOpenLibraryClient(base string, hc *http.Client) *openLibraryClient {
	if base == "" {
		base = openLibraryDefaultURL
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &openLibraryClient{base: strings.TrimRight(base, "/"), hc: hc}
}

type olSearchDoc struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	AuthorName       []string `json:"author_name"`
	FirstPublishYear int      `json:"first_publish_year"`
	CoverID          int      `json:"cover_i"`
	ISBN             []string `json:"isbn"`
}

type olSearchResult struct {
	Docs []olSearchDoc `json:"docs"`
}

type olWork struct {
	Key              string          `json:"key"`
	Title            string          `json:"title"`
	Description      json.RawMessage `json:"description"`
	FirstPublishDate string          `json:"first_publish_date"`
	Covers           []int           `json:"covers"`
	Subjects         []string        `json:"subjects"`
	Authors          []struct {
		Author struct {
			Key string `json:"key"`
		} `json:"author"`
	} `json:"authors"`
}

type olEdition struct {
	Key           string       `json:"key"`
	Title         string       `json:"title"`
	NumberOfPages int          `json:"number_of_pages"`
	ISBN10        []string     `json:"isbn_10"`
	ISBN13        []string     `json:"isbn_13"`
	Publishers    []string     `json:"publishers"`
	PublishDate   string       `json:"publish_date"`
	Languages     []olLanguage `json:"languages"`
	Covers        []int        `json:"covers"`
	Series        []string     `json:"series"`
	Works         []struct {
		Key string `json:"key"`
	} `json:"works"`
}

type olLanguage struct {
	Key string `json:"key"`
}

type olEditions struct {
	Entries []olEdition `json:"entries"`
}

type olAuthor struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Photos []int  `json:"photos"`
	Links  []struct {
		URL string `json:"url"`
	} `json:"links"`
}

func (c *openLibraryClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("openlibrary: %w: %v", plugin.ErrUnavailable, err)
	}
	req.Header.Set("User-Agent", openLibraryUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("openlibrary: %w: %v", plugin.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return plugin.ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return plugin.ErrRateLimited
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout:
		return plugin.ErrUnavailable
	default:
		return fmt.Errorf("%w: openlibrary status %d", plugin.ErrBadResponse, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, openLibraryMaxBody+1))
	if err != nil {
		return fmt.Errorf("openlibrary: %w: %v", plugin.ErrUnavailable, err)
	}
	if len(body) > openLibraryMaxBody {
		return fmt.Errorf("%w: openlibrary response too large", plugin.ErrBadResponse)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: openlibrary: %v", plugin.ErrBadResponse, err)
	}
	return nil
}

func (c *openLibraryClient) search(ctx context.Context, q string) ([]plugin.ItemSummary, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []plugin.ItemSummary{}, nil
	}
	v := url.Values{}
	v.Set("q", q)
	v.Set("limit", "20")
	v.Set("fields", "key,title,author_name,first_publish_year,cover_i,isbn")
	var out olSearchResult
	if err := c.get(ctx, "/search.json?"+v.Encode(), &out); err != nil {
		return nil, err
	}
	res := make([]plugin.ItemSummary, 0, len(out.Docs))
	for _, d := range out.Docs {
		id := olID(d.Key, "/works/")
		if id == "" {
			continue
		}
		fields := map[string]plugin.FieldValue{}
		if len(d.AuthorName) > 0 {
			if a := plugin.SingleLine(d.AuthorName[0], maxNameRunes); a != "" {
				fields["author"] = plugin.FieldValue{Text: a}
			}
		}
		if d.FirstPublishYear > 0 {
			y := float64(d.FirstPublishYear)
			fields["year"] = plugin.FieldValue{Num: &y}
		}
		if len(d.ISBN) > 0 {
			if isbn := plugin.SingleLine(d.ISBN[0], maxIDRunes); isbn != "" {
				fields["isbn"] = plugin.FieldValue{Text: isbn}
			}
		}
		item := plugin.ItemSummary{
			Source: SourceOpenLibrary, ID: id, Kind: KindBook,
			Titles:   []plugin.Title{{Lang: "en", Text: plugin.SingleLine(d.Title, maxTitleRunes)}},
			Fields:   fields,
			CoverURL: olCover(d.CoverID),
			URL:      "https://openlibrary.org/works/" + url.PathEscape(id),
		}
		res = append(res, item)
	}
	return res, nil
}

func (c *openLibraryClient) work(ctx context.Context, id string) (olWork, error) {
	var w olWork
	if err := c.get(ctx, "/works/"+url.PathEscape(id)+".json", &w); err != nil {
		return olWork{}, err
	}
	return w, nil
}

func (c *openLibraryClient) editions(ctx context.Context, id string) ([]olEdition, error) {
	var out olEditions
	if err := c.get(ctx, "/works/"+url.PathEscape(id)+"/editions.json?limit=50", &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

func (c *openLibraryClient) author(ctx context.Context, id string) (olAuthor, error) {
	var a olAuthor
	if err := c.get(ctx, "/authors/"+url.PathEscape(id)+".json", &a); err != nil {
		return olAuthor{}, err
	}
	return a, nil
}

func (c *openLibraryClient) details(ctx context.Context, id string) (plugin.ItemData, error) {
	w, err := c.work(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	eds, err := c.editions(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	ed := chooseEdition(eds)
	return c.record(ctx, id, w, ed)
}

func (c *openLibraryClient) record(ctx context.Context, id string, w olWork, ed *olEdition) (plugin.ItemData, error) {
	credits, err := c.credits(ctx, w)
	if err != nil {
		return plugin.ItemData{}, err
	}
	r := bookRecord{
		Source: SourceOpenLibrary, ID: id, Title: w.Title, Language: "en",
		Credits: credits, ExternalIDs: map[string]string{},
	}
	if len(credits) > 0 {
		r.Author = credits[0].Person.Name
	}
	if ed != nil {
		r.AltTitle = ed.Title
		r.AltLanguage = marcLanguage(ed.Languages)
		if r.AltLanguage == "en" {
			r.AltTitle, r.AltLanguage = "", ""
		}
		if strings.TrimSpace(r.Title) == "" {
			r.Title, r.Language = ed.Title, r.AltLanguage
			r.AltTitle, r.AltLanguage = "", ""
			if r.Language == "" {
				r.Language = "en"
			}
		}
		r.Pages = ed.NumberOfPages
		r.ISBN = firstNonEmpty(append(ed.ISBN13, ed.ISBN10...)...)
		if len(ed.Publishers) > 0 {
			r.Publisher = ed.Publishers[0]
		}
		if len(ed.Series) > 0 {
			r.Series = ed.Series[0]
		}
		if len(ed.Covers) > 0 {
			r.CoverURL = olCover(ed.Covers[0])
		}
		if r.Year, r.PublishedOn = bookDate(w.FirstPublishDate); r.Year == 0 {
			r.Year, r.PublishedOn = bookDate(ed.PublishDate)
		}
	} else {
		r.Year, r.PublishedOn = bookDate(w.FirstPublishDate)
		if len(w.Covers) > 0 {
			r.CoverURL = olCover(w.Covers[0])
		}
	}
	if r.Title == "" {
		return plugin.ItemData{}, fmt.Errorf("%w: openlibrary work %s has no title", plugin.ErrBadResponse, id)
	}
	if r.PublishedOn == nil && r.Year == 0 {
		r.Year = 0
	}
	r.Synopsis = olDescription(w.Description)
	r.ExternalURL = "https://openlibrary.org/works/" + url.PathEscape(id)
	if r.ISBN != "" {
		r.ExternalIDs["isbn"] = r.ISBN
	}
	r.Genres, r.Tags = olTerms(w.Subjects)
	if len(r.ExternalIDs) == 0 {
		r.ExternalIDs = nil
	}
	return bookData(r), nil
}

func (c *openLibraryClient) credits(ctx context.Context, w olWork) ([]plugin.Credit, error) {
	var out []plugin.Credit
	for _, a := range w.Authors {
		id := olID(a.Author.Key, "/authors/")
		if id == "" {
			continue
		}
		au, err := c.author(ctx, id)
		if errors.Is(err, plugin.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		name := plugin.SingleLine(au.Name, maxNameRunes)
		if name == "" {
			continue
		}
		p := plugin.PersonRef{ID: id, Name: name, URL: "https://openlibrary.org/authors/" + url.PathEscape(id)}
		if len(au.Photos) > 0 && au.Photos[0] > 0 {
			p.ImageURL = fmt.Sprintf("https://covers.openlibrary.org/a/olid/%s-M.jpg", url.PathEscape(id))
		}
		out = append(out, plugin.Credit{Role: "author", Person: p})
	}
	return out, nil
}

func (c *openLibraryClient) people(ctx context.Context, id string) ([]plugin.Credit, error) {
	w, err := c.work(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.credits(ctx, w)
}

func (c *openLibraryClient) lookupISBN(ctx context.Context, isbn string) (plugin.ItemData, error) {
	isbn = strings.NewReplacer("-", "", " ", "").Replace(isbn)
	if isbn == "" {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	var ed olEdition
	if err := c.get(ctx, "/isbn/"+url.PathEscape(isbn)+".json", &ed); err != nil {
		return plugin.ItemData{}, err
	}
	if len(ed.Works) == 0 {
		return plugin.ItemData{}, fmt.Errorf("%w: openlibrary ISBN %s has no work", plugin.ErrBadResponse, isbn)
	}
	id := olID(ed.Works[0].Key, "/works/")
	if id == "" {
		return plugin.ItemData{}, fmt.Errorf("%w: openlibrary ISBN %s has no work id", plugin.ErrBadResponse, isbn)
	}
	w, err := c.work(ctx, id)
	if err != nil {
		return plugin.ItemData{}, err
	}
	return c.record(ctx, id, w, &ed)
}

type openLibrarySource struct{ c *openLibraryClient }

func (s *openLibrarySource) Search(ctx context.Context, kind, q string) ([]plugin.ItemSummary, error) {
	if kind != KindBook {
		return nil, plugin.ErrNotFound
	}
	return s.c.search(ctx, q)
}

func (s *openLibrarySource) Details(ctx context.Context, kind, id string) (plugin.ItemData, error) {
	if kind != KindBook {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	return s.c.details(ctx, olID(id, "/works/"))
}

func (s *openLibrarySource) People(ctx context.Context, kind, id string) ([]plugin.Credit, error) {
	if kind != KindBook {
		return nil, plugin.ErrNotFound
	}
	return s.c.people(ctx, olID(id, "/works/"))
}

func (s *openLibrarySource) Recommendations(context.Context, string, string) ([]plugin.ItemSummary, error) {
	return nil, nil
}

func (s *openLibrarySource) Lookup(ctx context.Context, kind, idType, value string) (plugin.ItemData, error) {
	if kind != KindBook {
		return plugin.ItemData{}, plugin.ErrNotFound
	}
	if idType != "isbn" {
		return plugin.ItemData{}, fmt.Errorf("openlibrary: cannot look up %q ids", idType)
	}
	return s.c.lookupISBN(ctx, value)
}

func olID(raw, prefix string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, prefix)
	s = strings.TrimPrefix(s, "/")
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_' || r == '-') {
			return ""
		}
	}
	return s
}

func olCover(id int) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", id)
}

func chooseEdition(eds []olEdition) *olEdition {
	var english, englishPages, anyPages, anyCover *olEdition
	for i := range eds {
		e := &eds[i]
		if anyPages == nil {
			anyPages = e
		}
		if len(e.Covers) > 0 && anyCover == nil {
			anyCover = e
		}
		if e.NumberOfPages <= 0 {
			continue
		}
		if englishPages == nil {
			englishPages = e
		}
		if marcLanguage(e.Languages) == "en" {
			if english == nil {
				english = e
			}
			if len(e.Covers) > 0 {
				englishPages = e
			}
		}
	}
	switch {
	case englishPages != nil:
		return englishPages
	case english != nil:
		return english
	case anyCover != nil:
		return anyCover
	case anyPages != nil:
		return anyPages
	}
	return nil
}

func marcLanguage(ls []olLanguage) string {
	if len(ls) == 0 {
		return ""
	}
	code := strings.TrimPrefix(ls[0].Key, "/languages/")
	if v, ok := marcLanguages[strings.ToLower(code)]; ok {
		return v
	}
	return ""
}

var marcLanguages = map[string]string{
	"eng": "en", "ger": "de", "deu": "de", "fre": "fr", "fra": "fr",
	"spa": "es", "ita": "it", "por": "pt", "rus": "ru", "jpn": "ja",
	"chi": "zh", "zho": "zh", "kor": "ko", "nld": "nl", "dut": "nl",
	"pol": "pl", "swe": "sv", "dan": "da", "nor": "no", "fin": "fi",
	"tur": "tr", "ara": "ar", "heb": "he", "hin": "hi", "tha": "th",
	"vie": "vi", "ind": "id",
}

func olDescription(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return anilist.PlainText(text)
	}
	var obj struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return anilist.PlainText(obj.Value)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func bookDate(raw string) (int, *time.Time) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "January 2, 2006", "Jan 2, 2006"} {
		if t, err := time.Parse(layout, raw); err == nil {
			day := plugin.DateOnly(t)
			return t.Year(), &day
		}
	}
	for i := 0; i+4 <= len(raw); i++ {
		if raw[i] >= '0' && raw[i] <= '9' && raw[i+1] >= '0' && raw[i+1] <= '9' &&
			raw[i+2] >= '0' && raw[i+2] <= '9' && raw[i+3] >= '0' && raw[i+3] <= '9' {
			if y, err := strconv.Atoi(raw[i : i+4]); err == nil && y >= 1000 && y <= 2100 {
				return y, nil
			}
		}
	}
	return 0, nil
}

var olGenreNames = map[string]string{
	"fantasy": "Fantasy", "fantasy fiction": "Fantasy",
	"science fiction": "Science Fiction", "science-fiction": "Science Fiction",
	"mystery": "Mystery", "detective and mystery stories": "Mystery",
	"horror": "Horror", "romance": "Romance", "historical fiction": "Historical Fiction",
	"biography": "Biography", "autobiography": "Autobiography", "poetry": "Poetry",
	"drama": "Drama", "adventure": "Adventure", "thriller": "Thriller",
	"nonfiction": "Nonfiction", "philosophy": "Philosophy", "psychology": "Psychology",
	"history": "History", "science": "Science", "travel": "Travel",
	"cooking": "Cooking", "art": "Art", "music": "Music", "religion": "Religion",
	"business": "Business", "economics": "Economics", "politics": "Politics",
	"education": "Education", "health": "Health", "sports": "Sports",
	"nature": "Nature", "classics": "Classics", "young adult": "Young Adult",
}

func olTerms(subjects []string) ([]string, []plugin.Tag) {
	var genres []string
	var tags []plugin.Tag
	for _, raw := range subjects {
		s := plugin.SingleLine(raw, maxTermRunes)
		if s == "" {
			continue
		}
		low := strings.ToLower(s)
		if strings.ContainsAny(s, ":=") || strings.Contains(low, "new york times") ||
			strings.Contains(low, "large type books") || strings.Contains(low, "protected daisy") ||
			strings.Contains(low, "accessible book") || strings.Contains(low, "in library") ||
			strings.Contains(low, "overdrive") || strings.Contains(low, "reading level") ||
			strings.Contains(low, "bestseller") {
			continue
		}
		if g, ok := olGenreNames[low]; ok {
			genres = append(genres, g)
			continue
		}
		tags = append(tags, plugin.Tag{Name: s})
	}
	return cleanTerms(genres, maxGenres), cleanTags(tags)
}
