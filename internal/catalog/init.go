package catalog

import (
	"context"
	"fmt"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Init proposes a policy for every column from type and name heuristics
// (SPEC §5.4), refined by row sampling when sample > 0 (SPEC R5.3a, R5.3b).
// It writes nothing: the caller reviews the proposal and accepts it through
// Put or Raise.
func (s *Store) Init(ctx context.Context, connID string, sample int) (*ports.InitProposal, error) {
	if _, err := s.get(connID); err != nil {
		return nil, err
	}
	if s.deps.Introspector == nil {
		return nil, fmt.Errorf("catalog: no introspector configured")
	}
	relations, err := s.deps.Introspector.Introspect(ctx, connID)
	if err != nil {
		return nil, fmt.Errorf("catalog: introspecting %s: %w", connID, err)
	}

	proposal := &ports.InitProposal{
		SafeToBulkAccept: make(map[string]types.ColumnPolicy),
		NeedsReview:      make(map[string]types.ColumnPolicy),
		SampleRates:      make(map[string]float64),
	}
	for _, rel := range relations {
		for _, col := range rel.Columns {
			s.proposeColumn(ctx, connID, rel.Ref, col, sample, proposal)
		}
	}
	return proposal, nil
}

// proposeColumn classifies one column into proposal's SafeToBulkAccept or
// NeedsReview, per SPEC §5.4's type defaults:
//
//	typed scalar (numeric, boolean, datetime, uuid)  -> allow + tripwire
//	json, jsonb                                      -> scan, needs review
//	text, varchar matching the PII name heuristic     -> token
//	text, varchar otherwise                          -> scan, needs review
//
// Sampling (SPEC R5.3a, R5.3b) can promote a text column with no name match
// to token, and a numeric non-key column shaped like an SSN or a card number
// to token; both promotions are still bulk-safe, since they are backed by
// measured evidence rather than a guess.
func (s *Store) proposeColumn(ctx context.Context, connID string, rel types.RelationRef, col ports.Column, sample int, proposal *ports.InitProposal) {
	key := columnKey{Schema: rel.Schema, Table: rel.Relation, Column: col.Name}.String()
	family := Family(col.TypeOID)

	switch {
	case family == ports.FamilyText && isJSONType(col.TypeOID):
		proposal.NeedsReview[key] = types.ColumnPolicy{Policy: types.PolicyScan}

	case family == ports.FamilyText:
		s.proposeText(ctx, connID, rel, col, sample, key, proposal)

	default:
		proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyAllow}
		if family == ports.FamilyNumeric && sample > 0 && s.deps.Sampler != nil {
			s.proposeNumeric(ctx, connID, rel, col, sample, key, proposal)
		}
	}
}

// proposeText handles one text/varchar-family column (json/jsonb excluded by
// the caller). The name heuristic runs first and is definitive when it
// matches; sampling only runs to rescue a mis-named column the heuristic
// missed — SPEC's "contact", "ref", "external_id" example.
func (s *Store) proposeText(ctx context.Context, connID string, rel types.RelationRef, col ports.Column, sample int, key string, proposal *ports.InitProposal) {
	if s.deps.Names != nil {
		if namespace, ok := s.deps.Names.MatchName(col.Name); ok {
			proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyToken, Namespace: namespace}
			return
		}
	}

	if sample > 0 && s.deps.Sampler != nil && s.deps.Rules != nil {
		samples, err := s.deps.Sampler.SampleColumn(ctx, connID, rel, col.Name, sample)
		if err == nil && len(samples) > 0 {
			namespace, rate := s.deps.Rules.MatchRate(ctx, samples)
			if rate > 0 {
				proposal.SampleRates[key] = rate
			}
			if rate >= sampleTokenThreshold && namespace != "" {
				proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyToken, Namespace: namespace}
				return
			}
		}
	}

	// No heuristic match and no sampled evidence at the threshold: SPEC's
	// review task. This is where unnamed name and address columns live.
	proposal.NeedsReview[key] = types.ColumnPolicy{Policy: types.PolicyScan}
}

// proposeNumeric applies SPEC R5.3b to one numeric-family column: sample it,
// and if 9-digit-or-Luhn-valid values reach the bulk-accept threshold,
// propose token on a non-key column and report-only on a key column, since
// nothing but key status separates an SSN column from an id column of the
// same shape.
func (s *Store) proposeNumeric(ctx context.Context, connID string, rel types.RelationRef, col ports.Column, sample int, key string, proposal *ports.InitProposal) {
	samples, err := s.deps.Sampler.SampleColumn(ctx, connID, rel, col.Name, sample)
	if err != nil || len(samples) == 0 {
		return
	}

	var matches, nineDigitHits, luhnHits int
	for _, v := range samples {
		n9 := isNineDigit(v)
		lv := luhnValid(v)
		if n9 {
			nineDigitHits++
		}
		if lv {
			luhnHits++
		}
		if n9 || lv {
			matches++
		}
	}
	rate := float64(matches) / float64(len(samples))
	if rate < sampleTokenThreshold {
		return
	}
	proposal.SampleRates[key] = rate

	if col.IsPK || col.IsFK || col.IsIdentity || col.HasDefault {
		return // report only: pg_constraint/pg_attrdef say this is a key, not an SSN.
	}

	namespace := "ssn"
	if luhnHits > nineDigitHits {
		namespace = "card"
	}
	proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyToken, Namespace: namespace}
}
