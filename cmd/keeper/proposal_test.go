package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func TestPrintProposalSaysItOnlyProposedAndNamesTheCommandWithoutAPath(t *testing.T) {
	var out bytes.Buffer
	printProposal(&out, "prod", &ports.InitProposal{
		SafeToBulkAccept: map[string]types.ColumnPolicy{"public.t.id": {Policy: types.PolicyAllow}},
	})
	got := out.String()
	if !strings.Contains(got, "only proposed") || !strings.Contains(got, "keeper catalog edit prod <schema.table.column> --policy <policy>") {
		t.Errorf("output lacks the statement or the command:\n%s", got)
	}
	if strings.Contains(got, ".yaml") || strings.Contains(got, ".keeper") {
		t.Errorf("output names a path:\n%s", got)
	}
}
