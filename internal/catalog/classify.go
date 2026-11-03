package catalog

import (
	"fmt"
	"maps"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// classifyDefault is the policy a column gets when nobody has classified it:
// a name that says personal data wins, then the column's type decides. A token
// needs a text value to hash, so a personal name on any other type is redacted.
func classifyDefault(kind byte, col ports.Column) types.ColumnPolicy {
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
	}
	return types.ColumnPolicy{Policy: types.PolicyRedact}
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

// classify writes a default into the overlay for every column that has no entry
// in either file. A committed entry is a person's decision and an overlay entry
// is an earlier default, so neither is touched and a second run adds nothing.
func (c *Catalog) classify(relations []ports.Relation) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	merged := c.merged
	overlay := maps.Clone(c.overlay)
	c.mu.RUnlock()

	added := false
	for _, rel := range relations {
		for _, col := range rel.Columns {
			key := columnKey{Schema: rel.Ref.Schema, Table: rel.Ref.Relation, Column: col.Name}
			if _, ok := merged[key]; ok {
				continue
			}
			overlay[key] = classifyDefault(rel.Kind, col)
			added = true
		}
	}
	if !added {
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
