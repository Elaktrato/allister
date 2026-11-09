package library

import (
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allister/allister/internal/plugin"
)

const (
	dateLayout    = "2006-01-02"
	maxTextRunes  = 20000
	maxListValues = 1000
)

// fieldRow is one item_fields row: exactly one of text, num, date is set.
type fieldRow struct {
	key  string
	text *string
	num  *float64
	date *string
	seq  int
}

// normalizeFields validates fields against the kind's definitions and returns
// the rows to store in a deterministic order. Fields without any value are
// skipped (sources often lack fields); unknown keys, values of the wrong
// shape and out-of-range values are rejected. Required fields are NOT
// enforced here (see ValidateItem).
func normalizeFields(def plugin.KindDef, fields map[string]plugin.FieldValue) ([]fieldRow, error) {
	defs := make(map[string]plugin.FieldDef, len(def.Fields))
	for _, f := range def.Fields {
		defs[f.Key] = f
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var rows []fieldRow
	for _, k := range keys {
		fd, ok := defs[k]
		if !ok {
			return nil, invalid("unknown field %q for kind %q", k, def.Key)
		}
		r, err := fieldRows(fd, fields[k])
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	return rows, nil
}

func fieldRows(fd plugin.FieldDef, v plugin.FieldValue) ([]fieldRow, error) {
	hasText := strings.TrimSpace(v.Text) != ""
	hasNum, hasDate, hasList := v.Num != nil, v.Date != nil, len(v.List) > 0
	if !hasText && !hasNum && !hasDate && !hasList {
		return nil, nil
	}
	shape := func(want string, ok bool) error {
		if !ok {
			return invalid("field %q (%s) needs a %s value", fd.Key, fd.Type, want)
		}
		return nil
	}
	switch fd.Type {
	case plugin.FieldText, plugin.FieldEnum, plugin.FieldURL:
		if err := shape("text", hasText && !hasNum && !hasDate && !hasList); err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(v.Text) > maxTextRunes {
			return nil, invalid("field %q is longer than %d characters", fd.Key, maxTextRunes)
		}
		switch fd.Type {
		case plugin.FieldEnum:
			if !slices.Contains(fd.EnumValues, v.Text) {
				return nil, invalid("field %q: %q is not one of %s", fd.Key, v.Text, strings.Join(fd.EnumValues, ", "))
			}
		case plugin.FieldURL:
			u, err := url.Parse(v.Text)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, invalid("field %q: %q is not an http(s) URL", fd.Key, v.Text)
			}
		}
		s := v.Text
		return []fieldRow{{key: fd.Key, text: &s}}, nil
	case plugin.FieldNumber:
		if err := shape("number", hasNum && !hasText && !hasDate && !hasList); err != nil {
			return nil, err
		}
		n := *v.Num
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, invalid("field %q is not a finite number", fd.Key)
		}
		if fd.Min != nil && n < *fd.Min {
			return nil, invalid("field %q must be at least %v", fd.Key, *fd.Min)
		}
		if fd.Max != nil && n > *fd.Max {
			return nil, invalid("field %q must be at most %v", fd.Key, *fd.Max)
		}
		return []fieldRow{{key: fd.Key, num: &n}}, nil
	case plugin.FieldDate:
		if err := shape("date", hasDate && !hasText && !hasNum && !hasList); err != nil {
			return nil, err
		}
		if v.Date.IsZero() || v.Date.Year() < 1 || v.Date.Year() > 9999 {
			return nil, invalid("field %q is not a valid date", fd.Key)
		}
		s := plugin.DateOnly(*v.Date).Format(dateLayout)
		return []fieldRow{{key: fd.Key, date: &s}}, nil
	case plugin.FieldList:
		if err := shape("list", hasList && !hasText && !hasNum && !hasDate); err != nil {
			return nil, err
		}
		if len(v.List) > maxListValues {
			return nil, invalid("field %q has more than %d values", fd.Key, maxListValues)
		}
		rows := make([]fieldRow, 0, len(v.List))
		for i, item := range v.List {
			s := strings.TrimSpace(item)
			if s == "" {
				return nil, invalid("field %q has an empty list value", fd.Key)
			}
			if utf8.RuneCountInString(s) > maxTextRunes {
				return nil, invalid("field %q has a value longer than %d characters", fd.Key, maxTextRunes)
			}
			rows = append(rows, fieldRow{key: fd.Key, text: &s, seq: i})
		}
		return rows, nil
	}
	return nil, invalid("field %q has unsupported type %q", fd.Key, fd.Type)
}

// requiredMissing returns the labels of required fields without a stored value.
func requiredMissing(def plugin.KindDef, rows []fieldRow) []string {
	have := map[string]bool{}
	for _, r := range rows {
		have[r.key] = true
	}
	var miss []string
	for _, f := range def.Fields {
		if f.Required && !have[f.Key] {
			miss = append(miss, f.Key)
		}
	}
	return miss
}

// fieldAcc rebuilds field values from stored rows (ordered by key, seq).
type fieldAcc struct {
	vals map[string]plugin.FieldValue
	defs map[string]plugin.FieldType
}

func newFieldAcc(def *plugin.KindDef) *fieldAcc {
	a := &fieldAcc{vals: map[string]plugin.FieldValue{}, defs: map[string]plugin.FieldType{}}
	if def != nil {
		for _, f := range def.Fields {
			a.defs[f.Key] = f.Type
		}
	}
	return a
}

func (a *fieldAcc) add(key string, text *string, num *float64, date *string) {
	v := a.vals[key]
	switch {
	case num != nil:
		v.Num = num
	case date != nil:
		if t, err := time.Parse(dateLayout, *date); err == nil {
			v.Date = &t
		}
	case text != nil:
		typ, known := a.defs[key]
		if typ == plugin.FieldList || (!known && (v.Text != "" || len(v.List) > 0)) {
			if v.Text != "" { // heuristic path: promote a second value to a list
				v.List = append(v.List, v.Text)
				v.Text = ""
			}
			v.List = append(v.List, *text)
		} else {
			v.Text = *text
		}
	}
	a.vals[key] = v
}
