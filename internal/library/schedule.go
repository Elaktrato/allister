package library

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/allister/allister/internal/plugin"
)

// scheduleLayout is how stored schedule timestamps are written and read
// (the same layout the job queue uses).
const scheduleLayout = "2006-01-02T15:04:05Z"

// Airing is one stored release air date of an item.
type Airing struct {
	Season int
	Number int
	AirsAt time.Time
}

// ScheduledEntry is one stored airing of an active entry with what the
// calendar feed needs to label it.
type ScheduledEntry struct {
	ItemID           int64
	PluginID, Kind   string
	Source, SourceID string
	Titles           []plugin.Title
	Season           int
	Number           int
	AirsAt           time.Time
	// RunNumber and Progress describe the entry's display run.
	RunNumber int
	Progress  map[string]float64
	// ProgressKeys are the item's active progress tracks.
	ProgressKeys []string
	// Totals are the totals of the item's bounded tracks (a total field above
	// 0), by track key.
	Totals map[string]float64
}

// StaleSchedule names an active entry whose schedule was never stored or is
// older than the caller's cutoff, with the plugin and source to ask.
type StaleSchedule struct {
	ItemID   int64
	PluginID string
	Source   string
}

// SaveSchedule replaces the stored airing schedule of itemID and records
// when it was fetched. An empty schedule is recorded too (the marker says the
// item was asked), so an item without upcoming releases is not fetched on
// every calendar poll.
func (l *Library) SaveSchedule(ctx context.Context, itemID int64, airings []Airing, syncedAt time.Time) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM airings WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	for _, a := range airings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO airings(item_id, season, number, airs_at) VALUES (?, ?, ?, ?)`,
			itemID, a.Season, a.Number, a.AirsAt.UTC().Format(scheduleLayout)); err != nil {
			return fmt.Errorf("save schedule: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO item_schedules(item_id, synced_at) VALUES (?, ?)
		ON CONFLICT(item_id) DO UPDATE SET synced_at = excluded.synced_at`,
		itemID, syncedAt.UTC().Format(scheduleLayout)); err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	return nil
}

// ScheduleEntries returns the stored airings of every active entry, titles
// included (rowid order), ordered by air time. The caller filters by source
// capability: the library does not know the manifests.
func (l *Library) ScheduleEntries(ctx context.Context) ([]ScheduledEntry, error) {
	return l.scheduleQuery(ctx, "", nil, -1)
}

// ScheduleSource names a plugin's source (the pair the airings are read by).
type ScheduleSource struct{ PluginID, Source string }

// ScheduleBetween is ScheduleEntries bounded: the airings of active entries
// with from <= airs_at < to whose item belongs to one of the listed
// plugin/source pairs, in air order, at most limit.
func (l *Library) ScheduleBetween(ctx context.Context, from, to time.Time, sources []ScheduleSource, limit int) ([]ScheduledEntry, error) {
	if len(sources) == 0 || limit <= 0 {
		return nil, nil
	}
	args := []any{from.UTC().Format(scheduleLayout), to.UTC().Format(scheduleLayout)}
	pairs := make([]string, 0, len(sources))
	for _, s := range sources {
		pairs = append(pairs, "(i.plugin_id = ? AND i.source = ?)")
		args = append(args, s.PluginID, s.Source)
	}
	return l.scheduleQuery(ctx, " AND a.airs_at >= ? AND a.airs_at < ? AND ("+strings.Join(pairs, " OR ")+")", args, limit)
}

// scheduleQuery loads the airings of active entries matching the extra
// WHERE terms (constant SQL, bound args), titles included.
func (l *Library) scheduleQuery(ctx context.Context, extra string, args []any, limit int) ([]ScheduledEntry, error) {
	//nolint:gosec // G202: constant SQL fragments; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT a.item_id, i.plugin_id, i.kind, i.source, i.source_id, a.season, a.number, a.airs_at, e.run_number, i.progress_keys
		FROM airings a
		JOIN items i ON i.id = a.item_id
		JOIN `+viewEntryState+` e ON e.item_id = a.item_id
		WHERE e.status = ?`+extra+`
		ORDER BY a.airs_at, a.item_id, a.season, a.number LIMIT ?`, append(append([]any{string(Active)}, args...), limit)...)
	if err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	var out []ScheduledEntry
	ids := map[int64]bool{}
	for rows.Next() {
		var e ScheduledEntry
		var airs, keys string
		if err := rows.Scan(&e.ItemID, &e.PluginID, &e.Kind, &e.Source, &e.SourceID, &e.Season, &e.Number, &airs, &e.RunNumber, &keys); err != nil {
			rows.Close()
			return nil, fmt.Errorf("schedule entries: %w", err)
		}
		if err := decodeKeys(keys, &e.ProgressKeys); err != nil {
			rows.Close()
			return nil, err
		}
		if e.AirsAt, err = time.Parse(scheduleLayout, airs); err != nil {
			rows.Close()
			return nil, fmt.Errorf("schedule entries: %w", err)
		}
		ids[e.ItemID] = true
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	if len(ids) == 0 {
		return out, nil
	}
	list := make([]any, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	trows, err := l.db.QueryContext(ctx, `SELECT item_id, lang, text FROM item_titles
		WHERE item_id IN (`+placeholders(len(list))+`) ORDER BY item_id, rowid`, list...)
	if err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	defer trows.Close()
	titles := map[int64][]plugin.Title{}
	for trows.Next() {
		var id int64
		var t plugin.Title
		if err := trows.Scan(&id, &t.Lang, &t.Text); err != nil {
			return nil, fmt.Errorf("schedule entries: %w", err)
		}
		titles[id] = append(titles[id], t)
	}
	if err := trows.Err(); err != nil {
		return nil, fmt.Errorf("schedule entries: %w", err)
	}
	progress, err := l.displayProgress(ctx, list)
	if err != nil {
		return nil, err
	}
	totals, err := l.itemTotals(ctx, list)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Totals = totals[out[i].ItemID]
		if def, err := l.kindDef(out[i].PluginID, out[i].Kind); err == nil {
			out[i].Totals = scheduledTotals(def, totals[out[i].ItemID])
		}
		out[i].Titles = titles[out[i].ItemID]
		out[i].Progress = progress[out[i].ItemID]
		if out[i].Progress == nil {
			out[i].Progress = map[string]float64{}
		}
	}
	return out, nil
}

// displayProgress reads the stored progress values of the display run of
// each listed item (ids are bound as arguments).
func (l *Library) displayProgress(ctx context.Context, ids []any) (map[int64]map[string]float64, error) {
	out := map[int64]map[string]float64{}
	if len(ids) == 0 {
		return out, nil
	}
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT item_id, key, value FROM `+viewDisplayProgress+`
		WHERE item_id IN (`+placeholders(len(ids))+`)`, ids...)
	if err != nil {
		return nil, fmt.Errorf("display progress: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var k string
		var v float64
		if err := rows.Scan(&id, &k, &v); err != nil {
			return nil, fmt.Errorf("display progress: %w", err)
		}
		if out[id] == nil {
			out[id] = map[string]float64{}
		}
		out[id][k] = v
	}
	return out, rows.Err()
}

// Beyond reports whether the release (season, number) lies beyond progress.
// With a group track and a grouped release (season > 0) the pair is compared
// lexicographically against (group value, primary value); otherwise only the
// number is compared with the primary value. Missing progress counts as 0,
// except an unset group value, which counts as the first group (a count
// advanced with +1 never sets the group track).
func Beyond(def plugin.KindDef, progress map[string]float64, season, number int) bool {
	ps := def.Progress
	if ps.PrimaryKey == "" {
		return false
	}
	n := progress[ps.PrimaryKey]
	if ps.GroupKey != "" && season > 0 {
		g := math.Max(progress[ps.GroupKey], 1)
		if float64(season) != g {
			return float64(season) > g
		}
	}
	return float64(number) > n
}

// MarkableTracks reports whether every progress track that marking the release
// of season watched sets is active: the primary track (a count or pages
// track), and the group track when the kind has one and the release carries a
// season. A season above the group track's total (totals by track key, an
// absent key means unbounded) cannot be stored, so it is not markable either.
// A kind without a primary track has nothing to set.
func MarkableTracks(def plugin.KindDef, active []string, totals map[string]float64, season int) bool {
	ps := def.Progress
	pd, ok := progressDef(def, ps.PrimaryKey)
	if ps.PrimaryKey == "" || !ok || !slices.Contains(active, ps.PrimaryKey) || (pd.Type != plugin.Count && pd.Type != plugin.Pages) {
		return false
	}
	if ps.GroupKey == "" || season <= 0 {
		return true
	}
	if t, bounded := totals[ps.GroupKey]; bounded && t > 0 && float64(season) > t {
		return false
	}
	return slices.Contains(active, ps.GroupKey)
}

// trackTotals returns the totals of the item's bounded tracks by track key.
func trackTotals(def plugin.KindDef, item *Item) map[string]float64 {
	out := map[string]float64{}
	for _, d := range def.Progress.Eligible {
		if total, bounded := progressBound(d, item); bounded {
			out[d.Key] = total
		}
	}
	return out
}

// AiredCounts returns, per listed item, how many stored releases have aired
// (airs_at <= now) beyond the display run's progress. Only active entries on
// their first run are counted, and only releases the calendar can mark
// (MarkableTracks: the tracks the release sets are active for the item and the
// season is within the group total); every other item is absent from the map (no
// hint), as is an item whose plugin or kind is not installed.
func (l *Library) AiredCounts(ctx context.Context, itemIDs []int64, now time.Time) (map[int64]int, error) {
	out := map[int64]int{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	list := make([]any, len(itemIDs))
	for i, id := range itemIDs {
		list[i] = id
	}
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT i.id, i.plugin_id, i.kind, i.progress_keys
		FROM items i JOIN `+viewEntryState+` e ON e.item_id = i.id
		WHERE e.status = ? AND e.run_number = 1 AND i.id IN (`+placeholders(len(list))+`)`,
		append([]any{string(Active)}, list...)...)
	if err != nil {
		return nil, fmt.Errorf("aired counts: %w", err)
	}
	type counted struct {
		def    plugin.KindDef
		keys   []string
		totals map[string]float64
	}
	items := map[int64]*counted{}
	var ids []any
	for rows.Next() {
		var id int64
		var pluginID, kind, pk string
		if err := rows.Scan(&id, &pluginID, &kind, &pk); err != nil {
			rows.Close()
			return nil, fmt.Errorf("aired counts: %w", err)
		}
		def, err := l.kindDef(pluginID, kind)
		if err != nil {
			continue
		}
		c := &counted{def: def}
		if err := decodeKeys(pk, &c.keys); err != nil {
			rows.Close()
			return nil, fmt.Errorf("aired counts: %w", err)
		}
		items[id] = c
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("aired counts: %w", err)
	}
	for id, c := range items {
		it, err := l.Item(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("aired counts: %w", err)
		}
		if it != nil {
			c.totals = trackTotals(c.def, it)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	progress, err := l.displayProgress(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id.(int64)] = 0
	}
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	arows, err := l.db.QueryContext(ctx, `SELECT item_id, season, number FROM airings
		WHERE airs_at <= ? AND item_id IN (`+placeholders(len(ids))+`)`,
		append([]any{now.UTC().Format(scheduleLayout)}, ids...)...)
	if err != nil {
		return nil, fmt.Errorf("aired counts: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var id int64
		var season, number int
		if err := arows.Scan(&id, &season, &number); err != nil {
			return nil, fmt.Errorf("aired counts: %w", err)
		}
		c := items[id]
		if MarkableTracks(c.def, c.keys, c.totals, season) && Beyond(c.def, progress[id], season, number) {
			out[id]++
		}
	}
	return out, arows.Err()
}

// StaleSchedules returns the active entries whose schedule was never stored
// or is older than the cutoff, with the plugin and source to ask.
func (l *Library) StaleSchedules(ctx context.Context, olderThan time.Time) ([]StaleSchedule, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT e.item_id, i.plugin_id, i.source
		FROM `+viewEntryState+` e
		JOIN items i ON i.id = e.item_id
		LEFT JOIN item_schedules s ON s.item_id = e.item_id
		WHERE e.status = ? AND (s.synced_at IS NULL OR s.synced_at < ?)
		ORDER BY e.item_id`, string(Active), olderThan.UTC().Format(scheduleLayout))
	if err != nil {
		return nil, fmt.Errorf("stale schedules: %w", err)
	}
	defer rows.Close()
	var out []StaleSchedule
	for rows.Next() {
		var s StaleSchedule
		if err := rows.Scan(&s.ItemID, &s.PluginID, &s.Source); err != nil {
			return nil, fmt.Errorf("stale schedules: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// itemTotals reads the numeric fields above 0 of each listed item (the first
// value of a field wins, like the entry checks), by item id and field key.
func (l *Library) itemTotals(ctx context.Context, ids []any) (map[int64]map[string]float64, error) {
	out := map[int64]map[string]float64{}
	//nolint:gosec // G202: constant SQL and placeholders; values are bound
	rows, err := l.db.QueryContext(ctx, `SELECT item_id, key, num FROM item_fields
		WHERE num > 0 AND item_id IN (`+placeholders(len(ids))+`) ORDER BY item_id, seq`, ids...)
	if err != nil {
		return nil, fmt.Errorf("item totals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var k string
		var n float64
		if err := rows.Scan(&id, &k, &n); err != nil {
			return nil, fmt.Errorf("item totals: %w", err)
		}
		if out[id] == nil {
			out[id] = map[string]float64{}
		}
		if _, seen := out[id][k]; !seen {
			out[id][k] = n
		}
	}
	return out, rows.Err()
}

// scheduledTotals maps an item's field values to the totals of its bounded
// tracks by track key.
func scheduledTotals(def plugin.KindDef, fields map[string]float64) map[string]float64 {
	it := &Item{Fields: map[string]plugin.FieldValue{}}
	for k, n := range fields {
		it.Fields[k] = plugin.FieldValue{Num: &n}
	}
	return trackTotals(def, it)
}
