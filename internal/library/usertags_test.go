package library

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func entryTagsOf(t *testing.T, l *Library, ids ...int64) map[int64][]string {
	t.Helper()
	got, err := l.EntryTags(bg, ids)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func userTagsOf(t *testing.T, l *Library) []UserTag {
	t.Helper()
	got, err := l.UserTags(bg)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func setTags(t *testing.T, l *Library, id int64, names ...string) {
	t.Helper()
	if err := l.SetEntryTags(bg, id, names); err != nil {
		t.Fatal(err)
	}
}

// tagNames are the names of the stored tags with their counts, "name:count".
func tagNames(tags []UserTag) []string {
	var out []string
	for _, tg := range tags {
		out = append(out, tg.Name+":"+strconv.Itoa(tg.Count))
	}
	return out
}

// Names differing only by case or Unicode folding are one tag, stored as
// first typed (Review Focus 5).
func TestSetEntryTagsFoldsNames(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "ut-1", nil, Entry{Status: Active})
	b := addBook(t, e.lib, "ut-2", nil, Entry{Status: Active})
	setTags(t, e.lib, a, " Comfort ", "comfort", "COMFORT", "Äpfel", "äpfel", "", "  ")
	setTags(t, e.lib, b, "COMFORT", "ÄPFEL", "with-partner")
	got := entryTagsOf(t, e.lib, a, b)
	want := map[int64][]string{a: {"Comfort", "Äpfel"}, b: {"Comfort", "with-partner", "Äpfel"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EntryTags = %v, want %v", got, want)
	}
	if n := count(t, e.db, `SELECT count(*) FROM user_tags`); n != 3 {
		t.Fatalf("user_tags rows = %d, want 3", n)
	}
	if got := tagNames(userTagsOf(t, e.lib)); !reflect.DeepEqual(got, []string{"Comfort:2", "with-partner:1", "Äpfel:2"}) {
		t.Fatalf("UserTags = %v", got)
	}
	// replacing the set drops the removed tag from the entry, the tag stays
	setTags(t, e.lib, b, "with-partner")
	if got := entryTagsOf(t, e.lib, b); !reflect.DeepEqual(got[b], []string{"with-partner"}) {
		t.Fatalf("after replace = %v", got)
	}
	if got := tagNames(userTagsOf(t, e.lib)); !reflect.DeepEqual(got, []string{"Comfort:1", "with-partner:1", "Äpfel:1"}) {
		t.Fatalf("UserTags after replace = %v", got)
	}
	setTags(t, e.lib, b)
	if got := entryTagsOf(t, e.lib, a, b); len(got[b]) != 0 || len(got[a]) != 2 {
		t.Fatalf("after clearing = %v", got)
	}
}

func TestSetEntryTagsRefusals(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "ut-r", nil, Entry{Status: Active})
	setTags(t, e.lib, a, "keep")
	ok40 := strings.Repeat("あ", MaxUserTagRunes)
	setTags(t, e.lib, a, ok40)
	for _, tc := range []struct {
		names []string
		msg   string
	}{
		{[]string{strings.Repeat("あ", MaxUserTagRunes+1)}, "40"},
		{[]string{"line\nbreak"}, "control"},
		{[]string{"tab\there"}, "control"},
		{[]string{"ok", "bad\x00"}, "NUL"},
		{[]string{"\xff"}, "UTF-8"},
	} {
		wantInvalid(t, e.lib.SetEntryTags(bg, a, tc.names), tc.msg)
	}
	many := make([]string, maxEntryTags+1)
	for i := range many {
		many[i] = "t" + strconv.Itoa(i)
	}
	wantInvalid(t, e.lib.SetEntryTags(bg, a, many), "tags")
	// nothing of a refused set was written
	if got := entryTagsOf(t, e.lib, a); !reflect.DeepEqual(got[a], []string{ok40}) {
		t.Fatalf("tags after refusals = %v", got)
	}
	if n := count(t, e.db, `SELECT count(*) FROM user_tags`); n != 2 {
		t.Fatalf("user_tags rows = %d, want 2", n)
	}
	if err := e.lib.SetEntryTags(bg, 9999, []string{"x"}); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("unknown entry: %v", err)
	}
}

func TestQueryUserTagsAnyAll(t *testing.T) {
	l := newLib(t)
	a := bookT(t, l, "q1", "Alpha", nil, Entry{})
	b := bookT(t, l, "q2", "Beta", nil, Entry{})
	bookT(t, l, "q3", "Gamma", nil, Entry{})
	setTags(t, l, a, "Comfort", "Äpfel")
	setTags(t, l, b, "comfort")
	run := func(vals []string, all bool) []string {
		return titlesOf(mustList(t, l, Query{PluginID: "book", Kind: "book", UserTags: vals, UserTagsAll: all}))
	}
	sameSet(t, "any", run([]string{"COMFORT"}, false), "Alpha", "Beta")
	sameSet(t, "any folds unicode", run([]string{"ÄPFEL", "nope"}, false), "Alpha")
	sameSet(t, "all", run([]string{"comfort", "äpfel"}, true), "Alpha")
	sameSet(t, "all dedupes case", run([]string{"comfort", "COMFORT"}, true), "Alpha", "Beta")
	sameSet(t, "unknown", run([]string{"nope"}, false))
	sameSet(t, "blank ignored", run([]string{" "}, true), "Alpha", "Beta", "Gamma")
	sameSet(t, "hostile", run([]string{"'; DROP TABLE items; --", "\x00"}, false))
	// the counts of the filter bar ignore the own-tag filter itself
	counts, err := l.UserTagTerms(bg, Query{PluginID: "book", Kind: "book", UserTags: []string{"äpfel"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(counts, []TermCount{{"Comfort", 2}, {"Äpfel", 1}}) {
		t.Fatalf("UserTagTerms = %v", counts)
	}
	// list rows carry the entry's tags
	pg := mustList(t, l, Query{PluginID: "book", Kind: "book", Sort: Sort{Kind: SortTitle}})
	if got := pg.Rows[0].UserTags; !reflect.DeepEqual(got, []string{"Comfort", "Äpfel"}) {
		t.Fatalf("row tags = %v", got)
	}
	if got := pg.Rows[2].UserTags; got != nil {
		t.Fatalf("untagged row tags = %v", got)
	}
}

func TestRenameUserTagMerges(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "rn-1", nil, Entry{Status: Active})
	b := addBook(t, e.lib, "rn-2", nil, Entry{Status: Active})
	setTags(t, e.lib, a, "comfy", "Comfort")
	setTags(t, e.lib, b, "comfy")
	id := func(name string) int64 {
		for _, tg := range userTagsOf(t, e.lib) {
			if tg.Name == name {
				return tg.ID
			}
		}
		t.Fatalf("no tag %q", name)
		return 0
	}
	// a new spelling of the same name just renames
	comfy := id("comfy")
	if err := e.lib.RenameUserTag(bg, comfy, " Comfy "); err != nil {
		t.Fatal(err)
	}
	if got := tagNames(userTagsOf(t, e.lib)); !reflect.DeepEqual(got, []string{"Comfort:1", "Comfy:2"}) {
		t.Fatalf("after respelling = %v", got)
	}
	// onto an existing name (another case): merged into it, no duplicates
	comfort := id("Comfort")
	if err := e.lib.RenameUserTag(bg, comfy, "COMFORT"); err != nil {
		t.Fatal(err)
	}
	if got := tagNames(userTagsOf(t, e.lib)); !reflect.DeepEqual(got, []string{"Comfort:2"}) {
		t.Fatalf("after merge = %v", got)
	}
	if got := userTagsOf(t, e.lib)[0].ID; got != comfort {
		t.Fatalf("merged into %d, want the existing tag %d", got, comfort)
	}
	if got := entryTagsOf(t, e.lib, a, b); !reflect.DeepEqual(got, map[int64][]string{a: {"Comfort"}, b: {"Comfort"}}) {
		t.Fatalf("entry tags after merge = %v", got)
	}
	wantInvalid(t, e.lib.RenameUserTag(bg, comfort, strings.Repeat("x", MaxUserTagRunes+1)), "40")
	wantInvalid(t, e.lib.RenameUserTag(bg, comfort, "  "), "empty")
	if err := e.lib.RenameUserTag(bg, comfy, "x"); !errors.Is(err, ErrNoUserTag) {
		t.Fatalf("rename of a removed tag: %v", err)
	}
}

func TestDeleteUserTagRemovesItFromEntries(t *testing.T) {
	e := newEnv(t)
	a := addBook(t, e.lib, "dl-1", nil, Entry{Status: Active})
	setTags(t, e.lib, a, "gone", "kept")
	var id int64
	for _, tg := range userTagsOf(t, e.lib) {
		if tg.Name == "gone" {
			id = tg.ID
		}
	}
	if err := e.lib.DeleteUserTag(bg, id); err != nil {
		t.Fatal(err)
	}
	if got := entryTagsOf(t, e.lib, a); !reflect.DeepEqual(got[a], []string{"kept"}) {
		t.Fatalf("after delete = %v", got)
	}
	if err := e.lib.DeleteUserTag(bg, id); !errors.Is(err, ErrNoUserTag) {
		t.Fatalf("second delete: %v", err)
	}
	// removing the entry removes its tag links, not the tags
	if err := e.lib.DeleteItem(bg, a); err != nil {
		t.Fatal(err)
	}
	if got := tagNames(userTagsOf(t, e.lib)); !reflect.DeepEqual(got, []string{"kept:0"}) {
		t.Fatalf("after removing the entry = %v", got)
	}
}
