package backup

import (
	"bytes"
	"strings"
	"testing"
)

func (e *appearanceEnv) restoreZone(data []byte) *Report {
	e.t.Helper()
	rep, err := Restore(bg, bytes.NewReader(data), int64(len(data)), Options{Library: e.lib, CoversDir: e.covers, Appearance: e.app, TimeZone: e.set})
	if err != nil {
		e.t.Fatal(err)
	}
	if rep.Aborted != "" {
		e.t.Fatalf("aborted: %s", rep.Aborted)
	}
	return rep
}

func zoneArchive(t *testing.T, body string) []byte {
	t.Helper()
	return zipOf(t, entry{name: "backup.json", body: []byte(body)})
}

func (e *appearanceEnv) zone() string {
	s, err := e.set.StoredTimeZone(bg)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestRestoreWithoutTimeZoneMember(t *testing.T) {
	e := newAppearanceEnv(t)
	rep := e.restoreZone(zoneArchive(t, `{"format":"allister.backup","format_version":1,"items":[]}`))
	if e.zone() != "" || len(rep.Appearance) != 0 {
		t.Errorf("zone %q notes %v", e.zone(), rep.Appearance)
	}
}

func TestRestoreTimeZoneAfterItems(t *testing.T) {
	e := newAppearanceEnv(t)
	e.restoreZone(zoneArchive(t, `{"format":"allister.backup","format_version":1,"items":[],"time_zone":"Europe/Berlin"}`))
	if e.zone() != "Europe/Berlin" {
		t.Errorf("zone %q", e.zone())
	}
}

func TestRestoreTimeZoneBeforeHeaderStoresNothingWhenRefused(t *testing.T) {
	e := newAppearanceEnv(t)
	data := zoneArchive(t, `{"time_zone":"Europe/Berlin","format":"nope","format_version":1,"items":[]}`)
	if _, err := Restore(bg, bytes.NewReader(data), int64(len(data)), Options{Library: e.lib, CoversDir: e.covers, TimeZone: e.set}); err == nil {
		t.Fatal("refused archive accepted")
	}
	if e.zone() != "" {
		t.Errorf("stored %q", e.zone())
	}
}

func TestRestoreTimeZoneInvalidIgnoredWithNote(t *testing.T) {
	for _, v := range []string{`"Mars/Olympus"`, `"Local"`, `42`, `{"a":1}`, `null`} {
		e := newAppearanceEnv(t)
		rep := e.restoreZone(zoneArchive(t, `{"format":"allister.backup","format_version":1,"items":[],"time_zone":`+v+`}`))
		if e.zone() != "" {
			t.Errorf("%s: stored %q", v, e.zone())
		}
		if v != `null` && !strings.Contains(strings.Join(rep.Appearance, "|"), "Time zone") && !strings.Contains(strings.Join(rep.Appearance, "|"), "time zone") {
			t.Errorf("%s: no note: %v", v, rep.Appearance)
		}
	}
}

func TestRestoreTimeZoneOwnKeptNote(t *testing.T) {
	e := newAppearanceEnv(t)
	if err := e.set.SetTimeZone(bg, "Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	rep := e.restoreZone(zoneArchive(t, `{"format":"allister.backup","format_version":1,"items":[],"time_zone":"Europe/Berlin"}`))
	if e.zone() != "Asia/Tokyo" || !strings.Contains(strings.Join(rep.Appearance, "|"), "Time zone kept: this installation has its own.") {
		t.Errorf("zone %q notes %v", e.zone(), rep.Appearance)
	}
}
