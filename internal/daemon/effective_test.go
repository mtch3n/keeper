package daemon

import (
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestEffectiveSettingsOfStoredValues(t *testing.T) {
	c := &types.Connection{ID: "c1", Name: "prod", Reads: "", Writes: "approve"}
	h := connectionHealth(c)
	if h.Reads != types.ReadsAlways {
		t.Errorf("stored empty reads show as %q, want always", h.Reads)
	}
	if h.Writes != types.WritesOff {
		t.Errorf("stored approve writes show as %q, want off", h.Writes)
	}
	if got := updated(c); got.Reads != types.ReadsAlways || got.Writes != types.WritesOff {
		t.Errorf("update result shows reads %q writes %q", got.Reads, got.Writes)
	}
	if c.Reads != "" || c.Writes != "approve" {
		t.Error("normalising changed the stored record")
	}
	ok := &types.Connection{Reads: types.ReadsNever, Writes: types.WritesAsk}
	if h := connectionHealth(ok); h.Reads != types.ReadsNever || h.Writes != types.WritesAsk {
		t.Errorf("valid values changed: %q %q", h.Reads, h.Writes)
	}
}
