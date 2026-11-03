package redact

import (
	"errors"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func scoped(t *testing.T, ns map[string]NamespaceRule) (*Redactor, func(sess, v string) string) {
	t.Helper()
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyToken, Namespace: "email"}}
	r := newRedactor(t, cat, ns)
	cols := []types.ColumnMeta{col("email", "text", types.PolicyToken, 1, 1)}
	return r, func(sess, v string) string {
		rows := [][]any{{v}}
		ctx := WithStatement(t.Context(), Statement{ConnectionID: "c1"})
		if _, err := r.Apply(ctx, sess, cols, rows); err != nil {
			t.Fatal(err)
		}
		return rows[0][0].(string)
	}
}

func Test_TOKEN_C1_SessionTokensDifferAcrossSessions(t *testing.T) {
	r, tok := scoped(t, nil)
	a, b := tok("s1", "jane@example.com"), tok("s2", "jane@example.com")
	if a == b {
		t.Fatalf("two sessions got the same session token %q", a)
	}
	for sess, token := range map[string]string{"s1": a, "s2": b} {
		if v, _, err := r.Resolve(t.Context(), sess, "c1", token); err != nil || v != "jane@example.com" {
			t.Errorf("%s could not resolve its own token: %q %v", sess, v, err)
		}
	}
}

func Test_TOKEN_C10_ASessionTokenIsUnknownToAnotherSession(t *testing.T) {
	r, tok := scoped(t, nil)
	a := tok("s1", "jane@example.com")
	if _, _, err := r.Resolve(t.Context(), "s2", "c1", a); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("another session resolved a session token: %v", err)
	}
}

func Test_TOKEN_C4_AFoldingNamespaceGivesOneToken(t *testing.T) {
	_, tok := scoped(t, map[string]NamespaceRule{"email": {CaseFold: true}})
	if a, b := tok("s1", "Jane@Example.com "), tok("s1", "jane@example.com"); a != b {
		t.Errorf("one value written two ways gave two tokens: %q vs %q", a, b)
	}
}

func Test_TOKEN_C11_ANonFoldingNamespaceKeepsCase(t *testing.T) {
	_, tok := scoped(t, nil)
	if a, b := tok("s1", "Jane"), tok("s1", "jane"); a == b {
		t.Errorf("Jane and jane gave one token %q without case folding", a)
	}
}

func TestShortValuesGiveTokensOfOneLength(t *testing.T) {
	_, tok := scoped(t, nil)
	if a, b := tok("s1", "abcde"), tok("s1", "abcdefghi"); len(a) != len(b) {
		t.Errorf("token lengths %d and %d tell the values apart", len(a), len(b))
	}
}
