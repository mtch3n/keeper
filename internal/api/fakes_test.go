package api_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// ---------------------------------------------------------------- vault

type fakeVault struct {
	mu          sync.Mutex
	locked      bool
	conns       map[string]*types.Connection
	dsns        map[string]string
	registered  []*types.Connection
	acceptCalls []string
}

func newVault(cs ...*types.Connection) *fakeVault {
	v := &fakeVault{conns: map[string]*types.Connection{}, dsns: map[string]string{}}
	for _, c := range cs {
		v.conns[c.ID] = c
	}
	return v
}

func (v *fakeVault) Unlock(context.Context, string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.locked = false
	return nil
}

func (v *fakeVault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.locked = true
}

func (v *fakeVault) Locked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.locked
}

func (v *fakeVault) KeySource() string { return "test" }

func (v *fakeVault) Connections(context.Context) ([]*types.Connection, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]*types.Connection, 0, len(v.conns))
	for _, c := range v.conns {
		out = append(out, c)
	}
	return out, nil
}

func (v *fakeVault) Connection(_ context.Context, id string) (*types.Connection, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.conns[id]
	if !ok {
		return nil, errors.New("no such connection")
	}
	return c, nil
}

func (v *fakeVault) Register(_ context.Context, c *types.Connection, readDSN, writeDSN string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.conns[c.ID] = c
	v.dsns[c.ID] = readDSN
	v.registered = append(v.registered, c)
	_ = writeDSN
	return nil
}

func (v *fakeVault) Update(_ context.Context, c *types.Connection) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.conns[c.ID] = c
	return nil
}

func (v *fakeVault) Remove(_ context.Context, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.conns, id)
	return nil
}

func (v *fakeVault) DSN(_ context.Context, id string, _ ports.Role) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.dsns[id], nil
}

func (v *fakeVault) Accept(_ context.Context, id string, findings []string, actor, via string) (*types.Connection, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.acceptCalls = append(v.acceptCalls, via)
	c := v.conns[id]
	if c == nil {
		return nil, errors.New("no such connection")
	}
	for _, f := range findings {
		c.Acceptances = append(c.Acceptances, types.Acceptance{FindingID: f, Actor: actor, Via: via, At: time.Now()})
	}
	c.Enabled = len(c.Unaccepted()) == 0
	return c, nil
}

func (v *fakeVault) TokenKey(context.Context, string, int) ([]byte, int, error) {
	return []byte("k"), 1, nil
}
func (v *fakeVault) Export(context.Context) ([]byte, error) { return nil, nil }
func (v *fakeVault) RotateMaster(context.Context) error     { return nil }

// ---------------------------------------------------------------- auditor

type fakeAuditor struct{ findings []types.Finding }

func (a *fakeAuditor) Audit(context.Context, string, ports.Role) ([]types.Finding, error) {
	return a.findings, nil
}

// ---------------------------------------------------------------- catalog

type fakeCatalog struct {
	mu       sync.Mutex
	byName   map[string]types.ColumnPolicy
	readable map[string][]string
	fresh    bool
	entries  map[string]types.ColumnPolicy
	unclass  []string
}

func newCatalog() *fakeCatalog {
	return &fakeCatalog{
		byName:   map[string]types.ColumnPolicy{},
		readable: map[string][]string{},
		fresh:    true,
		entries:  map[string]types.ColumnPolicy{},
	}
}

func (c *fakeCatalog) Lookup(uint32, uint16) (types.ColumnPolicy, bool) {
	return types.ColumnPolicy{}, false
}

func (c *fakeCatalog) LookupName(rel types.RelationRef, col string) (types.ColumnPolicy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.byName[rel.String()+"."+col]
	return p, ok
}

func (c *fakeCatalog) Relation(uint32) (types.RelationRef, bool) { return types.RelationRef{}, false }
func (c *fakeCatalog) RelationPolicies(uint32) []ports.FamilyPolicy {
	return nil
}

func (c *fakeCatalog) Readable(_ context.Context, rel types.RelationRef) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cols, ok := c.readable[rel.String()]
	if !ok {
		return nil, errors.New("unknown relation")
	}
	return cols, nil
}

func (c *fakeCatalog) Fresh(context.Context, []uint32) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fresh, nil
}

func (c *fakeCatalog) Entries(context.Context, string) (map[string]types.ColumnPolicy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]types.ColumnPolicy{}
	for k, v := range c.entries {
		out[k] = v
	}
	return out, nil
}

func (c *fakeCatalog) Put(_ context.Context, _ string, entries map[string]types.ColumnPolicy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range entries {
		c.entries[k] = v
	}
	return nil
}

func (c *fakeCatalog) Raise(context.Context, string, string, types.ColumnPolicy) error { return nil }

func (c *fakeCatalog) Init(context.Context, string, int) (*ports.InitProposal, error) {
	return &ports.InitProposal{SafeToBulkAccept: map[string]types.ColumnPolicy{}}, nil
}

func (c *fakeCatalog) SuggestGrants(context.Context, string) ([]string, error) {
	return []string{"GRANT SELECT (id) ON public.users TO app_ro;"}, nil
}

func (c *fakeCatalog) Unclassified(context.Context, string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unclass, nil
}

// ---------------------------------------------------------------- executor

type fakeExecutor struct {
	mu       sync.Mutex
	rels     []ports.Relation
	closed   []string
	failIntr bool
}

func (e *fakeExecutor) Describe(context.Context, string, string, []ports.Param) ([]types.ColumnMeta, error) {
	return nil, nil
}
func (e *fakeExecutor) Plan(context.Context, string, string, []ports.Param) (*ports.PlanFacts, error) {
	return &ports.PlanFacts{}, nil
}
func (e *fakeExecutor) Run(context.Context, string, string, []ports.Param, int) (*ports.RawResult, error) {
	return &ports.RawResult{}, nil
}
func (e *fakeExecutor) PreviewWrite(context.Context, string, string, []ports.Param) (*ports.RawResult, error) {
	return &ports.RawResult{}, nil
}
func (e *fakeExecutor) CommitWrite(context.Context, string, string, []ports.Param) (*ports.RawResult, error) {
	return &ports.RawResult{}, nil
}

func (e *fakeExecutor) Introspect(context.Context, string) ([]ports.Relation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failIntr {
		return nil, errors.New("introspection unavailable")
	}
	return e.rels, nil
}

func (e *fakeExecutor) SampleColumn(context.Context, string, types.RelationRef, string, int) ([]string, error) {
	return nil, nil
}

func (e *fakeExecutor) Close(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = append(e.closed, id)
}

// ---------------------------------------------------------------- redactor

type fakeRedactor struct {
	mu      sync.Mutex
	minted  map[string]string // token -> value, per session key
	dropped []string
	n       int
}

func newRedactor() *fakeRedactor { return &fakeRedactor{minted: map[string]string{}} }

func (r *fakeRedactor) Apply(context.Context, string, []types.ColumnMeta, [][]any) (map[string]types.Transform, error) {
	return nil, nil
}

func (r *fakeRedactor) Mint(_ context.Context, sessionID, _, namespace, value string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	tok := "<" + namespace + ":" + strconv.Itoa(r.n) + ">"
	r.minted[sessionID+"|"+tok] = value
	return tok, nil
}

func (r *fakeRedactor) Resolve(_ context.Context, sessionID, token string) (string, types.Policy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.minted[sessionID+"|"+token]
	if !ok {
		return "", "", errors.New("unknown token")
	}
	return v, types.PolicyToken, nil
}

func (r *fakeRedactor) DropSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropped = append(r.dropped, sessionID)
	for k := range r.minted {
		if len(k) > len(sessionID) && k[:len(sessionID)] == sessionID {
			delete(r.minted, k)
		}
	}
}

func (r *fakeRedactor) droppedSessions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dropped...)
}

// ---------------------------------------------------------------- audit log

type fakeAuditLog struct {
	mu      sync.Mutex
	records []types.AuditRecord
	reject  string
}

func (a *fakeAuditLog) Write(_ context.Context, r *types.AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, *r)
	return nil
}

func (a *fakeAuditLog) Query(context.Context, ports.AuditFilter) ([]types.AuditRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]types.AuditRecord(nil), a.records...), nil
}

func (a *fakeAuditLog) Get(_ context.Context, id string) (*types.AuditRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.records {
		if a.records[i].ID == id {
			return &a.records[i], nil
		}
	}
	return nil, nil
}

func (a *fakeAuditLog) Normalize(sql string, _ func(string) bool) string { return sql }

func (a *fakeAuditLog) ScreenIntent(_ context.Context, intent string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reject != "" && intent == a.reject {
		return errors.New("intent carries identifying text")
	}
	return nil
}

// ---------------------------------------------------------------- judge

type fakeJudge struct{}

func (fakeJudge) Assess(context.Context, ports.JudgeRequest) (*ports.JudgeVerdict, error) {
	return &ports.JudgeVerdict{Tier: types.Tier1Record}, nil
}
func (fakeJudge) Available(context.Context) bool { return true }
func (fakeJudge) Identity() string               { return "fake-judge" }

// ---------------------------------------------------------------- detector

type fakeDetector struct{}

func (fakeDetector) Scan(context.Context, string) ([]ports.Span, error) { return nil, nil }
func (fakeDetector) Identity() ports.DetectorIdentity {
	return ports.DetectorIdentity{Name: "fake", NetworkPosture: "none"}
}
