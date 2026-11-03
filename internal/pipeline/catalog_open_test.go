package pipeline

import (
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func TestCatalogOpenFailureKeepsItsCode(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.plan = readPlan(1, cleanRef)
	})
	opener := &types.Error{Code: types.CodeUnreachable, Summary: "keeper could not reach the database", Action: "check the host"}
	h.pipe.catalogs = func(string) (ports.Catalog, error) { return nil, opener }

	dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})
	if dec.Error == nil || dec.Error.Code != types.CodeUnreachable {
		t.Errorf("query: error = %+v, want code %q", dec.Error, types.CodeUnreachable)
	}
	_, ke, err := h.pipe.Explain(t.Context(), Request{ConnID: h.authority.conn.ID, Session: testSession(), SQL: "SELECT id FROM clean_ids"})
	if err != nil {
		t.Fatal(err)
	}
	if ke == nil || ke.Code != types.CodeUnreachable {
		t.Errorf("explain: error = %+v, want code %q", ke, types.CodeUnreachable)
	}

	h.pipe.catalogs = func(string) (ports.Catalog, error) { return nil, nil }
	if dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"}); dec.Error == nil || dec.Error.Summary != "this connection has no catalog" {
		t.Errorf("a nil catalog with no error lost its message: %+v", dec.Error)
	}
}
