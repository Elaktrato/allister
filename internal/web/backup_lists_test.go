package web

import (
	"testing"

	"github.com/allister/allister/internal/library"
)

// The restore summary reports the custom lists and their entries.
func TestBackupRestoreSummaryShowsLists(t *testing.T) {
	a := backupEnv(t)
	id := a.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Completed})
	l, err := a.lib.CreateList(bg, "Shelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.lib.AddToCustomList(bg, l.ID, id); err != nil {
		t.Fatal(err)
	}
	data := a.download(t)

	b := backupEnv(t)
	body := b.restoreUpload(t, data).Body.String()
	mustContain(t, body, `data-lists`, `data-count="lists">1<`, `data-count="list-items-added">1<`, `data-count="list-items-left-out">0<`)
	// restoring again adds no entry to the list
	body = b.restoreUpload(t, data).Body.String()
	mustContain(t, body, `data-count="lists">1<`, `data-count="list-items-added">0<`)

	// a backup without lists shows no lists line
	c := backupEnv(t)
	c.addFixture(t, "book", "book", bookItem("3", "Lone"), library.Entry{Status: library.Planned})
	body = backupEnv(t).restoreUpload(t, c.download(t)).Body.String()
	mustContain(t, body, "Restore summary")
	mustNotContain(t, body, "data-lists")
}
