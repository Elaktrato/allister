package web

import (
	"testing"

	"github.com/allister/allister/internal/library"
)

// The restore summary reports the series: created ones and ones left out.
func TestBackupRestoreSummaryShowsSeries(t *testing.T) {
	a := backupEnv(t)
	first := a.addFixture(t, "book", "book", bookItem("1", "Earthsea"), library.Entry{Status: library.Completed})
	second := a.addFixture(t, "book", "book", bookItem("2", "Tombs"), library.Entry{Status: library.Planned})
	ser, err := a.lib.CreateSeries(bg, first, "Earthsea Cycle")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.lib.AddPart(bg, ser.ID, second, ""); err != nil {
		t.Fatal(err)
	}
	data := a.download(t)

	b := backupEnv(t)
	body := b.restoreUpload(t, data).Body.String()
	mustContain(t, body, "Restore summary", `data-series`, `data-count="series-created">1<`, `data-count="series-skipped">0<`)

	// restoring again: the items and the series are already there
	body = b.restoreUpload(t, data).Body.String()
	mustContain(t, body, `data-count="series-created">0<`, `data-count="series-skipped">1<`)

	// a backup without series shows no series line
	c := backupEnv(t)
	c.addFixture(t, "book", "book", bookItem("3", "Lone"), library.Entry{Status: library.Planned})
	body = backupEnv(t).restoreUpload(t, c.download(t)).Body.String()
	mustContain(t, body, "Restore summary")
	mustNotContain(t, body, "data-series")
}
