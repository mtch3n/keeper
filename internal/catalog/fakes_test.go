package catalog

import (
	"context"
	"strings"

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

// fakeDetector stands in for a connection's passes. The sample value "HIT"
// is an email and nine digits are a US SSN, so a test can dial in an exact
// hit rate by choosing its samples. err fails every call.
type fakeDetector struct{ err error }

func (f fakeDetector) Detect(_ context.Context, texts []string) ([][]ports.Span, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]ports.Span, len(texts))
	for i, t := range texts {
		out[i] = []ports.Span{}
		switch {
		case t == "HIT":
			out[i] = append(out[i], ports.Span{Start: 0, End: len(t), Type: "email_address"})
		case len(t) == 9 && strings.Trim(t, "0123456789") == "":
			out[i] = append(out[i], ports.Span{Start: 0, End: len(t), Type: "us_ssn"})
		}
	}
	return out, nil
}

func (fakeDetector) Identity() ports.DetectorIdentity { return ports.DetectorIdentity{Name: "fake"} }

func detectorFor(d ports.Detector) ports.DetectorFor {
	return func(context.Context, string) (ports.Detector, error) { return d, nil }
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
