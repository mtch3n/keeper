package catalog

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// fakeIntrospector is a canned Introspector for tests that never touch a
// real database.
type fakeIntrospector struct {
	relations []ports.Relation
	err       error
}

func (f *fakeIntrospector) Introspect(context.Context, string) ([]ports.Relation, error) {
	return f.relations, f.err
}

// fakeSampler returns canned sample values keyed by "schema.table.column",
// standing in for ports.Executor.SampleColumn's local, no-egress read.
type fakeSampler struct {
	values map[string][]string
}

func (f *fakeSampler) SampleColumn(_ context.Context, _ string, rel types.RelationRef, column string, _ int) ([]string, error) {
	key := rel.Schema + "." + rel.Relation + "." + column
	return f.values[key], nil
}

// fakeNames is a canned NameHeuristic: exact column-name matches only.
type fakeNames struct {
	matches map[string]string
}

func (f *fakeNames) MatchName(column string) (string, bool) {
	ns, ok := f.matches[column]
	return ns, ok
}

// fakeRuleMatcher treats the literal sample value "HIT" as a match for the
// "email" rule and everything else as a miss, so a test can dial in an exact
// hit rate by choosing how many "HIT" values a fakeSampler returns.
type fakeRuleMatcher struct{}

func (fakeRuleMatcher) MatchRate(_ context.Context, samples []string) (string, float64) {
	if len(samples) == 0 {
		return "", 0
	}
	hits := 0
	for _, s := range samples {
		if s == "HIT" {
			hits++
		}
	}
	return "email", float64(hits) / float64(len(samples))
}

// fakePrivileges is a canned PrivilegeChecker.
type fakePrivileges struct {
	columns map[string][]string
	err     error
}

func (f *fakePrivileges) ReadableColumns(_ context.Context, _ string, rel types.RelationRef) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.columns[rel.Schema+"."+rel.Relation], nil
}

// fakeFingerprints is a canned Fingerprinter, keyed by table OID.
type fakeFingerprints struct {
	byOID map[uint32]string
	err   error
}

func (f *fakeFingerprints) Fingerprint(_ context.Context, _ string, tableOID uint32) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.byOID[tableOID], nil
}
