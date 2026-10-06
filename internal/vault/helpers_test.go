package vault

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

// jsonUnmarshalForTest applies the same duration codec production code uses,
// so a test can parse Export's output without duplicating that concern.
func jsonUnmarshalForTest(data []byte, v *document) error {
	return json.Unmarshal(data, v, jsonOptions...)
}

// testHost registers a host on v and returns its id.
func testHost(t *testing.T, v *Vault) string {
	t.Helper()
	h := types.Host{Name: "db1", Address: "host", Port: 5432, SSLMode: "require"}
	if err := v.RegisterHost(context.Background(), &h); err != nil {
		t.Fatalf("RegisterHost: %v", err)
	}
	return h.ID
}

// testConnection returns a minimal, valid connection for use as test fixture
// data. Callers that need Register to assign an id should pass "".
func testConnection(id string) types.Connection {
	return types.Connection{
		ID:       id,
		Name:     "test",
		Engine:   "postgres",
		Database: "app",
		Role:     "app_ro",
		Mode:     types.ModeAssisted,
		Limits:   types.DefaultLimits(),
	}
}
