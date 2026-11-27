package manga

import "testing"

// TestSourceStatisticsRefresh: the community score and popularity are
// statistics the source keeps current, so the bulk update refreshes them.
func TestSourceStatisticsRefresh(t *testing.T) {
	seen := 0
	for _, k := range manifest().Kinds {
		for _, f := range k.Fields {
			switch f.Key {
			case "community_score", "popularity":
				seen++
				if !f.SourceRefresh || f.Editable {
					t.Errorf("%s.%s: SourceRefresh %v, Editable %v; want true, false", k.Key, f.Key, f.SourceRefresh, f.Editable)
				}
			}
		}
	}
	if seen < 2 {
		t.Fatalf("saw %d statistic fields, want at least 2", seen)
	}
}
