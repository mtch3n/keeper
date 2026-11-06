package catalog

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// classifyDefault is the policy a column gets when nobody has classified it:
// a name that says personal data wins, then the column's type decides. A token
// needs a text value to hash, so a personal name on any other type is redacted.
//
// Unnamed text is scanned only when the planner's statistics say it is short
// and has few distinct values, like a status or a tier: pattern detection does
// not find a person's name, and free text is where a name is written. Free text,
// and text with no statistics yet, is redacted.
func classifyDefault(kind byte, col ports.Column, shape TextShape, known bool) types.ColumnPolicy {
	if kind == 'v' || kind == 'm' {
		// A view column can be a base column under another name; its mask is not
		// carried over, so only a person's declaration lets it through.
		return types.ColumnPolicy{Policy: types.PolicyRedact}
	}
	family := Family(col.TypeOID)
	if p, ok := contactByName(col.Name); ok {
		return tokenIfText(p, family)
	}
	if p, ok := personalByName(col.Name); ok {
		return tokenIfText(p, family)
	}
	switch family {
	case ports.FamilyNumeric, ports.FamilyBoolean, ports.FamilyDateTime, ports.FamilyUUID:
		return types.ColumnPolicy{Policy: types.PolicyAllow}
	case ports.FamilyText:
		// A column called just "name" names a person as often as a product,
		// and its values are short and few, so the statistics cannot tell.
		if secretByName(col.Name) || strings.EqualFold(col.Name, "name") {
			return types.ColumnPolicy{Policy: types.PolicyRedact}
		}
		// Detection masks the identifiers it finds and the rest of the text
		// shows. With detection off, or a detector that fails, the cell is
		// redacted whole, so this is never weaker than redact.
		if known && shape.categorical() {
			return types.ColumnPolicy{Policy: types.PolicyScan}
		}
		return types.ColumnPolicy{Policy: types.PolicyRedact}
	}
	return types.ColumnPolicy{Policy: types.PolicyRedact}
}

// secretByName is a text column whose name says it holds a credential.
// Detection does not recognise a key or a hash, so scanning would release it.
func secretByName(name string) bool {
	return nameHas(name)("key", "api_key", "apikey", "secret", "client_secret", "token",
		"access_token", "refresh_token", "password", "passwd", "password_hash", "hash",
		"salt", "private_key", "credential", "credentials", "otp", "pin")
}

func tokenIfText(p types.ColumnPolicy, family ports.TypeFamily) types.ColumnPolicy {
	if p.Policy == types.PolicyToken && family != ports.FamilyText {
		return types.ColumnPolicy{Policy: types.PolicyRedact}
	}
	return p
}

// contactByName covers what personalByName leaves to detection: a name that says
// the column holds an email, a phone number or a secret.
func contactByName(name string) (types.ColumnPolicy, bool) {
	has := nameHas(name)
	switch {
	case has("email", "e_mail", "email_address"):
		return types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "email"}, true
	case has("phone", "mobile", "telephone"):
		return types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "phone"}, true
	case has("ssn", "card_number", "password"):
		return types.ColumnPolicy{Policy: types.PolicyRedact}, true
	}
	return types.ColumnPolicy{}, false
}

// classify rewrites the overlay as the default for every column with no
// committed entry. A committed entry is a person's decision and is never
// touched. The overlay holds only defaults, so it is recomputed rather than
// kept: a default written by an older rule would otherwise outlive the rule.
func (c *Catalog) classify(relations []ports.Relation, shapes map[columnKey]TextShape) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	committed := c.committed
	before := c.overlay
	c.mu.RUnlock()

	overlay := map[columnKey]types.ColumnPolicy{}
	for _, rel := range relations {
		for _, col := range rel.Columns {
			key := columnKey{Schema: rel.Ref.Schema, Table: rel.Ref.Relation, Column: col.Name}
			if _, ok := committed[key]; ok {
				continue
			}
			shape, known := shapes[key]
			overlay[key] = classifyDefault(rel.Kind, col, shape, known)
		}
	}
	if maps.EqualFunc(overlay, before, func(a, b types.ColumnPolicy) bool { return reflect.DeepEqual(a, b) }) {
		return nil
	}
	if err := saveYAMLFile(c.paths.Overlay, overlay); err != nil {
		return fmt.Errorf("catalog: saving automatic classification: %w", err)
	}
	return c.reload()
}

// AutoMasked reports whether any of the result's columns was masked by an
// automatic classification, or has no classification at all.
func (c *Catalog) AutoMasked(cols []types.ColumnMeta) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range cols {
		if m.TableOID == 0 {
			continue
		}
		cc, ok := c.cache.lookupColumn(m.TableOID, m.AttNum)
		if !ok {
			continue
		}
		p, classified := c.merged[cc.Column]
		if !classified {
			return true
		}
		if _, auto := c.overlay[cc.Column]; auto && p.Policy != types.PolicyAllow {
			return true
		}
	}
	return false
}

// TextShape is what the planner's statistics say about a text column, without
// reading a value: how many distinct values it holds and their average width.
type TextShape struct {
	Distinct float64
	AvgWidth int
}

// categorical is a column like a status, a tier or a carrier: few values, all
// short. Its thresholds keep prose out: a sentence is wider than 32 bytes.
func (s TextShape) categorical() bool {
	return s.Distinct > 0 && s.Distinct <= 100 && s.AvgWidth > 0 && s.AvgWidth <= 32
}

// TextStats reads pg_stats for every text column the role can read.
// internal/pgdb's DB satisfies it.
type TextStats interface {
	TextShapes(ctx context.Context, connID string) (map[types.RelationRef]map[string]TextShape, error)
}
