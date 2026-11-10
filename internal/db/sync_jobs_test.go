package db

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// latestVersion is the newest migration. 00008 is unused; later migrations
// continue at 00010 (goose cannot apply a lower number to a database already
// at a higher version).
const latestVersion = 11

func TestMigrationRewritesLegacySyncJobs(t *testing.T) {
	path := tempPath(t)
	old := openAt(t, path, 7)
	rows := []struct {
		id             int64
		kind, payload  string
		state          string
		wantKind, want string
	}{
		{1, "sync_anime", `{"anime_id":5,"plugin_id":"anime"}`, "pending", "sync_item", `{"item_id":5,"plugin_id":"anime"}`},
		{2, "sync_anime", `{"anime_id":6}`, "running", "sync_item", `{"item_id":6,"plugin_id":"anime"}`},
		{3, "sync_anime", `{"anime_id":7,"plugin_id":"anime"}`, "failed", "sync_item", `{"item_id":7,"plugin_id":"anime"}`},
		// finished history stays as it was
		{4, "sync_anime", `{"anime_id":8}`, "done", "sync_anime", `{"anime_id":8}`},
		// bad payloads are left untouched (they fail as an unknown kind)
		{5, "sync_anime", `not json`, "pending", "sync_anime", `not json`},
		{6, "sync_anime", `{"plugin_id":"anime"}`, "pending", "sync_anime", `{"plugin_id":"anime"}`},
		{7, "sync_anime", `{"anime_id":"x"}`, "pending", "sync_anime", `{"anime_id":"x"}`},
		// other kinds are not touched by 00009 (00010 then rewrites a legacy
		// import job to import_run, see import_plugin_test.go)
		{8, "import_yamtrack", `{"import_id":1}`, "pending", "import_run", `{"import_id":1,"plugin_id":"anime"}`},
		{9, "other_kind", `{"anime_id":1}`, "pending", "other_kind", `{"anime_id":1}`},
	}
	for _, r := range rows {
		mustExec(t, old, `INSERT INTO jobs(id, kind, payload_json, state) VALUES(?, ?, ?, ?)`, r.id, r.kind, r.payload, r.state)
	}
	old.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion {
		t.Fatalf("version = %d, %v", v, err)
	}
	check := func() {
		t.Helper()
		for _, r := range rows {
			var kind, payload, state string
			if err := d.QueryRow(`SELECT kind, payload_json, state FROM jobs WHERE id = ?`, r.id).Scan(&kind, &payload, &state); err != nil {
				t.Fatal(err)
			}
			if kind != r.wantKind || payload != r.want || state != r.state {
				t.Errorf("job %d: %s %s %s, want %s %s %s", r.id, kind, payload, state, r.wantKind, r.want, r.state)
			}
		}
	}
	check()
	// idempotent: re-running the Up statements changes nothing
	if err := goose.DownTo(d, "migrations", 7); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	check()
	// reopening is a no-op
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

// Down restores the legacy shape for pending rows of the anime plugin only
// (a running or failed row stays generic: the legacy code could not tell it
// from a new job anyway, and items of other plugins never had a legacy job).
func TestRewriteSyncJobsDown(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mustExec(t, d, `INSERT INTO jobs(id, kind, payload_json, state) VALUES
		(1, 'sync_item', '{"item_id":5,"plugin_id":"anime"}', 'pending'),
		(2, 'sync_item', '{"item_id":6,"plugin_id":"anime"}', 'failed'),
		(3, 'sync_item', '{"item_id":7,"plugin_id":"book"}', 'pending')`)
	if err := goose.DownTo(d, "migrations", 7); err != nil {
		t.Fatal(err)
	}
	want := map[int64][2]string{
		1: {"sync_anime", `{"anime_id":5,"plugin_id":"anime"}`},
		2: {"sync_item", `{"item_id":6,"plugin_id":"anime"}`},
		3: {"sync_item", `{"item_id":7,"plugin_id":"book"}`},
	}
	for id, w := range want {
		var kind, payload string
		if err := d.QueryRow(`SELECT kind, payload_json FROM jobs WHERE id = ?`, id).Scan(&kind, &payload); err != nil {
			t.Fatal(err)
		}
		if kind != w[0] || payload != w[1] {
			t.Errorf("job %d: %s %s, want %v", id, kind, payload, w)
		}
	}
}

func TestRewriteSyncJobsOnFreshDB(t *testing.T) {
	d, err := Open(tempPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, err := goose.GetDBVersion(d); err != nil || v != latestVersion {
		t.Fatalf("version = %d, %v", v, err)
	}
	if n := rowCount(t, d, `SELECT count(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs rows %d", n)
	}
}
