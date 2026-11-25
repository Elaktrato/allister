package backup

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func (e *appearanceEnv) restorePaging(data []byte) *Report {
	e.t.Helper()
	rep, err := Restore(bg, bytes.NewReader(data), int64(len(data)), Options{Library: e.lib, CoversDir: e.covers, ListPaging: e.set})
	if err != nil {
		e.t.Fatal(err)
	}
	if rep.Aborted != "" {
		e.t.Fatalf("aborted: %s", rep.Aborted)
	}
	return rep
}

func backupJSON(t *testing.T, data []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := zr.Open("backup.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteListPagingMember(t *testing.T) {
	e := newAppearanceEnv(t)
	var b bytes.Buffer
	if err := Write(bg, &b, e.lib, nil, e.covers, nil, Settings{ListPaging: "pages"}, clock); err != nil {
		t.Fatal(err)
	}
	if j := backupJSON(t, b.Bytes()); !strings.Contains(j, `"list_paging":"pages"`) {
		t.Errorf("no member: %.300s", j)
	}
	b.Reset()
	if err := Write(bg, &b, e.lib, nil, e.covers, nil, Settings{}, clock); err != nil {
		t.Fatal(err)
	}
	if j := backupJSON(t, b.Bytes()); strings.Contains(j, "list_paging") {
		t.Errorf("empty setting written: %.300s", j)
	}
}

func TestRestoreListPaging(t *testing.T) {
	for _, where := range []string{
		`{"format":"allister.backup","format_version":1,"list_paging":"pages","items":[]}`,
		`{"format":"allister.backup","format_version":1,"items":[],"list_paging":"pages"}`,
	} {
		e := newAppearanceEnv(t)
		rep := e.restorePaging(zoneArchive(t, where))
		if e.get("list_paging") != "pages" || !strings.Contains(strings.Join(rep.Appearance, "|"), "Lists setting restored.") {
			t.Errorf("%s: paging %q notes %v", where, e.get("list_paging"), rep.Appearance)
		}
	}
	// an installation with its own keeps it
	e := newAppearanceEnv(t)
	if err := e.set.SetListPaging(bg, "scroll"); err != nil {
		t.Fatal(err)
	}
	rep := e.restorePaging(zoneArchive(t, `{"format":"allister.backup","format_version":1,"items":[],"list_paging":"pages"}`))
	if e.get("list_paging") != "scroll" || !strings.Contains(strings.Join(rep.Appearance, "|"), "Lists setting kept: this installation has its own.") {
		t.Errorf("own: %q %v", e.get("list_paging"), rep.Appearance)
	}
	// invalid values are ignored with a note
	for _, v := range []string{`"endless"`, `42`, `""`} {
		e := newAppearanceEnv(t)
		rep := e.restorePaging(zoneArchive(t, `{"format":"allister.backup","format_version":1,"items":[],"list_paging":`+v+`}`))
		if e.get("list_paging") != "" || !strings.Contains(strings.Join(rep.Appearance, "|"), "Lists setting of the backup was not restored") {
			t.Errorf("%s: %q %v", v, e.get("list_paging"), rep.Appearance)
		}
	}
	// a refused archive stores nothing
	e = newAppearanceEnv(t)
	data := zoneArchive(t, `{"list_paging":"pages","format":"nope","format_version":1,"items":[]}`)
	if _, err := Restore(bg, bytes.NewReader(data), int64(len(data)), Options{Library: e.lib, CoversDir: e.covers, ListPaging: e.set}); err == nil {
		t.Fatal("refused archive accepted")
	}
	if e.get("list_paging") != "" {
		t.Errorf("stored %q", e.get("list_paging"))
	}
}
