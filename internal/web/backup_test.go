package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allister/allister/internal/backup"
	"github.com/allister/allister/internal/jobs"
	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugins/anime"
)

// backupEnv is the fixture env (anime, book, tv) with a sync job for every
// plugin, as cmd/allister wires it.
func backupEnv(t *testing.T) *env {
	t.Helper()
	e := newFixtureEnv(t).env
	e.srv.deps.SyncJobs = map[string]func(int64) (string, any){}
	for _, m := range e.reg.Manifests() {
		e.srv.deps.SyncJobs[m.ID] = jobs.SyncJobFor(m.ID)
	}
	return e
}

func (e *env) download(t *testing.T) []byte {
	t.Helper()
	rec := e.get("/settings/backup/download")
	if rec.Code != 200 {
		t.Fatalf("download status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

func (e *env) restoreUpload(t *testing.T, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, nil, &upload{field: "file", filename: "b.zip", ctype: "application/zip", data: data})
	req := httptest.NewRequest("POST", "/settings/backup/restore", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// tmpFiles lists the files left in DATA_DIR/tmp.
func (e *env) tmpFiles(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(e.srv.deps.DataDir, "tmp"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, en := range ents {
		out = append(out, en.Name())
	}
	return out
}

func backupJSON(t *testing.T, data []byte) (map[string]any, []library.BackupItem, []string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var raw []byte
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Name == "backup.json" {
			if f.Method != zip.Deflate {
				t.Fatalf("backup.json method %d", f.Method)
			}
			rc, _ := f.Open()
			raw, _ = io.ReadAll(rc)
			rc.Close()
		} else if f.Method != zip.Store {
			t.Fatalf("%s method %d", f.Name, f.Method)
		}
	}
	var head map[string]any
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatalf("backup.json: %v", err)
	}
	var doc struct {
		Items []library.BackupItem `json:"items"`
	}
	json.Unmarshal(raw, &doc)
	return head, doc.Items, names
}

func TestBackupDownloadStreamsZipWithHeaders(t *testing.T) {
	e := backupEnv(t)
	d := bookItem("1", "Earthsea")
	d.CoverURL = writeCoverFile(t, e, 1)
	e.addFixture(t, "book", "book", d, library.Entry{Status: library.Active})
	e.addFixture(t, "tv", "show", tvShow("2", "Show"), library.Entry{Status: library.Planned})
	// a disabled plugin's data is part of the backup
	if err := e.reg.SetEnabled(context.Background(), "tv", false); err != nil {
		t.Fatal(err)
	}
	rec := e.get("/settings/backup/download")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/zip" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Disposition") != `attachment; filename="allister-backup-2026-01-01.zip"` {
		t.Fatalf("headers = %v", h)
	}
	head, items, names := backupJSON(t, rec.Body.Bytes())
	if head["format"] != "allister.backup" || head["format_version"] != float64(1) {
		t.Fatalf("head = %v", head)
	}
	plugins, _ := head["plugins"].([]any)
	if len(plugins) != 3 {
		t.Fatalf("plugins = %v", head["plugins"])
	}
	if len(items) != 2 || items[1].PluginID != "tv" {
		t.Fatalf("items = %+v", items)
	}
	if !reflect.DeepEqual(names, []string{"backup.json", "covers/" + d.CoverURL}) {
		t.Fatalf("names = %v", names)
	}
}

// writeCoverFile stores a real PNG in the env's covers dir.
func writeCoverFile(t *testing.T, e *env, seed byte) string {
	t.Helper()
	// a content-hash name, as covers.Save stores it (a restore stores the
	// same bytes under the same name)
	sum := sha256.Sum256(tinyPNG(seed))
	name := hex.EncodeToString(sum[:]) + ".png"
	if err := os.MkdirAll(e.covers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.covers, name), tinyPNG(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func tinyPNG(seed byte) []byte {
	// 1x1 PNG; the seed changes a pixel byte (the CRC is not checked by sniffing)
	b := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")
	return append(b, seed)
}

func TestBackupPageAndSettingsLink(t *testing.T) {
	e := backupEnv(t)
	mustContain(t, e.get("/settings").Body.String(), `href="/settings/backup"`)
	rec := e.get("/settings/backup")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), `href="/settings/backup/download"`, `action="/settings/backup/restore"`,
		`enctype="multipart/form-data"`, `name="file"`, "not included", "never overwritten", `aria-current="page"`)
	mustNotContain(t, rec.Body.String(), "Restore summary")
}

func TestBackupRestoreViaPageEnqueuesSyncAndCleansUp(t *testing.T) {
	a := backupEnv(t)
	d := bookItem("1", "Earthsea")
	d.CoverURL = writeCoverFile(t, a, 1)
	a.addFixture(t, "book", "book", d, library.Entry{Status: library.Active})
	m := bookItem("m", "Manual <b>book</b>")
	m.Source = "manual"
	a.addFixture(t, "book", "book", m, library.Entry{Status: library.Planned}, library.WithSyncState("done"))
	a.addFixture(t, "tv", "show", tvShow("2", "Show"), library.Entry{Status: library.Planned})
	if err := a.reg.SetEnabled(context.Background(), "tv", false); err != nil {
		t.Fatal(err)
	}
	data := a.download(t)

	b := backupEnv(t)
	if err := b.reg.SetEnabled(context.Background(), "tv", false); err != nil {
		t.Fatal(err)
	}
	var spooled os.FileMode
	b.srv.spoolHook = func(path string) {
		if fi, err := os.Stat(path); err == nil {
			spooled = fi.Mode().Perm()
		}
	}
	rec := b.restoreUpload(t, data)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	mustContain(t, body, "Restore summary", `data-count="created">3<`, `data-count="skipped">0<`)
	if spooled != 0o600 {
		t.Fatalf("temp file mode %v", spooled)
	}
	if left := b.tmpFiles(t); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	// sync jobs for the two sourced items (also of the disabled plugin), none for the manual one
	var plugins []string
	for _, j := range b.jobs.got {
		raw, _ := json.Marshal(j.Payload)
		var p struct {
			PluginID string `json:"plugin_id"`
			ItemID   int64  `json:"item_id"`
		}
		json.Unmarshal(raw, &p)
		plugins = append(plugins, p.PluginID)
	}
	if !reflect.DeepEqual(plugins, []string{"book", "tv"}) {
		t.Fatalf("jobs = %+v", b.jobs.got)
	}
	_, items, _ := backupJSON(t, b.download(t))
	_, want, _ := backupJSON(t, data)
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("round trip differs:\n%+v\n%+v", items, want)
	}
	// a second restore skips everything
	rec = b.restoreUpload(t, data)
	mustContain(t, rec.Body.String(), `data-count="created">0<`, `data-count="skipped">3<`)
}

func TestBackupRestoreRefusals(t *testing.T) {
	e := backupEnv(t)
	zipWith := func(name, body string) []byte {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		w, _ := zw.Create(name)
		w.Write([]byte(body))
		zw.Close()
		return b.Bytes()
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"not a zip", []byte("<html>hello</html>"), "not an Allister backup"},
		{"no backup.json", zipWith("x.txt", "x"), "not an Allister backup"},
		{"newer format", zipWith("backup.json", `{"format":"allister.backup","format_version":2,"items":[]}`), "format version 2"},
		{"other format", zipWith("backup.json", `{"format":"x","format_version":1,"items":[]}`), "not an Allister backup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.restoreUpload(t, tc.data)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d", rec.Code)
			}
			mustContain(t, rec.Body.String(), tc.want)
			mustNotContain(t, rec.Body.String(), "Restore summary")
			if left := e.tmpFiles(t); len(left) != 0 {
				t.Fatalf("temp files left: %v", left)
			}
		})
	}
	// no file
	body, ct := multipartBody(t, map[string][]string{"x": {"1"}}, nil)
	req := httptest.NewRequest("POST", "/settings/backup/restore", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "choose the backup file") {
		t.Fatalf("no file: %d %s", rec.Code, rec.Body.String())
	}
	// not multipart
	req = httptest.NewRequest("POST", "/settings/backup/restore", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("not multipart: %d", rec.Code)
	}
	// cross-origin POST is refused before anything runs
	req = httptest.NewRequest("POST", "/settings/backup/restore", strings.NewReader(""))
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", rec.Code)
	}
}

func TestBackupRestoreShowsAbortedSummaryAndEscapes(t *testing.T) {
	e := backupEnv(t)
	good := library.BackupItem{PluginID: "book", Kind: "book", Source: "books", SourceID: "1", Titles: []plugin.Title{{Lang: "en", Text: "Fine"}}, ProgressKeys: []string{"pages"}}
	evil := good
	evil.SourceID = "2"
	evil.Titles = []plugin.Title{{Lang: "en", Text: "<script>alert(1)</script>"}}
	evil.ExternalURL = "javascript:alert(1)"
	var items []string
	for _, it := range []library.BackupItem{good, evil} {
		b, _ := json.Marshal(it)
		items = append(items, string(b))
	}
	for i := 0; i < 205; i++ {
		u := good
		u.PluginID = fmt.Sprintf("plugin<%d>", i)
		b, _ := json.Marshal(u)
		items = append(items, string(b))
	}
	doc := `{"format":"allister.backup","format_version":1,"items":[` + strings.Join(items, ",") + `,{"broken": ]}`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("backup.json")
	w.Write([]byte(doc))
	zw.Close()
	rec := e.restoreUpload(t, buf.Bytes())
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "Restore summary", "The restore stopped", `data-count="created">1<`, `data-count="failed">1<`,
		`data-count="unknown">205<`, "and 5 more", "&lt;script&gt;alert(1)&lt;/script&gt;", "plugin&lt;0&gt;")
	mustNotContain(t, body, "<script>alert(1)</script>", "plugin<0>")
	if left := e.tmpFiles(t); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestBackupRestoreUploadCapIs413(t *testing.T) {
	e := backupEnv(t)
	if e.srv.maxBackupBody != 512<<20 {
		t.Fatalf("default cap = %d", e.srv.maxBackupBody)
	}
	e.srv.maxBackupBody = 1000
	rec := e.restoreUpload(t, bytes.Repeat([]byte("x"), 5000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rec.Code)
	}
	if left := e.tmpFiles(t); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestBackupRestoreOneAtATime(t *testing.T) {
	e := backupEnv(t)
	e.srv.restoreMu.Lock()
	rec := e.restoreUpload(t, []byte("x"))
	e.srv.restoreMu.Unlock()
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already running") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	// concurrent uploads: exactly the ones that find the lock free run
	var wg sync.WaitGroup
	codes := make([]int, 4)
	data := e.download(t)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.restoreUpload(t, data).Code
		}(i)
	}
	wg.Wait()
	for _, c := range codes {
		if c != 200 && c != http.StatusConflict {
			t.Fatalf("codes = %v", codes)
		}
	}
}

func TestBackupRoundTripsRealAnimePlugin(t *testing.T) {
	a := newEnv(t)
	d := anime.ItemData(a.fp.anime)
	id, err := a.lib.AddToList(context.Background(), "anime", "anime", d, library.Entry{Status: library.Active, Rating: ip(9),
		Progress: map[string]float64{"episode": 5}, Comment: "c <b>"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.lib.SaveCredits(context.Background(), id, []plugin.Credit{{Role: "character", Detail: "MAIN",
		Person: plugin.PersonRef{ID: "c1", Name: "Light", ImageURL: "https://img.example/l.jpg"},
		With:   &plugin.PersonRef{ID: "a1", Name: "Actor", ImageURL: "https://img.example/m.jpg"}, WithRole: "voice"}}); err != nil {
		t.Fatal(err)
	}
	data := a.download(t)
	b := newEnv(t)
	rec := b.restoreUpload(t, data)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), `data-count="created">1<`)
	if len(b.jobs.got) != 1 || b.jobs.got[0].Kind != jobs.KindSyncItem {
		t.Fatalf("jobs = %+v", b.jobs.got)
	}
	_, want, _ := backupJSON(t, data)
	_, got, _ := backupJSON(t, b.download(t))
	if !reflect.DeepEqual(got, want) || len(got) != 1 || len(got[0].Credits) != 1 || got[0].Entry == nil {
		t.Fatalf("round trip differs:\n%+v\n%+v", got, want)
	}
}

// serveRecover runs the request and returns what the handler panicked with.
func serveRecover(h http.Handler, req *http.Request) (rec *httptest.ResponseRecorder, panicked any) {
	rec = httptest.NewRecorder()
	defer func() { panicked = recover() }()
	h.ServeHTTP(rec, req)
	return rec, nil
}

func TestBackupDownloadFailureIsNeverAFinishedZip(t *testing.T) {
	// a failure before any byte was sent is a plain 500
	e := backupEnv(t)
	e.addFixture(t, "book", "book", bookItem("1", "B"), library.Entry{Status: library.Active})
	e.d.Close()
	rec, p := serveRecover(e.h, httptest.NewRequest("GET", "/settings/backup/download", nil))
	if p != nil || rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("closed db: panic %v, status %d, headers %v", p, rec.Code, rec.Header())
	}
	// a failure after bytes were sent aborts the connection (the browser
	// marks the download failed instead of saving a truncated zip)
	e = backupEnv(t)
	e.srv.backupWrite = func(_ context.Context, w io.Writer, _ *library.Library, _ []backup.PluginVersion, _ string, _ time.Time) error {
		w.Write([]byte("PK\x03\x04 partial"))
		return errors.New("database gone in batch 2")
	}
	_, p = serveRecover(e.h, httptest.NewRequest("GET", "/settings/backup/download", nil))
	if p != http.ErrAbortHandler {
		t.Fatalf("panic = %v, want http.ErrAbortHandler", p)
	}
	// a client that went away needs nothing
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec, p = serveRecover(e.h, httptest.NewRequest("GET", "/settings/backup/download", nil).WithContext(ctx))
	if p != nil {
		t.Fatalf("cancelled request panicked: %v", p)
	}
}
