package plugin

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// maxUnitRunes is the longest FieldDef.Unit; maxIDLabelRunes the longest
// ImporterInfo.ExternalIDLabel; maxUnmatchedNoteRunes the longest
// ImporterInfo.UnmatchedNote.
const (
	maxUnitRunes          = 20
	maxIDLabelRunes       = 40
	maxUnmatchedNoteRunes = 200
	maxColumnLabelRunes   = 40
)

var (
	// idRe: plugin, kind, source, importer and exporter ids are lower-case
	// slugs: letters, digits and "-", 1 to 32 characters, starting with a letter.
	idRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	// keyRe: field and progress keys.
	keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	// iconRe: a Manifest.Icon (an icon name, empty allowed separately).
	iconRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
)

// reservedIDs are first URL segments owned by the core.
var reservedIDs = map[string]bool{
	"list": true, "people": true, "settings": true, "covers": true,
	"static": true, "healthz": true, "api": true,
}

// reservedSourceIDs are the core's segments right after a plugin's prefix
// (/{plugin}/manual/..., /{plugin}/export/..., /{plugin}/search): a source
// with such an id would be shadowed by them under /{plugin}/{source}/{id}.
var reservedSourceIDs = map[string]bool{"manual": true, "export": true, "search": true}

type problems struct{ list []error }

func (p *problems) add(path, format string, args ...any) {
	p.list = append(p.list, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
}

// Validate checks a manifest against the contract. It returns one error that
// lists every problem found (joined by newlines), each prefixed with its path,
// for example `kinds[0].fields[2].key: duplicate "year"`. It returns nil when
// the manifest is valid.
func Validate(m Manifest) error {
	var p problems
	if m.ContractVersion != ContractVersion {
		p.add("contract_version", "plugin speaks contract %d, core speaks %d", m.ContractVersion, ContractVersion)
	}
	switch {
	case !idRe.MatchString(m.ID):
		p.add("id", "%q is not a valid slug (lower-case letters, digits and -, 1 to 32 characters, starting with a letter)", m.ID)
	case reservedIDs[m.ID]:
		p.add("id", "%q is reserved by the core", m.ID)
	}
	if m.Name == "" {
		p.add("name", "required")
	}
	if m.Icon != "" && !iconRe.MatchString(m.Icon) {
		p.add("icon", "%q is not an icon name (lower-case letters, digits and _, 1 to 40 characters)", m.Icon)
	}
	if len(m.Kinds) == 0 {
		p.add("kinds", "at least one kind required")
	}
	seenKind := map[string]bool{}
	for i, k := range m.Kinds {
		path := fmt.Sprintf("kinds[%d]", i)
		if !idRe.MatchString(k.Key) {
			p.add(path+".key", "%q is not a valid slug", k.Key)
		} else if seenKind[k.Key] {
			p.add(path+".key", "duplicate %q", k.Key)
		}
		seenKind[k.Key] = true
		validateKind(&p, path, k)
	}

	sources := map[string]bool{}
	for i, s := range m.Sources {
		path := fmt.Sprintf("sources[%d]", i)
		checkID(&p, path+".id", s.ID, sources)
		if reservedSourceIDs[s.ID] {
			p.add(path+".id", "%q is reserved by the core", s.ID)
		}
		if s.Name == "" {
			p.add(path+".name", "required")
		}
		if s.RequestsPerMinute <= 0 {
			p.add(path+".requests_per_minute", "must be positive")
		}
		for j, pat := range s.URLPatterns {
			if err := checkURLPattern(pat); err != nil {
				p.add(fmt.Sprintf("%s.url_patterns[%d]", path, j), "%v", err)
			}
		}
	}
	importers := map[string]bool{}
	for i, im := range m.Importers {
		path := fmt.Sprintf("importers[%d]", i)
		checkID(&p, path+".id", im.ID, importers)
		if im.Name == "" {
			p.add(path+".name", "required")
		}
		switch l := im.ExternalIDLabel; {
		case !utf8.ValidString(l) || strings.IndexFunc(l, isControl) >= 0:
			p.add(path+".external_id_label", "must be a single line without control characters")
		case utf8.RuneCountInString(l) > maxIDLabelRunes:
			p.add(path+".external_id_label", "longer than %d characters", maxIDLabelRunes)
		}
		switch n := im.UnmatchedNote; {
		case !utf8.ValidString(n) || strings.IndexFunc(n, isControl) >= 0:
			p.add(path+".unmatched_note", "must be a single line without control characters")
		case utf8.RuneCountInString(n) > maxUnmatchedNoteRunes:
			p.add(path+".unmatched_note", "longer than %d characters", maxUnmatchedNoteRunes)
		}
		if im.ResolveVia != "" {
			validateResolveVia(&p, path, im, m.Sources)
		}
	}
	exporters := map[string]bool{}
	for i, ex := range m.Exporters {
		path := fmt.Sprintf("exporters[%d]", i)
		checkID(&p, path+".id", ex.ID, exporters)
		if ex.Name == "" {
			p.add(path+".name", "required")
		}
		if ex.Extension == "" {
			p.add(path+".extension", "required")
		}
		if ex.MIME == "" {
			p.add(path+".mime", "required")
		}
	}
	return errors.Join(p.list...)
}

// validateResolveVia checks that the importer's ResolveVia source exists,
// supports lookup and can look up the importer's ExternalIDType; otherwise
// every row of every import would fail its lookup.
func validateResolveVia(p *problems, path string, im ImporterInfo, sources []SourceInfo) {
	var src *SourceInfo
	for i := range sources {
		if sources[i].ID == im.ResolveVia {
			src = &sources[i]
			break
		}
	}
	if src == nil {
		p.add(path+".resolve_via", "unknown source %q", im.ResolveVia)
		return
	}
	if !src.SupportsLookup {
		p.add(path+".resolve_via", "source %q does not support lookup", src.ID)
	}
	switch {
	case im.ExternalIDType == "":
		p.add(path+".external_id_type", "required when resolve_via is set")
	case !slices.Contains(src.ExternalIDTypes, im.ExternalIDType):
		p.add(path+".external_id_type", "source %q cannot look up %q ids", src.ID, im.ExternalIDType)
	}
}

func checkID(p *problems, path, id string, seen map[string]bool) {
	if !idRe.MatchString(id) {
		p.add(path, "%q is not a valid slug", id)
	} else if seen[id] {
		p.add(path, "duplicate %q", id)
	}
	seen[id] = true
}

func validateKind(p *problems, path string, k KindDef) {
	if k.Label == "" {
		p.add(path+".label", "required")
	}
	if k.LabelPlural == "" {
		p.add(path+".label_plural", "required")
	}
	if len(k.TitleLanguages) == 0 {
		p.add(path+".title_languages", "at least one language required")
	}
	seenLang := map[string]bool{}
	for i, l := range k.TitleLanguages {
		if l == "" {
			p.add(fmt.Sprintf("%s.title_languages[%d]", path, i), "empty language")
		} else if seenLang[l] {
			p.add(fmt.Sprintf("%s.title_languages[%d]", path, i), "duplicate %q", l)
		}
		seenLang[l] = true
	}
	seenFallback := map[string]bool{}
	for i, l := range k.TitleFallback {
		at := fmt.Sprintf("%s.title_fallback[%d]", path, i)
		switch {
		case !slices.Contains(k.TitleLanguages, l) || l == "":
			p.add(at, "%q is not one of the title languages", l)
		case seenFallback[l]:
			p.add(at, "duplicate %q", l)
		}
		seenFallback[l] = true
	}
	for _, t := range []struct {
		name string
		def  TermDef
	}{{"genres", k.Genres}, {"tags", k.Tags}} {
		if t.def.Enabled && t.def.Label == "" {
			p.add(path+"."+t.name+".label", "required when enabled")
		}
	}
	for _, s := range []struct {
		name, label string
	}{{"planned", k.Statuses.Planned}, {"active", k.Statuses.Active}, {"paused", k.Statuses.Paused},
		{"completed", k.Statuses.Completed}, {"dropped", k.Statuses.Dropped}} {
		if s.label == "" {
			p.add(path+".statuses."+s.name, "label required")
		}
	}

	fields := map[string]FieldType{}
	for i, f := range k.Fields {
		fp := fmt.Sprintf("%s.fields[%d]", path, i)
		if !keyRe.MatchString(f.Key) {
			p.add(fp+".key", "%q is not a valid key", f.Key)
		} else if _, dup := fields[f.Key]; dup {
			p.add(fp+".key", "duplicate %q", f.Key)
		}
		fields[f.Key] = f.Type
		if f.Label == "" {
			p.add(fp+".label", "required")
		}
		if !validFieldType(f.Type) {
			p.add(fp+".type", "unknown field type %q", f.Type)
		}
		if f.Type == FieldEnum && len(f.EnumValues) == 0 {
			p.add(fp+".enum_values", "enum field needs at least one value")
		}
		if f.Type != FieldEnum && len(f.EnumValues) > 0 {
			p.add(fp+".enum_values", "only enum fields take values")
		}
		if f.Unit != "" {
			switch {
			case f.Type != FieldNumber:
				p.add(fp+".unit", "only number fields take a unit")
			case !utf8.ValidString(f.Unit) || strings.IndexFunc(f.Unit, isControl) >= 0:
				p.add(fp+".unit", "must be a single line without control characters")
			case utf8.RuneCountInString(f.Unit) > maxUnitRunes:
				p.add(fp+".unit", "longer than %d characters", maxUnitRunes)
			}
		}
		if f.Min != nil && !finite(*f.Min) {
			p.add(fp+".min", "must be a finite number")
		}
		if f.Max != nil && !finite(*f.Max) {
			p.add(fp+".max", "must be a finite number")
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			p.add(fp, "min %v is above max %v", *f.Min, *f.Max)
		}
	}

	validateProgress(p, path+".progress", k.Progress, fields)
	validateColumns(p, path, k.Columns, fields)

	for i, s := range k.Sections {
		sp := fmt.Sprintf("%s.sections[%d]", path, i)
		if !validSectionType(s.Type) {
			p.add(sp+".type", "unknown section type %q", s.Type)
		}
		if s.Type == SectionGenres && !k.Genres.Enabled {
			p.add(sp, "genres section requires genres to be enabled")
		}
		if s.Type == SectionTags && !k.Tags.Enabled {
			p.add(sp, "tags section requires tags to be enabled")
		}
		if s.Field != "" {
			if _, ok := fields[s.Field]; !ok {
				p.add(sp+".field", "no field %q", s.Field)
			}
		}
	}
}

// columnHides are the allowed ColumnDef.Hide values.
var columnHides = []string{"", "sm", "md", "lg"}

// validateColumns checks a kind's declared table columns: each names a field
// or a core column (never both, never the always-shown title), once, with a
// bounded single-line label and a known width hint.
func validateColumns(p *problems, path string, cols []ColumnDef, fields map[string]FieldType) {
	seen := map[string]bool{}
	for i, c := range cols {
		cp := fmt.Sprintf("%s.columns[%d]", path, i)
		_, isField := fields[c.Key]
		isCore := slices.Contains(CoreColumns(), c.Key)
		switch {
		case c.Key == "title":
			p.add(cp+".key", "%q is always shown and cannot be declared", c.Key)
		case isField && isCore:
			p.add(cp+".key", "%q names both a field and a core column", c.Key)
		case !isField && !isCore:
			p.add(cp+".key", "%q is neither a field of the kind nor a core column (%s)", c.Key, strings.Join(CoreColumns(), ", "))
		case seen[c.Key]:
			p.add(cp+".key", "duplicate %q", c.Key)
		}
		seen[c.Key] = true
		switch l := c.Label; {
		case !utf8.ValidString(l) || strings.IndexFunc(l, isControl) >= 0:
			p.add(cp+".label", "must be a single line without control characters")
		case utf8.RuneCountInString(l) > maxColumnLabelRunes:
			p.add(cp+".label", "longer than %d characters", maxColumnLabelRunes)
		}
		if !slices.Contains(columnHides, c.Hide) {
			p.add(cp+".hide", `%q is not one of "", "sm", "md", "lg"`, c.Hide)
		}
	}
}

func validateProgress(p *problems, path string, ps ProgressSpec, fields map[string]FieldType) {
	types := map[string]ProgressType{}
	for i, d := range ps.Eligible {
		dp := fmt.Sprintf("%s.eligible[%d]", path, i)
		if !keyRe.MatchString(d.Key) {
			p.add(dp+".key", "%q is not a valid key", d.Key)
		} else if _, dup := types[d.Key]; dup {
			p.add(dp+".key", "duplicate %q", d.Key)
		}
		types[d.Key] = d.Type
		if d.Label == "" {
			p.add(dp+".label", "required")
		}
		if !ValidProgressType(d.Type) {
			p.add(dp+".type", "unknown progress type %q (allowed: count, pages, percent, duration)", d.Type)
		}
		if d.TotalField != "" {
			ft, ok := fields[d.TotalField]
			switch {
			case d.Type == Percent:
				p.add(dp+".total_field", "percent progress has no total")
			case !ok:
				p.add(dp+".total_field", "no field %q", d.TotalField)
			case ft != FieldNumber:
				p.add(dp+".total_field", "field %q is not a number field", d.TotalField)
			}
		}
	}
	checkKeys := func(at string, keys []string) bool {
		ok := true
		seen := map[string]bool{}
		for _, key := range keys {
			if _, eligible := types[key]; !eligible {
				p.add(at, "%q is not an eligible progress key", key)
				ok = false
			} else if seen[key] {
				p.add(at, "%q listed twice", key)
				ok = false
			}
			seen[key] = true
		}
		return ok
	}
	if len(ps.Eligible) > 0 {
		if len(ps.Defaults) == 0 {
			p.add(path+".defaults", "at least one default key required")
		}
		if len(ps.Combinations) == 0 {
			p.add(path+".combinations", "at least one combination required")
		}
	}
	defaultsOK := checkKeys(path+".defaults", ps.Defaults)
	matched := false
	for i, c := range ps.Combinations {
		at := fmt.Sprintf("%s.combinations[%d]", path, i)
		if len(c) == 0 {
			p.add(at, "empty combination")
			continue
		}
		checkKeys(at, c)
		if sameSet(c, ps.Defaults) {
			matched = true
		}
	}
	if defaultsOK && len(ps.Defaults) > 0 && len(ps.Combinations) > 0 && !matched {
		p.add(path+".defaults", "not one of the allowed combinations")
	}
	if ps.PrimaryKey != "" {
		t, ok := types[ps.PrimaryKey]
		switch {
		case !ok:
			p.add(path+".primary_key", "%q is not an eligible progress key", ps.PrimaryKey)
		case t != Count && t != Pages:
			p.add(path+".primary_key", "%q must be a count or pages progress, not %s", ps.PrimaryKey, t)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
