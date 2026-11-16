package web

// Bulk edit: POST /{plugin}/bulk changes many entries of a plugin's list at
// once. The bar under the list (bulk_bar.html) posts the action, its value,
// the list's own URL parameters as one query string (list) and either the
// ticked item ids (ids, repeated) or scope=filter: the list parameters are
// parsed exactly like the list page's and, for scope=filter, re-run at apply
// time, so the selection is what the list shows. The
// first post answers a confirmation naming the count; the confirmation
// posts confirm=1 with expect, the count the owner saw: a count that
// changed since is shown again and nothing is written. Applying answers the
// list with the notice "Changed N, skipped M". Each action is one
// transaction of the library's Bulk* calls.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// bulkSkipsShown is the number of skipped entries the notice names.
const bulkSkipsShown = 5

// reasonNotInThisList is the skip reason of an id of another plugin.
const reasonNotInThisList = "not in this list"

// bulkParam is one hidden field (a chosen action or value).
type bulkParam struct{ Name, Value string }

// bulkBar is the data of the bulk edit bar and of the confirmation's
// editable fields: the route, the list's parameters (List, a canonical query
// string), the number of matching entries and the actions with their
// choices (Selected marks the posted values when the form is shown again).
type bulkBar struct {
	Action              string
	List                string
	Total               int
	Actions             []statusOpt
	Statuses            []statusOpt
	Lists               []statusOpt
	Series              []statusOpt
	AddTags, RemoveTags string
}

// bulkNotice reports an applied bulk change on the reloaded list;
// ReplaceURL is the list's canonical URL (app.js puts it in the address bar).
type bulkNotice struct {
	Text       string
	Skips      []string // "<title>: <reason>", at most bulkSkipsShown
	More       int      // skipped entries beyond Skips
	ReplaceURL string
}

// bulkConfirmView is the data of bulk_confirm.html: the question with the
// count, or (Error set) the editable fields with the message.
type bulkConfirmView struct {
	Title, Summary, Submit string
	Bar                    bulkBar
	Scope                  string
	IDs                    []string
	Hidden                 []bulkParam // the chosen action and value
	Count                  int
	CountText              string // "1 entry", "<n> entries"
	Changed                bool   // the count differs from the one confirmed
	Error                  string
	CancelURL              string
}

// bulkListParams are the list parameters of a parsed list request as a
// canonical query string, the status always named ("all" for every status)
// so a filter selection never falls back to the home status.
func bulkListParams(m plugin.Manifest, k plugin.KindDef, lq listQuery, page int) string {
	v := lq.Filters.values()
	if len(m.Kinds) > 1 {
		v.Set("kind", k.Key)
	}
	st := lq.Status
	if st == "" {
		st = "all"
	}
	v.Set("status", st)
	if lq.View != "" {
		v.Set("view", lq.View)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	return v.Encode()
}

// bulkBar builds the bar of a list: the statuses of the kind, the custom
// lists and the series of this plugin and kind (the actions without a
// choice are left out).
func (s *server) bulkBar(ctx context.Context, m plugin.Manifest, k plugin.KindDef, lq listQuery, page, total int) (*bulkBar, error) {
	b := &bulkBar{Action: "/" + url.PathEscape(m.ID) + "/bulk", List: bulkListParams(m, k, lq, page), Total: total}
	for _, st := range library.Statuses() {
		b.Statuses = append(b.Statuses, statusOpt{Value: string(st), Label: statusLabel(k, st)})
	}
	lists, err := s.deps.Library.Lists(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range lists {
		b.Lists = append(b.Lists, statusOpt{Value: strconv.FormatInt(l.ID, 10), Label: l.Name})
	}
	for p, pages := 1, 1; p <= pages; p++ {
		pg, err := s.deps.Library.ListSeries(ctx, library.SeriesQuery{PluginID: m.ID, Sort: "title", Page: p, PageSize: listPageSize})
		if err != nil {
			return nil, err
		}
		pages = pg.Pages
		for _, row := range pg.Rows {
			if row.Series.Kind == k.Key {
				b.Series = append(b.Series, statusOpt{Value: strconv.FormatInt(row.Series.ID, 10), Label: row.Series.Title})
			}
		}
	}
	b.Actions = append(b.Actions, statusOpt{Value: "status", Label: "Set status"})
	if len(b.Lists) > 0 {
		b.Actions = append(b.Actions, statusOpt{Value: "list", Label: "Add to list"})
	}
	b.Actions = append(b.Actions, statusOpt{Value: "tags", Label: "Add or remove tags"})
	if len(b.Series) > 0 {
		b.Actions = append(b.Actions, statusOpt{Value: "series", Label: "Add to series"})
	}
	b.Actions = append(b.Actions, statusOpt{Value: "remove", Label: "Remove from library"})
	return b, nil
}

// choose marks the option with value v and returns its label ("" when no
// option has it).
func choose(opts []statusOpt, v string) string {
	label := ""
	for i := range opts {
		opts[i].Selected = v != "" && opts[i].Value == v
		if opts[i].Selected {
			label = opts[i].Label
		}
	}
	return label
}

// splitTags reads a comma separated tag field (blank names dropped).
func splitTags(raw string) []string {
	var out []string
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// entriesNoun is "1 entry" or "<n> entries".
func entriesNoun(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return strconv.Itoa(n) + " entries"
}

// bulkOp is a validated bulk action: run applies it to ids, kind is the
// kind every entry must have ("" for any), the rest words the confirmation.
type bulkOp struct {
	run     func(ctx context.Context, ids []int64) (library.BulkResult, error)
	kind    string
	summary string
	values  []bulkParam
	remove  bool
}

// parseBulkOp checks the posted action and its value against the bar's
// choices; msg is the refusal shown with the form again.
func (s *server) parseBulkOp(r *http.Request, k plugin.KindDef, b *bulkBar) (op bulkOp, msg string) {
	f := r.PostForm
	act := f.Get("action")
	b.AddTags, b.RemoveTags = f.Get("add_tags"), f.Get("remove_tags")
	if choose(b.Actions, act) == "" {
		return op, "Choose an action."
	}
	lib := s.deps.Library
	op.values = []bulkParam{{"action", act}}
	switch act {
	case "status":
		v := f.Get("to_status")
		label := choose(b.Statuses, v)
		if label == "" {
			return op, "Choose a status."
		}
		today := s.now().In(s.location(r.Context()))
		op.run = func(ctx context.Context, ids []int64) (library.BulkResult, error) {
			return lib.BulkSetStatus(ctx, ids, library.Status(v), today)
		}
		op.summary, op.values = "Set the status to "+label+".", append(op.values, bulkParam{"to_status", v})
	case "list":
		v := f.Get("to_list")
		name := choose(b.Lists, v)
		if name == "" {
			return op, "Choose a list."
		}
		id, _ := strconv.ParseInt(v, 10, 64)
		op.run = func(ctx context.Context, ids []int64) (library.BulkResult, error) {
			return lib.BulkAddToList(ctx, ids, id)
		}
		op.summary, op.values = "Add them to the list “"+name+"”.", append(op.values, bulkParam{"to_list", v})
	case "series":
		v := f.Get("to_series")
		title := choose(b.Series, v)
		if title == "" {
			return op, "Choose a series."
		}
		id, _ := strconv.ParseInt(v, 10, 64)
		op.run = func(ctx context.Context, ids []int64) (library.BulkResult, error) {
			return lib.BulkAddToSeries(ctx, ids, id)
		}
		op.kind = k.Key
		op.summary, op.values = "Add them to the series “"+title+"”.", append(op.values, bulkParam{"to_series", v})
	case "tags":
		add, remove := splitTags(b.AddTags), splitTags(b.RemoveTags)
		if len(add) == 0 && len(remove) == 0 {
			return op, "Name at least one tag to add or remove."
		}
		op.run = func(ctx context.Context, ids []int64) (library.BulkResult, error) {
			return lib.BulkTags(ctx, ids, add, remove)
		}
		var parts []string
		if len(add) > 0 {
			parts = append(parts, "add the own tags "+strings.Join(add, ", "))
		}
		if len(remove) > 0 {
			parts = append(parts, "remove the own tags "+strings.Join(remove, ", "))
		}
		op.summary = upperFirst(strings.Join(parts, " and ")) + "."
		op.values = append(op.values, bulkParam{"add_tags", b.AddTags}, bulkParam{"remove_tags", b.RemoveTags})
	case "remove":
		op.run = lib.BulkRemove
		op.remove = true
		op.summary = "Their runs, own tags and places in lists and series are deleted; this cannot be undone."
	}
	return op, ""
}

// upperFirst upper-cases the first letter of an ASCII sentence.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// bulkSelection reads the selection: every entry the list parameters match
// (scope=filter, re-run now) or the posted ids, de-duplicated in order. ok is
// false after answering a malformed id.
func (s *server) bulkSelection(w http.ResponseWriter, r *http.Request, m plugin.Manifest, k plugin.KindDef, lq listQuery) ([]int64, bool) {
	ctx := r.Context()
	if r.PostForm.Get("scope") == "filter" {
		ids, err := s.deps.Library.MatchingIDs(ctx, listLibraryQuery(m, k, lq), s.titleLang(ctx, m))
		if err != nil {
			s.internalError(w, r, err)
			return nil, false
		}
		return ids, true
	}
	var ids []int64
	for _, raw := range r.PostForm["ids"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if !isASCIIDigits(raw, maxIDDigits) || err != nil || id <= 0 {
			http.Error(w, "bad entry id", http.StatusBadRequest)
			return nil, false
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, true
}

// bulkKeep splits ids into those of plugin m (and of kind, when given) and
// skips for the others.
func (s *server) bulkKeep(ctx context.Context, m plugin.Manifest, ids []int64, kind string) ([]int64, []library.BulkSkip, error) {
	var keep []int64
	var skips []library.BulkSkip
	for _, id := range ids {
		pluginID, itemKind := "", ""
		if kind == "" {
			p, err := s.deps.Library.ItemPluginID(ctx, id)
			if err != nil && !errors.Is(err, library.ErrNoEntry) {
				return nil, nil, err
			}
			pluginID = p
		} else {
			it, err := s.deps.Library.Item(ctx, id)
			if err != nil && !errors.Is(err, library.ErrNoEntry) {
				return nil, nil, err
			}
			if it != nil {
				pluginID, itemKind = it.PluginID, it.Kind
			}
		}
		switch {
		case pluginID != m.ID:
			skips = append(skips, library.BulkSkip{ItemID: id, Reason: reasonNotInThisList})
		case kind != "" && itemKind != kind:
			skips = append(skips, library.BulkSkip{ItemID: id, Reason: "not of the series' kind"})
		default:
			keep = append(keep, id)
		}
	}
	return keep, skips, nil
}

// postBulk is POST /{plugin}/bulk (see the top of this file).
func (s *server) postBulk(w http.ResponseWriter, r *http.Request, pluginID string) {
	m, ok := s.deps.Registry.Manifest(pluginID)
	if !ok || len(m.Kinds) == 0 {
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such list.", "")
		return
	}
	if !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	params, err := url.ParseQuery(r.PostForm.Get("list"))
	if err != nil {
		http.Error(w, "bad list parameters", http.StatusBadRequest)
		return
	}
	lq := parseListQuery(m, params)
	lq.Tab = ""
	lq.View = s.resolveView(ctx, m, lq.View)
	k, _ := kindDef(m, lq.Kind)
	bar, err := s.bulkBar(ctx, m, k, lq, lq.Page, 0)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ids, ok := s.bulkSelection(w, r, m, k, lq)
	if !ok {
		return
	}
	cv := bulkConfirmView{Scope: "ids", Count: len(ids), CountText: entriesNoun(len(ids)), Title: "Bulk edit",
		CancelURL: filteredListURL(m, k.Key, lq.Status, lq.View, lq.Page, lq.Filters)}
	if r.PostForm.Get("scope") == "filter" {
		cv.Scope = "filter"
	} else {
		for _, id := range ids {
			cv.IDs = append(cv.IDs, strconv.FormatInt(id, 10))
		}
	}
	op, msg := s.parseBulkOp(r, k, bar)
	cv.Bar = *bar
	refuse := func(msg string) {
		cv.Error = msg
		s.renderStatus(w, r, http.StatusUnprocessableEntity, "bulk_confirm", cv)
	}
	switch {
	case len(ids) == 0:
		refuse("Select at least one entry.")
		return
	case msg != "":
		refuse(msg)
		return
	}
	cv.Hidden = op.values
	cv.Summary = op.summary
	if op.remove {
		cv.Title, cv.Submit = "Remove "+entriesNoun(len(ids))+" from your library?", "Remove "+entriesNoun(len(ids))
	} else {
		cv.Title, cv.Submit = "Change "+entriesNoun(len(ids))+"?", "Change "+entriesNoun(len(ids))
	}
	if r.PostForm.Get("confirm") != "1" {
		s.render(w, r, "bulk_confirm", cv)
		return
	}
	if expect, err := strconv.Atoi(r.PostForm.Get("expect")); err != nil || expect != len(ids) {
		cv.Changed = true
		s.renderStatus(w, r, http.StatusConflict, "bulk_confirm", cv)
		return
	}
	keep, dropped := ids, []library.BulkSkip(nil)
	if cv.Scope == "ids" || op.kind != "" {
		if keep, dropped, err = s.bulkKeep(ctx, m, ids, op.kind); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	res, err := op.run(ctx, keep)
	switch {
	case errors.Is(err, library.ErrNoList):
		refuse("Choose a list.")
		return
	case errors.Is(err, library.ErrNoSeries):
		refuse("Choose a series.")
		return
	case errors.Is(err, library.ErrInvalid):
		refuse(invalidMessage(err))
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	res.Skipped = append(dropped, res.Skipped...)
	v, err := s.buildList(r, m, lq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if v.BulkNotice, err = s.noticeOfBulk(ctx, m, k, res, v.PushURL); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "list", v)
}

// noticeOfBulk words a bulk result: the counts and the first skipped
// entries by title with their reasons.
func (s *server) noticeOfBulk(ctx context.Context, m plugin.Manifest, k plugin.KindDef, res library.BulkResult, ret string) (*bulkNotice, error) {
	n := &bulkNotice{Text: "Changed " + strconv.Itoa(res.Changed) + ", skipped " + strconv.Itoa(len(res.Skipped)), ReplaceURL: ret}
	for i, sk := range res.Skipped {
		if i == bulkSkipsShown {
			n.More = len(res.Skipped) - i
			break
		}
		title, err := s.bulkTitle(ctx, m, k, sk.ItemID)
		if err != nil {
			return nil, err
		}
		n.Skips = append(n.Skips, title+": "+sk.Reason)
	}
	return n, nil
}

// bulkTitle is the shown title of an item (in its own plugin's title
// language when it belongs to another plugin).
func (s *server) bulkTitle(ctx context.Context, m plugin.Manifest, k plugin.KindDef, id int64) (string, error) {
	it, err := s.deps.Library.Item(ctx, id)
	if errors.Is(err, library.ErrNoEntry) || (err == nil && it == nil) {
		return "#" + strconv.FormatInt(id, 10), nil
	}
	if err != nil {
		return "", err
	}
	if other, ok := s.deps.Registry.Manifest(it.PluginID); ok && other.ID != m.ID && len(other.Kinds) > 0 {
		m, k = other, other.Kinds[0]
	}
	if t := s.resolveTitle(ctx, m, k, it.Kind, it.Titles); t != "" {
		return t, nil
	}
	return "(untitled)", nil
}
