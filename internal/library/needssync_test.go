package library

import (
	"reflect"
	"strconv"
	"testing"
)

func TestNeedsSyncIsGenericAndIgnoresLiveJobs(t *testing.T) {
	e := newEnv(t)
	save := func(id, state string) int64 {
		return mustSave(t, e.lib, "book", "book", bookData(id), WithSyncState(state))
	}
	pending, partial := save("1", "pending"), save("2", "partial")
	save("3", "done")
	withPending, withRunning, withFailed, withDone := save("4", "pending"), save("5", "partial"), save("6", "partial"), save("7", "pending")
	otherKind := save("8", "pending")
	tv := mustSave(t, e.lib, "tv", "show", tvData("9", nil), WithSyncState("partial"))
	job := func(kind, payload, state string) {
		t.Helper()
		if _, err := e.db.Exec(`INSERT INTO jobs(kind, payload_json, state) VALUES(?, ?, ?)`, kind, payload, state); err != nil {
			t.Fatal(err)
		}
	}
	p := func(id int64) string { return `{"item_id":` + strconv.FormatInt(id, 10) + `,"plugin_id":"book"}` }
	job("sync_item", p(withPending), "pending")
	job("sync_item", p(withRunning), "running")
	job("sync_item", p(withFailed), "failed")
	job("sync_item", p(withDone), "done")
	job("other", p(otherKind), "pending")
	job("sync_item", `{"anime_id":`+strconv.FormatInt(pending, 10)+`}`, "pending") // another payload shape never matches
	job("sync_item", `not json`, "pending")

	got, err := e.lib.NeedsSync(bg, "sync_item")
	if err != nil {
		t.Fatal(err)
	}
	want := []NeedsSyncItem{
		{pending, "book"}, {partial, "book"}, {withFailed, "book"}, {withDone, "book"}, {otherKind, "book"}, {tv, "tv"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// another job kind sees every unfinished item
	if got, err := e.lib.NeedsSync(bg, "nothing"); err != nil || len(got) != 8 {
		t.Fatalf("got %+v %v", got, err)
	}
	// nothing to sync: empty, no error
	if got, err := newLib(t).NeedsSync(bg, "sync_item"); err != nil || len(got) != 0 {
		t.Fatalf("empty library: %+v %v", got, err)
	}
}
