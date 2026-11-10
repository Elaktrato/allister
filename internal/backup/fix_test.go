package backup

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allister/allister/internal/library"
	"github.com/allister/allister/internal/plugin"
)

// craftedDir builds an archive of n identical central directory headers
// (46 bytes plus the one-byte name "a") followed by an end record that
// declares `declared` entries, a comment and trailing bytes. It is cheap:
// one header repeated, no file data.
func craftedDir(n int, declared uint16, comment string, trailing []byte) []byte {
	le := binary.LittleEndian
	hdr := make([]byte, 47)
	le.PutUint32(hdr, 0x02014b50)
	le.PutUint16(hdr[28:], 1)
	hdr[46] = 'a'
	dir := bytes.Repeat(hdr, n)
	end := make([]byte, 22)
	le.PutUint32(end, 0x06054b50)
	le.PutUint16(end[8:], declared)
	le.PutUint16(end[10:], declared)
	le.PutUint32(end[12:], uint32(len(dir)))
	le.PutUint32(end[16:], 0)
	le.PutUint16(end[20:], uint16(len(comment)))
	out := append(dir, end...)
	out = append(out, comment...)
	return append(out, trailing...)
}

func wantReject(t *testing.T, err error, substr string) {
	t.Helper()
	var rej *RejectError
	if !errors.As(err, &rej) || !strings.Contains(err.Error(), substr) {
		t.Fatalf("err = %v, want a reject mentioning %q", err, substr)
	}
}

func TestBackupZipDirectoryIsWalkedBeforeLoading(t *testing.T) {
	t.Run("declared count wraps at 16 bits", func(t *testing.T) {
		data := craftedDir(65536+5, 5, "", nil)
		// archive/zip itself accepts this: it compares only the low 16 bits
		if zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil || len(zr.File) != 65541 {
			t.Fatalf("premise: archive/zip read %v, %v", zr, err)
		}
		e := newEnv(t)
		_, err := e.restore(data, Limits{}, nil)
		wantReject(t, err, "directory")
	})
	t.Run("walked count over the cap", func(t *testing.T) {
		e := newEnv(t)
		_, err := e.restore(craftedDir(3000, 3000, "", nil), Limits{MaxEntries: 1000}, nil)
		wantReject(t, err, "more than 1000 files")
		// a declared count that is small does not hide the real directory
		_, err = e.restore(craftedDir(3000, 3000-2048, "", nil), Limits{MaxEntries: 1000}, nil)
		wantReject(t, err, "")
	})
	t.Run("trailing bytes after the end record", func(t *testing.T) {
		zipped := zipOf(t, entry{name: "backup.json", body: jsonOf(t, bookItem("1"))}, entry{name: "covers/a.png", body: pngBytes(1)})
		data := append(zipped, []byte("TRAILING BYTES")...)
		if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("premise: archive/zip refuses trailing bytes: %v", err)
		}
		e := newEnv(t)
		_, err := e.restore(data, Limits{MaxEntries: 1}, nil)
		wantReject(t, err, "more than 1 files")
		rep := mustRestore(t, e, data)
		if rep.Created != 1 {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("zip64 directory", func(t *testing.T) {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for i := 0; i < 70000; i++ { // over 65535 entries: zip64 end records
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprint("e", i), Method: zip.Store}); err != nil {
				t.Fatal(err)
			}
		}
		zw.Close()
		data := b.Bytes()
		if n, decl, err := scanDirectory(bytes.NewReader(data), int64(len(data)), 200_000); err != nil || n != 70000 || decl != 70000 {
			t.Fatalf("scan = %d %d, %v", n, decl, err)
		}
		e := newEnv(t)
		_, err := e.restore(data, Limits{MaxEntries: 69999}, nil)
		wantReject(t, err, "more than 69999 files")
	})
	t.Run("fake end record in the comment", func(t *testing.T) {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		zw.SetComment("PK\x05\x06 a fake record in the comment is not trusted")
		w, _ := zw.Create("backup.json")
		w.Write(jsonOf(t, bookItem("1")))
		zw.Close()
		data := b.Bytes()
		_, goErr := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		e := newEnv(t)
		rep, err := e.restore(data, Limits{}, nil)
		// consistent with archive/zip: refused as not a zip when it refuses
		if goErr != nil {
			wantReject(t, err, "not a zip")
		} else if err != nil || rep.Created != 1 {
			t.Fatalf("restore = %+v, %v", rep, err)
		}
	})
}

func TestBackupJSONKeyRules(t *testing.T) {
	item, _ := itemJSON(t, bookItem("1"))
	item2, _ := itemJSON(t, bookItem("2"))
	head := `{"format":"allister.backup","format_version":1,`
	for _, tc := range []struct{ name, doc, want string }{
		{"second format", `{"format":"allister.backup","format":"allister.backup","format_version":1,"items":[` + item + `]}`, `"format"`},
		{"second version", `{"format":"allister.backup","format_version":1,"format_version":1,"items":[` + item + `]}`, `"format_version"`},
		{"items first", `{"items":[` + item + `],"format":"allister.backup","format_version":1}`, `must come before`},
		{"version after items", `{"format":"allister.backup","items":[` + item + `],"format_version":1}`, `must come before`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(tc.doc)}), Limits{}, nil)
			wantReject(t, err, tc.want)
			if n := itemCount(t, e); n != 0 {
				t.Fatalf("restored %d items", n)
			}
		})
	}
	// a second items list after the first was restored stops the restore
	e := newEnv(t)
	rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: []byte(head + `"items":[` + item + `],"items":[` + item2 + `]}`)}), Limits{}, nil)
	if err != nil || rep.Created != 1 || !strings.Contains(rep.Aborted, `"items"`) {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if n := itemCount(t, e); n != 1 {
		t.Fatalf("items = %d", n)
	}
	// format repeated after the items is refused as well
	e = newEnv(t)
	rep, err = e.restore(zipOf(t, entry{name: "backup.json", body: []byte(head + `"items":[` + item + `],"format":"allister.backup"}`)}), Limits{}, nil)
	if err != nil || !strings.Contains(rep.Aborted, `"format"`) {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func itemJSON(t *testing.T, it library.BackupItem) (string, error) {
	t.Helper()
	b := jsonOf(t, it)
	s := string(b)
	return s[len(header("1")) : len(s)-2], nil
}

func TestBackupItemSizeCap(t *testing.T) {
	big := bookItem("2")
	big.Titles = []plugin.Title{{Lang: "en", Text: strings.Repeat("x", 280)}}
	data := zipOf(t, entry{name: "backup.json", body: jsonOf(t, bookItem("1"), big, bookItem("3"))})
	e := newEnv(t)
	rep, err := e.restore(data, Limits{MaxItemBytes: 250}, nil)
	if err != nil || rep.Created != 1 || !strings.Contains(rep.Aborted, "larger than 250 bytes") {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if DefaultLimits().MaxItemBytes != 4<<20 {
		t.Fatalf("default item cap = %d", DefaultLimits().MaxItemBytes)
	}
	// many items just under the cap pass (the decoder's read-ahead does not count)
	var items []any
	for i := 0; i < 50; i++ {
		it := bookItem(fmt.Sprint("n", i))
		it.Titles = []plugin.Title{{Lang: "en", Text: strings.Repeat("y", 100)}}
		if j, _ := itemJSON(t, it); len(j) < 200 || len(j) > 248 {
			t.Fatalf("item size %d is not just under the cap", len(j))
		}
		items = append(items, it)
	}
	e = newEnv(t)
	rep, err = e.restore(zipOf(t, entry{name: "backup.json", body: jsonOf(t, items...)}), Limits{MaxItemBytes: 250}, nil)
	if err != nil || rep.Aborted != "" || rep.Created != 50 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestBackupRestoreRemovesCoverOfAnItemNotWritten(t *testing.T) {
	e := newEnv(t)
	if _, err := e.db.Exec(`CREATE TRIGGER fail_boom BEFORE INSERT ON item_titles WHEN NEW.text = 'Boom'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	ok := bookItem("1")
	ok.Cover = "shared.png"
	boom := bookItem("2")
	boom.Titles = []plugin.Title{{Lang: "en", Text: "Boom"}}
	boom.Cover = "shared.png"
	// the shared cover is referenced by the restored item: it stays
	data := zipOf(t, entry{name: "backup.json", body: jsonOf(t, ok, boom)}, entry{name: "covers/shared.png", body: pngBytes(1)})
	if _, err := e.restore(data, Limits{}, nil); err == nil {
		t.Fatal("the trigger did not fail the restore")
	}
	if files := e.files(); len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	// a cover only the failed item used is removed again
	e2 := newEnv(t)
	if _, err := e2.db.Exec(`CREATE TRIGGER fail_boom BEFORE INSERT ON item_titles WHEN NEW.text = 'Boom'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	boom.Cover = "own.png"
	data = zipOf(t, entry{name: "backup.json", body: jsonOf(t, boom)}, entry{name: "covers/own.png", body: pngBytes(2)})
	if _, err := e2.restore(data, Limits{}, nil); err == nil {
		t.Fatal("the trigger did not fail the restore")
	}
	if files := e2.files(); len(files) != 0 {
		t.Fatalf("orphan cover left: %v", files)
	}
	if _, err := os.Stat(filepath.Join(e2.covers)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestBackupRestoreQueuesManualItemsWithRemoteCovers(t *testing.T) {
	e := newEnv(t)
	plain := bookItem("m1")
	plain.Source = "manual"
	remote := bookItem("m2")
	remote.Source = "manual"
	remote.CoverURL = "https://img.example/m2.jpg"
	var queued []int64
	rep, err := e.restore(zipOf(t, entry{name: "backup.json", body: jsonOf(t, plain, remote, bookItem("s"))}), Limits{},
		func(id int64, _ string) { queued = append(queued, id) })
	if err != nil || rep.Created != 3 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if len(queued) != 2 {
		t.Fatalf("queued = %v (want the remote-cover manual item and the sourced item)", queued)
	}
}
