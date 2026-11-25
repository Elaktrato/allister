// Package settings is a key/value store backed by the settings table.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// Get returns the value for key; ok is false when the key is not set.
func (s *Store) Get(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// Set stores value for key, overwriting any existing value.
func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// titleLangKey is the settings key holding a plugin's title language.
func titleLangKey(id string) string { return "plugin." + id + ".title_lang" }

// TitleLang returns the title language chosen for plugin id, or "" when none
// is stored (or it is unreadable): the caller then uses the plugin's default.
func (s *Store) TitleLang(ctx context.Context, id string) string {
	v, ok, err := s.Get(ctx, titleLangKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using the plugin default)", titleLangKey(id), err)
		return ""
	}
	if !ok {
		return ""
	}
	return v
}

// SetTitleLang stores the title language of plugin id. The caller checks it
// against the languages the plugin offers.
func (s *Store) SetTitleLang(ctx context.Context, id, lang string) error {
	return s.Set(ctx, titleLangKey(id), lang)
}

// KnownLanguages returns the distinct language tags found in the stored
// titles of plugin id's items, sorted. A plugin's settings page offers them
// next to the languages its manifest declares.
func (s *Store) KnownLanguages(ctx context.Context, pluginID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT t.lang FROM item_titles t JOIN items i ON i.id = t.item_id
		WHERE i.plugin_id = ? ORDER BY t.lang`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// columnsKey is the settings key holding the visible table columns of a
// plugin's kind.
func columnsKey(id, kind string) string { return "plugin." + id + ".columns." + kind }

// importKey is the settings key of one importer option (mode, account or
// schedule).
func importKey(id, importer, field string) string {
	return "plugin." + id + ".import." + importer + "." + field
}

// ImportMode returns the stored import mode of an importer ("skip", "newer",
// "always"; "" when none is stored).
func (s *Store) ImportMode(ctx context.Context, id, importer string) string {
	v, _, err := s.Get(ctx, importKey(id, importer, "mode"))
	if err != nil {
		log.Printf("settings: read import mode %s/%s: %v", id, importer, err)
		return ""
	}
	return v
}

// SetImportMode stores the import mode of an importer (the caller checks it).
func (s *Store) SetImportMode(ctx context.Context, id, importer, mode string) error {
	return s.Set(ctx, importKey(id, importer, "mode"), mode)
}

// ImportAccount returns the stored account name of an account importer.
func (s *Store) ImportAccount(ctx context.Context, id, importer string) string {
	v, _, err := s.Get(ctx, importKey(id, importer, "account"))
	if err != nil {
		log.Printf("settings: read import account %s/%s: %v", id, importer, err)
		return ""
	}
	return v
}

// SetImportAccount stores the account name of an account importer.
func (s *Store) SetImportAccount(ctx context.Context, id, importer, account string) error {
	return s.Set(ctx, importKey(id, importer, "account"), account)
}

// ImportSchedule returns the stored schedule of an account importer ("",
// "off", "daily" or "weekly").
func (s *Store) ImportSchedule(ctx context.Context, id, importer string) string {
	v, _, err := s.Get(ctx, importKey(id, importer, "schedule"))
	if err != nil {
		log.Printf("settings: read import schedule %s/%s: %v", id, importer, err)
		return ""
	}
	return v
}

// SetImportSchedule stores the schedule of an account importer (the caller
// checks it).
func (s *Store) SetImportSchedule(ctx context.Context, id, importer, schedule string) error {
	return s.Set(ctx, importKey(id, importer, "schedule"), schedule)
}

// DefaultIdleDays is a plugin's idle cutoff when none is stored, and
// MaxIdleDays the largest one accepted (0 turns the rule off).
const (
	DefaultIdleDays = 30
	MaxIdleDays     = 3650
)

// idleDaysKey is the settings key of a plugin's idle cutoff in days.
func idleDaysKey(id string) string { return "plugin." + id + ".idle_days" }

// IdleDays is plugin id's idle cutoff in days (0: off). Nothing stored, an
// unreadable setting or a stored value that is not an integer from 0 to
// MaxIdleDays is DefaultIdleDays.
func (s *Store) IdleDays(ctx context.Context, id string) int {
	v, ok, err := s.Get(ctx, idleDaysKey(id))
	if err != nil {
		log.Printf("settings: read idle days %s: %v", id, err)
		return DefaultIdleDays
	}
	if !ok {
		return DefaultIdleDays
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > MaxIdleDays {
		return DefaultIdleDays
	}
	return n
}

// SetIdleDays stores plugin id's idle cutoff; a value outside 0 to
// MaxIdleDays is refused.
func (s *Store) SetIdleDays(ctx context.Context, id string, days int) error {
	if days < 0 || days > MaxIdleDays {
		return fmt.Errorf("settings: idle days %d outside 0 to %d", days, MaxIdleDays)
	}
	return s.Set(ctx, idleDaysKey(id), strconv.Itoa(days))
}

// homeViewKey and homeStatusesKey are the settings keys holding a plugin's
// home list configuration: the view its list opens in and the status it
// opens with.
func homeViewKey(id string) string     { return "plugin." + id + ".home.view" }
func homeStatusesKey(id string) string { return "plugin." + id + ".home.statuses" }

// HomeView returns the view the plugin's list opens in, "cards" or "table".
// Nothing stored (or unreadable) means "cards".
func (s *Store) HomeView(ctx context.Context, id string) string {
	v, ok, err := s.Get(ctx, homeViewKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using cards)", homeViewKey(id), err)
		return "cards"
	}
	if !ok || (v != "cards" && v != "table") {
		return "cards"
	}
	return v
}

// SetHomeView stores the view the plugin's list opens in. The caller checks
// the value ("cards" or "table").
func (s *Store) SetHomeView(ctx context.Context, id, view string) error {
	return s.Set(ctx, homeViewKey(id), view)
}

// HomeStatuses returns the status class the plugin's list opens with, ""
// for every status. The caller validates the value against the core
// statuses (it is stored checked).
func (s *Store) HomeStatuses(ctx context.Context, id string) string {
	v, ok, err := s.Get(ctx, homeStatusesKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using every status)", homeStatusesKey(id), err)
		return ""
	}
	if !ok {
		return ""
	}
	return v
}

// SetHomeStatuses stores the status class the plugin's list opens with (""
// for every status). The caller checks the value.
func (s *Store) SetHomeStatuses(ctx context.Context, id, status string) error {
	return s.Set(ctx, homeStatusesKey(id), status)
}

// Columns returns the visible table columns chosen for kind of plugin id, in
// order (stored as a JSON list of column keys). ok is false when nothing is
// stored or the value is not a JSON list of strings: the kind's defaults
// apply. The caller checks the keys against the columns the kind offers.
func (s *Store) Columns(ctx context.Context, id, kind string) (keys []string, ok bool) {
	v, found, err := s.Get(ctx, columnsKey(id, kind))
	if err != nil {
		log.Printf("settings: read %s: %v (using the defaults)", columnsKey(id, kind), err)
		return nil, false
	}
	if !found {
		return nil, false
	}
	if err := json.Unmarshal([]byte(v), &keys); err != nil || keys == nil {
		log.Printf("settings: %s is not a list of column keys (using the defaults)", columnsKey(id, kind))
		return nil, false
	}
	return keys, true
}

// SetColumns stores the visible table columns of kind of plugin id (an empty
// list shows the title only). The caller has checked the keys.
func (s *Store) SetColumns(ctx context.Context, id, kind string, keys []string) error {
	if keys == nil {
		keys = []string{}
	}
	b, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return s.Set(ctx, columnsKey(id, kind), string(b))
}

// ResetColumns removes the column choice of kind of plugin id (back to the
// kind's defaults).
func (s *Store) ResetColumns(ctx context.Context, id, kind string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, columnsKey(id, kind))
	return err
}

// enabledKey is the settings key holding a plugin's enabled flag.
func enabledKey(id string) string { return "plugin." + id + ".enabled" }

// IsEnabled reports whether plugin id is switched on. A missing (or
// unrecognised) value means "use def", the manifest default. It implements
// plugin.EnabledStore.
func (s *Store) IsEnabled(ctx context.Context, id string, def bool) (bool, error) {
	v, ok, err := s.Get(ctx, enabledKey(id))
	if err != nil {
		log.Printf("settings: read %s: %v (using default %v)", enabledKey(id), err, def)
		return def, err
	}
	switch {
	case !ok:
		return def, nil
	case v == "1":
		return true, nil
	case v == "0":
		return false, nil
	}
	return def, nil
}

// SetEnabled stores the plugin's enabled flag as "1" or "0".
func (s *Store) SetEnabled(ctx context.Context, id string, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return s.Set(ctx, enabledKey(id), v)
}

// sourceFieldKey is the settings key holding one named credential field of
// source id.
func sourceFieldKey(id, field string) string { return "source." + id + "." + field }

// SourceKeyField returns the stored value of field of source id, "" when none
// is set. A read failure is returned, so the caller can tell it from an unset
// field. Values are trimmed only when written (SetSourceKeyField,
// SetSourceKeyFields); a read returns the stored value unchanged.
func (s *Store) SourceKeyField(ctx context.Context, sourceID, field string) (string, error) {
	v, _, err := s.Get(ctx, sourceFieldKey(sourceID, field))
	return v, err
}

// SetSourceKeyField stores the value of field of source id, without
// surrounding white space.
func (s *Store) SetSourceKeyField(ctx context.Context, sourceID, field, value string) error {
	return s.Set(ctx, sourceFieldKey(sourceID, field), strings.TrimSpace(value))
}

// SetSourceKeyFields stores every field of source id given in values (field
// name -> value, each without surrounding white space) in one transaction:
// either all are saved or, on a failure, none and the old values stay.
func (s *Store) SetSourceKeyFields(ctx context.Context, sourceID string, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, field := range slices.Sorted(maps.Keys(values)) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings(key, value) VALUES(?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			sourceFieldKey(sourceID, field), strings.TrimSpace(values[field])); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearSourceKeys removes every stored field of source id, the single-key
// field "key" included; other sources are untouched. Only an undotted field
// directly under the prefix is removed (field names are slugs), so clearing
// "a" never touches the fields of a source "a.b". An empty or dotted id, which
// no source can have, is refused.
func (s *Store) ClearSourceKeys(ctx context.Context, sourceID string) error {
	if sourceID == "" || strings.Contains(sourceID, ".") {
		return fmt.Errorf("settings: clear source keys: invalid source id %q", sourceID)
	}
	prefix := sourceFieldKey(sourceID, "")
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM settings WHERE substr(key, 1, length(?1)) = ?1
		 AND instr(substr(key, length(?1) + 1), '.') = 0`, prefix)
	return err
}

// SourceKey returns the stored API key of source id, "" when none is set. A
// read failure is returned, so the caller can tell it from an unset key.
func (s *Store) SourceKey(ctx context.Context, sourceID string) (string, error) {
	return s.SourceKeyField(ctx, sourceID, "key")
}

// SetSourceKey stores the API key of source id, without surrounding white
// space.
func (s *Store) SetSourceKey(ctx context.Context, sourceID, key string) error {
	return s.SetSourceKeyField(ctx, sourceID, "key", key)
}

// ClearSourceKey removes the API key of source id.
func (s *Store) ClearSourceKey(ctx context.Context, sourceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, sourceFieldKey(sourceID, "key"))
	return err
}

// KeyTimeZone is the settings key of the owner's time zone (an IANA name).
const KeyTimeZone = "time_zone"

// ValidTimeZone reports whether name is an IANA zone name time.LoadLocation
// knows, in the strict form of the zone database: "UTC" or a name with a
// slash, at most 64 characters of [A-Za-z0-9_+-/], no leading slash or "..".
// "" and "Local" are not names (the server's own zone is chosen with
// the TZ environment variable, which is the default).
func ValidTimeZone(name string) bool {
	if !timeZoneSyntax(name) {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// timeZoneSyntax is the name check ValidTimeZone runs before it asks the zone
// database, so no file path or odd name ever reaches time.LoadLocation.
func timeZoneSyntax(name string) bool {
	if name == "" || len(name) > 64 || (name != "UTC" && !strings.Contains(name, "/")) {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '+' || c == '-' || c == '/') {
			return false
		}
	}
	return name[0] != '/' && !strings.Contains(name, "..")
}

// StoredTimeZone is the zone the owner stored, "" when none (or an invalid
// one, which is never used).
func (s *Store) StoredTimeZone(ctx context.Context) (string, error) {
	v, ok, err := s.Get(ctx, KeyTimeZone)
	if err != nil || !ok || !ValidTimeZone(v) {
		return "", err
	}
	return v, nil
}

// SetTimeZone stores the zone name; "" removes it (back to the default). An
// invalid name is refused.
func (s *Store) SetTimeZone(ctx context.Context, name string) error {
	if name == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, KeyTimeZone)
		return err
	}
	if !ValidTimeZone(name) {
		return fmt.Errorf("settings: %q is not a time zone", name)
	}
	return s.Set(ctx, KeyTimeZone, name)
}

// DefaultTimeZone is the zone used when none is stored: the TZ environment
// variable when it names a valid zone, else UTC.
func DefaultTimeZone() string {
	if tz := os.Getenv("TZ"); ValidTimeZone(tz) {
		return tz
	}
	return "UTC"
}

// TimeZoneName is the effective zone name: the stored one, else the default.
// An unreadable setting falls back to the default.
func (s *Store) TimeZoneName(ctx context.Context) string {
	stored, err := s.StoredTimeZone(ctx)
	if err != nil {
		log.Printf("settings: read %s: %v (using the default zone)", KeyTimeZone, err)
	}
	if stored != "" {
		return stored
	}
	return DefaultTimeZone()
}

// TimeLocation is the effective zone as a location (never nil).
func (s *Store) TimeLocation(ctx context.Context) *time.Location {
	loc, err := time.LoadLocation(s.TimeZoneName(ctx))
	if err != nil {
		return time.UTC
	}
	return loc
}

// KeyListPaging is the settings key of how the list pages load their rows:
// ListPagingScroll (load more while scrolling, the default) or
// ListPagingPages (split into pages with Previous and Next).
const KeyListPaging = "list_paging"

// The values of KeyListPaging.
const (
	ListPagingScroll = "scroll"
	ListPagingPages  = "pages"
)

// ValidListPaging reports whether v is a value of KeyListPaging.
func ValidListPaging(v string) bool { return v == ListPagingScroll || v == ListPagingPages }

// ListPaging is the stored list paging; unset, unreadable or not a mode
// means ListPagingScroll.
func (s *Store) ListPaging(ctx context.Context) string {
	v, ok, err := s.Get(ctx, KeyListPaging)
	if err != nil {
		log.Printf("settings: read %s: %v (loading more while scrolling)", KeyListPaging, err)
	}
	if !ok || !ValidListPaging(v) {
		return ListPagingScroll
	}
	return v
}

// SetListPaging stores the list paging; anything but a mode is refused.
func (s *Store) SetListPaging(ctx context.Context, v string) error {
	if !ValidListPaging(v) {
		return fmt.Errorf("settings: %q is not a list paging", v)
	}
	return s.Set(ctx, KeyListPaging, v)
}
