package web

// The columns of the list table. The cover, the title and the actions are
// always there; every other column is offered by the kind: its declared
// Columns (plugin data, see plugin.ColumnDef) or, without them, the table
// derived from its schema. Which offered columns are shown, and in which
// order, starts with the kind's defaults (see visibleColumns).

import (
	"context"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// listColumn is one offered column of a kind's list table.
type listColumn struct {
	Key     string
	Label   string
	Hide    string // "", "sm", "md" or "lg": shown from that screen width on
	Core    string // the core column (plugin.Column*), "" for a field column
	Field   plugin.FieldDef
	Default bool
}

// coreColumnLabels are the headings of the core columns.
var coreColumnLabels = map[string]string{
	plugin.ColumnProgress: "Progress", plugin.ColumnRating: "Score",
	plugin.ColumnStatus: "Status", plugin.ColumnUpdated: "Updated",
}

// kindColumns are the columns kind k offers, in order: its declared Columns,
// else the derived table (the type field from sm, the sortable fields from
// md, progress, score, status, and updated from lg), all shown by default.
func kindColumns(k plugin.KindDef) []listColumn {
	if len(k.Columns) == 0 {
		return derivedColumns(k)
	}
	out := make([]listColumn, 0, len(k.Columns))
	for _, c := range k.Columns {
		col := listColumn{Key: c.Key, Label: c.Label, Hide: c.Hide, Default: c.Default}
		if fd, ok := fieldDef(k, c.Key); ok {
			col.Field = fd
			col.Label = orDefault(col.Label, fd.Label)
		} else if label, ok := coreColumnLabels[c.Key]; ok {
			col.Core = c.Key
			col.Label = orDefault(col.Label, label)
		} else {
			continue // Validate refuses this; never render an unknown key
		}
		out = append(out, col)
	}
	return out
}

// derivedColumns is the table of a kind that declares no columns.
func derivedColumns(k plugin.KindDef) []listColumn {
	var out []listColumn
	if fd, ok := typeField(k); ok {
		out = append(out, listColumn{Key: fd.Key, Label: fd.Label, Hide: "sm", Field: fd, Default: true})
	}
	for _, fd := range sortFields(k) {
		out = append(out, listColumn{Key: fd.Key, Label: fd.Label, Hide: "md", Field: fd, Default: true})
	}
	for _, key := range plugin.CoreColumns() {
		c := listColumn{Key: key, Label: coreColumnLabels[key], Core: key, Default: true}
		if key == plugin.ColumnUpdated {
			c.Hide = "lg"
		}
		out = append(out, c)
	}
	return out
}

// defaultColumns are the offered columns shown before the user chooses.
func defaultColumns(cols []listColumn) []listColumn {
	var out []listColumn
	for _, c := range cols {
		if c.Default {
			out = append(out, c)
		}
	}
	return out
}

// visibleColumns are the columns the list of kind k of plugin m shows.
func (s *server) visibleColumns(_ context.Context, _ plugin.Manifest, k plugin.KindDef) []listColumn {
	return defaultColumns(kindColumns(k))
}

// columnSort is the sort key of a column, "" when it does not sort: a
// field column sorts when its field is one of the sort fields, progress
// when the kind has a +1 track, score and updated always, status never.
func columnSort(k plugin.KindDef, c listColumn) string {
	switch c.Core {
	case plugin.ColumnProgress:
		if k.Progress.PrimaryKey != "" {
			return string(library.SortProgress)
		}
		return ""
	case plugin.ColumnRating:
		return string(library.SortRating)
	case plugin.ColumnUpdated:
		return string(library.SortUpdated)
	case plugin.ColumnStatus:
		return ""
	}
	for _, fd := range sortFields(k) {
		if fd.Key == c.Key {
			return fd.Key
		}
	}
	return ""
}

// rowCell is one column cell of a row: Core picks how the list_row
// template draws it (a field cell shows Text).
type rowCell struct {
	Core, Text, Hide string
}
