package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// The pipeline is designed to be exercised without a database: every port is a
// seam, and these are the doubles behind them.

// --- catalog ---------------------------------------------------------------

type colKey struct {
	oid uint32
	att uint16
}

type testColumn struct {
	name   string
	attnum uint16
	family ports.TypeFamily
	policy types.ColumnPolicy
}

type testRelation struct {
	ref  types.RelationRef
	oid  uint32
	cols []testColumn
}

type fakeCatalog struct {
	byIdentity map[colKey]types.ColumnPolicy
	byName     map[types.RelationRef]map[string]types.ColumnPolicy
	relations  map[uint32]types.RelationRef
	policies   map[uint32][]ports.FamilyPolicy
	readable   map[types.RelationRef][]string
	fresh      bool
	freshErr   error
}

func newCatalog(rels ...testRelation) *fakeCatalog {
	c := &fakeCatalog{
		byIdentity: map[colKey]types.ColumnPolicy{},
		byName:     map[types.RelationRef]map[string]types.ColumnPolicy{},
		relations:  map[uint32]types.RelationRef{},
		policies:   map[uint32][]ports.FamilyPolicy{},
		readable:   map[types.RelationRef][]string{},
		fresh:      true,
	}
	for _, r := range rels {
		c.relations[r.oid] = r.ref
		c.byName[r.ref] = map[string]types.ColumnPolicy{}
		for _, col := range r.cols {
			c.byIdentity[colKey{r.oid, col.attnum}] = col.policy
			c.byName[r.ref][col.name] = col.policy
			c.readable[r.ref] = append(c.readable[r.ref], col.name)
			if col.policy.Policy != types.PolicyAllow {
				c.policies[r.oid] = append(c.policies[r.oid], ports.FamilyPolicy{
					Column: col.name, Family: col.family, Policy: col.policy,
				})
			}
		}
	}
	return c
}

func (c *fakeCatalog) Lookup(oid uint32, att uint16) (types.ColumnPolicy, bool) {
	p, ok := c.byIdentity[colKey{oid, att}]
	return p, ok
}

func (c *fakeCatalog) LookupName(rel types.RelationRef, column string) (types.ColumnPolicy, bool) {
	p, ok := c.byName[rel][column]
	return p, ok
}

func (c *fakeCatalog) Relation(oid uint32) (types.RelationRef, bool) {
	r, ok := c.relations[oid]
	return r, ok
}

func (c *fakeCatalog) RelationPolicies(oid uint32) []ports.FamilyPolicy { return c.policies[oid] }

func (c *fakeCatalog) Readable(_ context.Context, rel types.RelationRef) ([]string, error) {
	return c.readable[rel], nil
}

func (c *fakeCatalog) Fresh(context.Context, []uint32) (bool, error) { return c.fresh, c.freshErr }

// --- redactor --------------------------------------------------------------

const maskMarker = "MASKED"

type fakeRedactor struct{ applyCalls int }

func (r *fakeRedactor) Apply(_ context.Context, _ string, cols []types.ColumnMeta, rows [][]any) (map[string]types.Transform, error) {
	r.applyCalls++
	out := map[string]types.Transform{}
	for i, c := range cols {
		out[c.Name] = types.Transform{Policy: c.Policy}
		if !c.Policy.Masks() {
			continue
		}
		for _, row := range rows {
			if i < len(row) {
				row[i] = maskMarker
			}
		}
	}
	return out, nil
}

func (r *fakeRedactor) Mint(context.Context, string, string, string, string) (string, error) {
	return "⟨e1:000000⟩", nil
}

func (r *fakeRedactor) Resolve(context.Context, string, string) (string, types.Policy, error) {
	return "", types.PolicyAllow, errors.New("no token")
}

func (r *fakeRedactor) DropSession(string) {}

// --- executor --------------------------------------------------------------

type fakeExecutor struct {
	cols []types.ColumnMeta
	rows [][]any

	plan *ports.PlanFacts

	describeErr error
	planErr     error
	runErr      error
	previewErr  error
	commitErr   error

	previewTag int64
	commitTag  int64

	calls []string
}

func (e *fakeExecutor) record(name string) { e.calls = append(e.calls, name) }

func (e *fakeExecutor) called(name string) bool {
	for _, c := range e.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (e *fakeExecutor) Describe(context.Context, string, string, []ports.Param) ([]types.ColumnMeta, error) {
	e.record("Describe")
	if e.describeErr != nil {
		return nil, e.describeErr
	}
	return e.cols, nil
}

func (e *fakeExecutor) Plan(context.Context, string, string, []ports.Param) (*ports.PlanFacts, error) {
	e.record("Plan")
	if e.planErr != nil {
		return nil, e.planErr
	}
	return e.plan, nil
}

func (e *fakeExecutor) result(tag int64) *ports.RawResult {
	rows := make([][]any, len(e.rows))
	for i, r := range e.rows {
		rows[i] = append([]any(nil), r...)
	}
	return &ports.RawResult{
		Columns:    e.cols,
		Rows:       rows,
		RowCount:   len(rows),
		CommandTag: tag,
		Duration:   time.Millisecond,
	}
}

func (e *fakeExecutor) Run(context.Context, string, string, []ports.Param, int) (*ports.RawResult, error) {
	e.record("Run")
	if e.runErr != nil {
		return nil, e.runErr
	}
	return e.result(0), nil
}

func (e *fakeExecutor) PreviewWrite(context.Context, string, string, []ports.Param) (*ports.RawResult, error) {
	e.record("PreviewWrite")
	if e.previewErr != nil {
		return nil, e.previewErr
	}
	return e.result(e.previewTag), nil
}

func (e *fakeExecutor) CommitWrite(context.Context, string, string, []ports.Param) (*ports.RawResult, error) {
	e.record("CommitWrite")
	if e.commitErr != nil {
		return nil, e.commitErr
	}
	return e.result(e.commitTag), nil
}

func (e *fakeExecutor) Introspect(context.Context, string) ([]ports.Relation, error) { return nil, nil }

func (e *fakeExecutor) SampleColumn(context.Context, string, types.RelationRef, string, int) ([]string, error) {
	return nil, nil
}

func (e *fakeExecutor) Close(string) {}

// --- audit -----------------------------------------------------------------

type fakeAudit struct {
	records  []*types.AuditRecord
	writeErr error
}

func (a *fakeAudit) Write(_ context.Context, r *types.AuditRecord) error {
	if a.writeErr != nil {
		return a.writeErr
	}
	a.records = append(a.records, r)
	return nil
}

func (a *fakeAudit) Query(context.Context, ports.AuditFilter) ([]types.AuditRecord, error) {
	return nil, nil
}

func (a *fakeAudit) Get(context.Context, string) (*types.AuditRecord, error) { return nil, nil }

func (a *fakeAudit) Normalize(sql string, _ func(string) bool) string { return "NORMALIZED:" + sql }

func (a *fakeAudit) ScreenIntent(context.Context, string) error { return nil }

func (a *fakeAudit) last(t *testing.T) *types.AuditRecord {
	t.Helper()
	if len(a.records) == 0 {
		t.Fatal("G9 was skipped: no audit record was written")
	}
	return a.records[len(a.records)-1]
}

// --- judge -----------------------------------------------------------------

type fakeJudge struct {
	available bool
	verdict   *ports.JudgeVerdict
	err       error
	calls     int
}

func (j *fakeJudge) Assess(context.Context, ports.JudgeRequest) (*ports.JudgeVerdict, error) {
	j.calls++
	if j.err != nil {
		return nil, j.err
	}
	return j.verdict, nil
}

func (j *fakeJudge) Available(context.Context) bool { return j.available }
func (j *fakeJudge) Identity() string               { return "fake-judge" }

// --- authority -------------------------------------------------------------

type fakeAuthority struct {
	conn   *types.Connection
	grants map[types.PathRef]*types.Grant
	err    error
}

func (a *fakeAuthority) Connection(context.Context, string) (*types.Connection, error) {
	return a.conn, a.err
}

func (a *fakeAuthority) Grant(_ context.Context, _ string, path types.PathRef) (*types.Grant, bool) {
	g, ok := a.grants[path]
	return g, ok
}

// --- the schema every test shares ------------------------------------------
//
// SPEC §7.6.1's measured schema, plus two relations that give the other side of
// each branch: orders holds a sensitive text column and no sensitive numeric
// one, and clean_ids holds nothing sensitive at all.

const (
	usersOID    uint32 = 1001
	ordersOID   uint32 = 1002
	cleanOID    uint32 = 1003
	userStatsID uint32 = 1004
)

var (
	usersRef  = types.RelationRef{Schema: "public", Relation: "users"}
	ordersRef = types.RelationRef{Schema: "public", Relation: "orders"}
	cleanRef  = types.RelationRef{Schema: "public", Relation: "clean_ids"}
)

func testSchema() *fakeCatalog {
	return newCatalog(
		testRelation{ref: usersRef, oid: usersOID, cols: []testColumn{
			{name: "id", attnum: 1, family: ports.FamilyNumeric, policy: types.ColumnPolicy{Policy: types.PolicyAllow}},
			{name: "email", attnum: 2, family: ports.FamilyText, policy: types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "email"}},
			{name: "ssn", attnum: 3, family: ports.FamilyText, policy: types.ColumnPolicy{Policy: types.PolicyDrop}},
			{name: "salary", attnum: 4, family: ports.FamilyNumeric, policy: types.ColumnPolicy{Policy: types.PolicyRedact}},
			{name: "status", attnum: 5, family: ports.FamilyText, policy: types.ColumnPolicy{Policy: types.PolicyScan}},
			{name: "internal_flag", attnum: 6, family: ports.FamilyBoolean, policy: types.ColumnPolicy{Policy: types.PolicyRedact, HideName: true}},
		}},
		testRelation{ref: ordersRef, oid: ordersOID, cols: []testColumn{
			{name: "id", attnum: 1, family: ports.FamilyNumeric, policy: types.ColumnPolicy{Policy: types.PolicyAllow}},
			{name: "total", attnum: 2, family: ports.FamilyNumeric, policy: types.ColumnPolicy{Policy: types.PolicyAllow}},
			{name: "user_email", attnum: 3, family: ports.FamilyText, policy: types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "email"}},
		}},
		testRelation{ref: cleanRef, oid: cleanOID, cols: []testColumn{
			{name: "id", attnum: 1, family: ports.FamilyNumeric, policy: types.ColumnPolicy{Policy: types.PolicyAllow}},
			{name: "label", attnum: 2, family: ports.FamilyText, policy: types.ColumnPolicy{Policy: types.PolicyAllow}},
		}},
	)
}

func testConnection() *types.Connection {
	return &types.Connection{
		ID:       "acme_prod",
		Name:     "acme prod",
		Engine:   "postgres",
		Database: "acme",
		Role:     "acme_prod_app_ro",
		Mode:     types.ModeAssisted,
		Limits:   types.DefaultLimits(),
	}
}

func testSession() types.Session {
	return types.Session{
		ID:     "sess-1",
		Client: types.ClientInfo{Name: "keeper-mcp", Version: "0.1.0"},
		Intent: "reconcile last week's orders",
	}
}

// --- harness ---------------------------------------------------------------

type harness struct {
	pipe      *Pipeline
	catalog   *fakeCatalog
	redactor  *fakeRedactor
	exec      *fakeExecutor
	audit     *fakeAudit
	judge     *fakeJudge
	authority *fakeAuthority
}

func newHarness(t *testing.T, mutate ...func(*harness)) *harness {
	t.Helper()
	h := &harness{
		catalog:   testSchema(),
		redactor:  &fakeRedactor{},
		exec:      &fakeExecutor{},
		audit:     &fakeAudit{},
		authority: &fakeAuthority{conn: testConnection(), grants: map[types.PathRef]*types.Grant{}},
	}
	for _, m := range mutate {
		m(h)
	}
	var judge ports.Judge
	if h.judge != nil {
		judge = h.judge
	}
	p, err := New(Deps{
		Catalogs:  func(string) (ports.Catalog, error) { return h.catalog, nil },
		Redactor:  h.redactor,
		Executor:  h.exec,
		Audit:     h.audit,
		Judge:     judge,
		Authority: h.authority,
		Now:       func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.pipe = p
	return h
}

func (h *harness) query(t *testing.T, req Request) *Decision {
	t.Helper()
	if req.ConnID == "" {
		req.ConnID = h.authority.conn.ID
	}
	if req.Session.ID == "" {
		req.Session = testSession()
	}
	dec, err := h.pipe.Query(t.Context(), req)
	if err != nil {
		t.Fatalf("Query returned an infrastructure error: %v", err)
	}
	return dec
}

// readPlan is an ordinary read over the named relations.
func readPlan(rows int64, relations ...types.RelationRef) *ports.PlanFacts {
	oids := map[types.RelationRef]uint32{usersRef: usersOID, ordersRef: ordersOID, cleanRef: cleanOID}
	p := &ports.PlanFacts{StatementType: "SELECT", EstimatedRows: rows, EstimatedCost: 12.5}
	for _, r := range relations {
		p.RelationNames = append(p.RelationNames, r)
		p.Relations = append(p.Relations, oids[r])
	}
	return p
}

func computed(name, typeName string) types.ColumnMeta {
	return types.ColumnMeta{Name: name, Type: typeName} // (0,0): the server says computed
}

func fromColumn(name, typeName string, oid uint32, att uint16) types.ColumnMeta {
	return types.ColumnMeta{Name: name, Type: typeName, TableOID: oid, AttNum: att}
}

func transformFor(t *testing.T, dec *Decision, name string) types.Transform {
	t.Helper()
	if dec.Result == nil {
		t.Fatalf("no result to read transforms from (error=%v)", dec.Error)
	}
	tr, ok := dec.Result.Transforms[name]
	if !ok {
		t.Fatalf("no transform for %q; have %v", name, dec.Result.Transforms)
	}
	return tr
}

func columnNames(cols []types.ColumnMeta) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
