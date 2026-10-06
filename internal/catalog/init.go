package catalog

import (
	"context"
	"fmt"
	"slices"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Init proposes a policy for every column from its type (SPEC §5.4), refined by
// running sampled values through the connection's passes when sample > 0 (SPEC
// R5.3a, R5.3b). A connection with no passes gets no proposals: classifying is
// the operator's job, and keeper has nothing of its own to suggest. Init writes
// nothing; the caller reviews the proposal and accepts it through Put or Raise.
func (s *Store) Init(ctx context.Context, connID string, sample int) (*ports.InitProposal, error) {
	if _, err := s.get(connID); err != nil {
		return nil, err
	}
	if s.deps.Introspector == nil {
		return nil, fmt.Errorf("catalog: no introspector configured")
	}
	proposal := &ports.InitProposal{
		SafeToBulkAccept: make(map[string]types.ColumnPolicy),
		NeedsReview:      make(map[string]types.ColumnPolicy),
		SampleRates:      make(map[string]float64),
	}
	var det ports.Detector
	if s.deps.Detectors != nil {
		d, err := s.deps.Detectors(ctx, connID)
		if err != nil {
			proposal.Degradations = append(proposal.Degradations, types.Degradation{Layer: "detector", Reason: "the connection's passes could not be built"})
			return proposal, nil
		}
		det = d
	}
	if det == nil {
		return proposal, nil
	}
	relations, err := s.deps.Introspector.Introspect(ctx, connID)
	if err != nil {
		return nil, fmt.Errorf("catalog: introspecting %s: %w", connID, err)
	}
	in := &initRun{store: s, det: det, connID: connID, sample: sample, proposal: proposal}
	for _, rel := range relations {
		for _, col := range rel.Columns {
			in.proposeColumn(ctx, rel.Ref, col)
		}
	}
	return proposal, nil
}

// initRun is one Init over one connection.
type initRun struct {
	store    *Store
	det      ports.Detector
	connID   string
	sample   int
	proposal *ports.InitProposal
	degraded bool
}

// proposeColumn classifies one column per SPEC §5.4's type defaults:
//
//	typed scalar (numeric, boolean, datetime, uuid)  -> allow + tripwire
//	json, jsonb                                      -> scan, needs review
//	text, varchar                                    -> scan, needs review
//
// Sampling (SPEC R5.3a, R5.3b) promotes a text column, or a numeric non-key
// column, whose samples the passes find PII in at the threshold rate to token,
// under the entity the passes named. A column whose samples were not examined
// gets no proposal at all, so it cannot read as a column found clean.
func (in *initRun) proposeColumn(ctx context.Context, rel types.RelationRef, col ports.Column) {
	key := columnKey{Schema: rel.Schema, Table: rel.Relation, Column: col.Name}.String()
	family := Family(col.TypeOID)

	switch {
	case family == ports.FamilyText && isJSONType(col.TypeOID):
		in.proposal.NeedsReview[key] = types.ColumnPolicy{Policy: types.PolicyScan}

	case family == ports.FamilyText:
		entity, rate, ok := in.sampleRate(ctx, rel, col)
		if !ok {
			return
		}
		if rate > 0 {
			in.proposal.SampleRates[key] = rate
		}
		if rate >= sampleTokenThreshold && entity != "" {
			in.proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyToken, Namespace: entity}
			return
		}
		in.proposal.NeedsReview[key] = types.ColumnPolicy{Policy: types.PolicyScan}

	case family == ports.FamilyNumeric:
		entity, rate, ok := in.sampleRate(ctx, rel, col)
		if !ok {
			return
		}
		in.proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyAllow}
		if rate < sampleTokenThreshold || entity == "" {
			return
		}
		in.proposal.SampleRates[key] = rate
		if col.IsPK || col.IsFK || col.IsIdentity || col.HasDefault {
			return // report only: pg_constraint/pg_attrdef say this is a key, not an identifier.
		}
		in.proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyToken, Namespace: entity}

	default:
		in.proposal.SafeToBulkAccept[key] = types.ColumnPolicy{Policy: types.PolicyAllow}
	}
}

// sampleRate runs a column's samples through the passes and reports the entity
// found in the largest share of them, with that share. ok is false only when
// samples existed and were not examined. Without sampling the rate is zero.
func (in *initRun) sampleRate(ctx context.Context, rel types.RelationRef, col ports.Column) (entity string, rate float64, ok bool) {
	if in.sample <= 0 || in.store.deps.Sampler == nil {
		return "", 0, true
	}
	samples, err := in.store.deps.Sampler.SampleColumn(ctx, in.connID, rel, col.Name, in.sample)
	if err != nil || len(samples) == 0 {
		return "", 0, true
	}
	res, err := in.det.Detect(ctx, samples)
	if err != nil || len(res) < len(samples) || slices.ContainsFunc(res, func(s []ports.Span) bool { return s == nil }) {
		in.degrade()
		return "", 0, false
	}
	hits := map[string]int{}
	for _, spans := range res {
		seen := map[string]bool{}
		for _, sp := range spans {
			if !seen[sp.Type] {
				seen[sp.Type] = true
				hits[sp.Type]++
			}
		}
	}
	for typ, n := range hits {
		if n > hits[entity] || (n == hits[entity] && typ < entity) {
			entity = typ
		}
	}
	return entity, float64(hits[entity]) / float64(len(samples)), true
}

func (in *initRun) degrade() {
	if in.degraded {
		return
	}
	in.degraded = true
	in.proposal.Degradations = append(in.proposal.Degradations, types.Degradation{Layer: "detector", Reason: "sampled values could not be examined, so their columns have no proposal"})
}
