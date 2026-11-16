package web

// The run routes of an entry, core routes guarded by the plugin of the item
// {id} like the other entry routes: POST /list/{id}/runs starts the entry
// again (a new run, today in the configured time zone), GET|POST
// /list/{id}/runs/{run}/edit edits one run of the history (status, rating,
// dates and review; a past run only completed or dropped) and POST
// /list/{id}/runs/{run}/delete deletes one (never the only run). The display
// run is edited in the entry dialog; the history links it there.

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

// runView is one run of the item page's history, newest first.
type runView struct {
	Number                    int
	Status, StatusKey         string
	Started, Finished, Rating string
	Review                    string
	Display                   bool   // the run the entry shows
	EditURL                   string // the run's form (the entry dialog for the display run)
	DeleteURL, DeleteConfirm  string // "" with one run (deleting it is removing the entry)
	Return                    string
	TimelineURL               string // the lazily loaded timeline fragment
}

// runDialog is the data of run_dialog (run_history.html).
type runDialog struct {
	Action, CancelURL, Return, Heading string
	Statuses                           []statusOpt
	Rating, StartedOn, FinishedOn      string
	Review                             string
	MaxReview                          int // the review's maxlength
	Error                              string
}

// runTarget is a run of a guarded entry, with all the entry's runs.
type runTarget struct {
	*entryTarget
	run  library.Run
	runs []library.Run
}

// latest reports whether the target is the entry's latest run (the only one
// that takes any status).
func (t *runTarget) latest() bool { return len(t.runs) > 0 && t.runs[0].ID == t.run.ID }

// overview is the item page of the target.
func (t *entryTarget) overview() string {
	it := t.row.Item
	return itemURL(t.man, it.Source, it.SourceID, it.Kind)
}

// runReturn is the return path of a run route: the posted or queried path,
// the item page when none is given.
func (t *entryTarget) runReturn(raw string) string {
	if raw == "" {
		return t.overview()
	}
	return returnTarget(raw, t.man.ID)
}

// postStartRun starts the entry again. It answers like +1: 303 to the
// return path, for htmx the re-rendered row or card. A display run that is
// not completed is refused (400).
func (s *server) postStartRun(w http.ResponseWriter, r *http.Request) {
	ref := entryOf(r)
	if !parseForm(w, r) {
		return
	}
	ctx := r.Context()
	_, err := s.deps.Library.StartRun(ctx, ref.ID, s.now().In(s.location(ctx)))
	switch {
	case errors.Is(err, library.ErrNoEntry):
		s.entryNotFound(w, r)
		return
	case errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusBadRequest, "Cannot start again", invalidMessage(err), "")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.entryChanged(w, r, returnTarget(r.PostFormValue("return"), ref.PluginID))
}

// loadRun loads the guarded entry and its run {run}; a malformed, stale or
// foreign run id gets the 404 page.
func (s *server) loadRun(w http.ResponseWriter, r *http.Request) (*runTarget, bool) {
	raw := r.PathValue("run")
	var id int64
	if isASCIIDigits(raw, maxIDDigits) {
		id, _ = strconv.ParseInt(raw, 10, 64)
	}
	if id <= 0 {
		s.runNotFound(w, r)
		return nil, false
	}
	t, ok := s.loadEntry(w, r)
	if !ok {
		return nil, false
	}
	runs, err := s.deps.Library.Runs(r.Context(), t.row.Item.ID)
	if err != nil {
		s.internalError(w, r, err)
		return nil, false
	}
	for _, run := range runs {
		if run.ID == id {
			return &runTarget{entryTarget: t, run: run, runs: runs}, true
		}
	}
	s.runNotFound(w, r)
	return nil, false
}

func (s *server) runNotFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such run of this entry.", "")
}

// runStatuses are the statuses a run's form offers: any for the latest run,
// completed or dropped for a past one (library.UpdateRun's rule).
func (t *runTarget) runStatuses() []library.Status {
	if t.latest() {
		return library.Statuses()
	}
	return []library.Status{library.Completed, library.Dropped}
}

// dialog completes the run form d (its typed values) with the status
// options, status selected.
func (t *runTarget) dialog(status string, d runDialog, ret string) runDialog {
	d.Action = "/list/" + strconv.FormatInt(t.row.Item.ID, 10) + "/runs/" + strconv.FormatInt(t.run.ID, 10) + "/edit"
	d.CancelURL, d.Return = ret, ret
	d.Heading = "Edit run " + strconv.Itoa(t.run.Number)
	d.MaxReview = library.MaxReviewRunes
	d.Statuses = nil
	for _, st := range t.runStatuses() {
		d.Statuses = append(d.Statuses, statusOpt{Value: string(st), Label: statusLabel(t.kind, st), Selected: string(st) == status})
	}
	return d
}

// runResponse writes the run form (htmx) or the item page with it open.
func (s *server) runResponse(w http.ResponseWriter, r *http.Request, t *runTarget, d runDialog, code int) {
	if isPartialRequest(r) {
		s.renderPartialStatus(w, code, "item", "run_dialog", d)
		return
	}
	it := t.row.Item
	s.showItem(w, r, itemRef{Plugin: t.man.ID, Source: it.Source, ID: it.SourceID, Kind: it.Kind}, itemExtras{RunDialog: &d}, code)
}

func (s *server) getRunEdit(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	run := t.run
	d := runDialog{StartedOn: fmtDay(run.StartedOn), FinishedOn: fmtDay(run.FinishedOn), Review: run.Review}
	if run.Rating != nil {
		d.Rating = strconv.Itoa(*run.Rating)
	}
	s.runResponse(w, r, t, t.dialog(string(run.Status), d, t.runReturn(r.URL.Query().Get("return"))), http.StatusOK)
}

// postRunEdit replaces the run's status, rating, dates and review; the
// library refuses a status a past run cannot take (422 with the form).
func (s *server) postRunEdit(w http.ResponseWriter, r *http.Request) {
	if !parseTextForm(w, r) {
		return
	}
	t, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	get := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	status := get("status")
	d := runDialog{Rating: get("rating"), StartedOn: get("started_on"), FinishedOn: get("finished_on"), Review: get("review")}
	ret := t.runReturn(r.PostFormValue("return"))
	u, msg := parseRunForm(status, d)
	if msg == "" {
		err := s.deps.Library.UpdateRun(r.Context(), t.run.ID, u)
		switch {
		case err == nil:
			s.done(w, r, ret)
			return
		case errors.Is(err, library.ErrNoRun):
			s.runNotFound(w, r)
			return
		case errors.Is(err, library.ErrInvalid):
			msg = invalidMessage(err)
		default:
			s.internalError(w, r, err)
			return
		}
	}
	d = t.dialog(status, d, ret)
	d.Error = msg
	s.runResponse(w, r, t, d, http.StatusUnprocessableEntity)
}

// parseRunForm reads the run form's values; msg is a user-facing refusal.
func parseRunForm(status string, d runDialog) (library.RunUpdate, string) {
	u := library.RunUpdate{Status: library.Status(status), Review: d.Review}
	if !slices.Contains(library.Statuses(), u.Status) {
		return u, "Please choose a status."
	}
	if d.Rating != "" {
		n, ok := parseWhole(d.Rating, 2)
		if !ok || n > 10 {
			return u, "Rating must be a whole number from 0 to 10, or blank for unrated."
		}
		rating := int(n)
		u.Rating = &rating
	}
	var msg string
	if u.StartedOn, msg = parseDay(d.StartedOn, "Start date"); msg != "" {
		return u, msg
	}
	if u.FinishedOn, msg = parseDay(d.FinishedOn, "Finish date"); msg != "" {
		return u, msg
	}
	return u, ""
}

// postRunDelete deletes the run; the later runs renumber. The only run is
// refused (400): that is removing the entry.
func (s *server) postRunDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	t, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	err := s.deps.Library.DeleteRun(r.Context(), t.run.ID)
	switch {
	case errors.Is(err, library.ErrNoRun):
		s.runNotFound(w, r)
		return
	case errors.Is(err, library.ErrInvalid):
		s.errorPage(w, r, http.StatusBadRequest, "Cannot delete this run", invalidMessage(err), "")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.done(w, r, t.runReturn(r.PostFormValue("return")))
}

// runHistory builds the item page's run history of the listed item it
// (newest first); overview is the page's own path, the forms' return.
func (s *server) runHistory(ctx context.Context, k plugin.KindDef, it *library.Item, e *library.Entry, overview string) ([]runView, error) {
	runs, err := s.deps.Library.Runs(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	base := "/list/" + strconv.FormatInt(it.ID, 10)
	q := "?" + url.Values{"return": {overview}}.Encode()
	out := make([]runView, 0, len(runs))
	for _, run := range runs {
		v := runView{Number: run.Number, Status: statusLabel(k, run.Status), StatusKey: string(run.Status),
			Started: fmtDay(run.StartedOn), Finished: fmtDay(run.FinishedOn), Rating: "–", Review: run.Review,
			Display: run.ID == e.RunID, Return: overview, TimelineURL: timelineURL(it.ID, run.ID)}
		if run.Rating != nil {
			v.Rating = strconv.Itoa(*run.Rating)
		}
		runBase := base + "/runs/" + strconv.FormatInt(run.ID, 10)
		if v.Display {
			v.EditURL = base + "/edit" + q
		} else {
			v.EditURL = runBase + "/edit" + q
		}
		if len(runs) > 1 {
			v.DeleteURL = runBase + "/delete"
			v.DeleteConfirm = "Delete run " + strconv.Itoa(run.Number) + " of this " + lowerFirst(k.Label) + "? Its dates, rating, review and progress are lost."
		}
		out = append(out, v)
	}
	return out, nil
}
