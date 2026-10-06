package daemon

import (
	"fmt"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestResultsKeepsOnlyTheMostRecent(t *testing.T) {
	var r results
	for i := range keptResults + 1 {
		r.keep(&types.QueryResult{AuditID: fmt.Sprint(i), RowCount: i})
	}
	if r.get("0") != nil {
		t.Error("the oldest result outlived the bound")
	}
	if got := r.get(fmt.Sprint(keptResults)); got == nil || got.RowCount != keptResults {
		t.Errorf("the newest result is missing: %+v", got)
	}
	if len(r.order) != keptResults || len(r.byID) != keptResults {
		t.Errorf("holding %d ids and %d results, want %d", len(r.order), len(r.byID), keptResults)
	}
}
