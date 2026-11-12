package manga

import (
	"reflect"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

func TestKindStats(t *testing.T) {
	k := manifest().Kinds[0]
	want := &plugin.StatsDef{Units: &plugin.StatsUnitsDef{Key: "chapter", LabelPlural: "Chapters"}}
	if !reflect.DeepEqual(k.Stats, want) {
		t.Fatalf("stats = %+v", k.Stats)
	}
}
