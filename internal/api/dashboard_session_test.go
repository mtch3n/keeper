package api_test

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

const sessionNeeded types.Code = "ui_session_needed"

// routesFile lists every route with its access rule. Reading it from the source
// is what makes a new route without a decision here a failing test.
func routesFromSource(t *testing.T) (human, cliOnly []string) {
	t.Helper()
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`s\.(?:handle|raw)\("(\w+) (\S+)", (access\w+)`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		route := m[1] + " " + regexp.MustCompile(`\{\w+\}`).ReplaceAllString(m[2], "x")
		switch m[3] {
		case "accessHuman", "accessShared":
			human = append(human, route)
		case "accessCLI":
			cliOnly = append(cliOnly, route)
		}
	}
	if len(human) < 20 {
		t.Fatalf("read only %d routes from routes.go", len(human))
	}
	return human, cliOnly
}

func TestLoopbackWithoutTheDashboardSessionServesNoHumanRoute(t *testing.T) {
	r := newRig(t)
	v := r.visitor()
	human, cliOnly := routesFromSource(t)
	for _, route := range human {
		method, path, _ := strings.Cut(route, " ")
		resp, raw := v.do(method, path, map[string]any{})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s answered %d without the dashboard session: %s", route, resp.StatusCode, raw)
			continue
		}
		e := decodeError(t, raw)
		if e.Code != sessionNeeded || !strings.Contains(e.Action, "keeper ui") {
			t.Errorf("%s: want ui_session_needed naming `keeper ui`, got %+v", route, e)
		}
	}
	for _, route := range cliOnly {
		method, path, _ := strings.Cut(route, " ")
		for _, c := range []*client{v, r.browser()} {
			if resp, raw := c.do(method, path, map[string]any{}); resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s on loopback answered %d: %s", route, resp.StatusCode, raw)
			}
		}
	}
}

func TestAValidCSRFTokenWithoutTheSessionChangesNothing(t *testing.T) {
	r := newRig(t)
	v := r.visitor()
	resp, raw := v.do("PATCH", "/v1/connections/c1", map[string]any{"writes": "allow"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("PATCH without the session: %d %s", resp.StatusCode, raw)
	}
	if w := r.vault.conns["c1"].Writes; w == types.WritesAllow {
		t.Fatal("writes changed without the dashboard session")
	}

	rg := escalatingRig(t)
	var tk types.Ticket
	rg.agent().mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	resp, raw = rg.visitor().do("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("decide without the session: %d %s", resp.StatusCode, raw)
	}
	if got := len(rg.pipe.seenAuths()); got != 1 {
		t.Fatalf("a statement ran without a decision: %d calls", got)
	}
}

func TestEventStreamNeedsTheDashboardSession(t *testing.T) {
	r := newRig(t)
	resp, raw := r.visitor().do("GET", "/v1/events", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("events without the session: %d %s", resp.StatusCode, raw)
	}
}

func TestClaimWithAWrongSecretFailsAndTheRightOneOpensTheSession(t *testing.T) {
	r := newRig(t)
	v := r.visitor()
	resp, raw := v.do("POST", "/v1/ui-session/claim", map[string]any{"secret": "guess"})
	if resp.StatusCode != http.StatusUnauthorized || len(resp.Cookies()) != 0 {
		t.Fatalf("a wrong secret: %d %s cookies %v", resp.StatusCode, raw, resp.Cookies())
	}
	if resp, _ := v.do("POST", "/v1/ui-session/claim", map[string]any{}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an empty secret: %d", resp.StatusCode)
	}

	_, secret := r.d.UISession()
	if len(secret) < 43 {
		t.Fatalf("the secret carries too little entropy: %d chars", len(secret))
	}
	resp, raw = v.do("POST", "/v1/ui-session/claim", map[string]any{"secret": secret})
	if resp.StatusCode/100 != 2 {
		t.Fatalf("the right secret: %d %s", resp.StatusCode, raw)
	}
	var c *http.Cookie
	for _, k := range resp.Cookies() {
		if k.Name == "keeper_ui" {
			c = k
		}
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Value == "" {
		t.Fatalf("the session cookie is %+v", c)
	}
	v.header.Set("Cookie", "keeper_ui="+c.Value)
	if resp, raw := v.do("GET", "/v1/approvals", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the claimed session does not open a route: %d %s", resp.StatusCode, raw)
	}
	v.header.Set("Cookie", "keeper_ui="+c.Value+"x")
	if resp, _ := v.do("GET", "/v1/approvals", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a changed cookie was accepted: %d", resp.StatusCode)
	}
}

func TestOnlyAPersonOnTheSocketGetsTheDashboardSecret(t *testing.T) {
	r := newRig(t)
	var out struct {
		Base   string `json:"base"`
		Secret string `json:"secret"`
	}
	r.socket().mustJSON("GET", "/v1/ui-session", nil, &out)
	base, secret := r.d.UISession()
	if out.Secret != secret || out.Base != base || secret == "" {
		t.Fatalf("the socket answered %+v", out)
	}

	resp, raw := r.agent().do("GET", "/v1/ui-session", nil)
	if resp.StatusCode != http.StatusForbidden || strings.Contains(string(raw), secret) {
		t.Fatalf("an agent session reached the secret: %d %s", resp.StatusCode, raw)
	}
	for _, c := range []*client{r.visitor(), r.browser()} {
		resp, raw := c.do("GET", "/v1/ui-session", nil)
		if resp.StatusCode/100 == 2 || strings.Contains(string(raw), secret) {
			t.Fatalf("loopback served the secret: %d %s", resp.StatusCode, raw)
		}
	}
}

func TestTheSecretReachesNothingAnAgentReadsOnlyTheNotification(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	_, secret := r.d.UISession()

	var seen []string
	raw := a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, nil)
	seen = append(seen, string(raw))
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	seen = append(seen, tk.Prompt, tk.URL)
	seen = append(seen, string(a.mustJSON("GET", "/v1/tickets/"+tk.ID, nil, nil)))
	var in types.LocalRequest
	seen = append(seen, string(a.mustJSON("POST", "/v1/requests", map[string]any{
		"kind": types.RequestInput, "connection_id": "c1", "namespace": "email",
	}, &in)))
	seen = append(seen, string(a.mustJSON("GET", "/v1/requests/"+in.ID, nil, nil)))
	for i, s := range seen {
		if strings.Contains(s, secret) {
			t.Errorf("agent-visible text %d holds the dashboard secret: %s", i, s)
		}
	}

	eventually(t, "the notification", func() bool { return len(r.notes.seen()) >= 2 })
	link := r.notes.seen()[0]
	if !strings.HasSuffix(link, "#"+secret) || !strings.Contains(link, "/auth?next=/r/") {
		t.Errorf("the notification link is %q", link)
	}
}

func TestTheLocalInputPageNeedsOnlyItsCapability(t *testing.T) {
	r := newRig(t)
	a := r.agent()
	v := r.visitor()
	var in types.LocalRequest
	a.mustJSON("POST", "/v1/requests", map[string]any{
		"kind": types.RequestInput, "connection_id": "c1", "namespace": "email", "purpose": "find orders",
	}, &in)
	var page map[string]any
	v.mustJSON("GET", "/v1/requests/"+in.ID, nil, &page)
	if page["kind"] != string(types.RequestInput) {
		t.Fatalf("the page read %+v", page)
	}
	if resp, raw := v.do("POST", "/v1/requests/"+in.ID+"/cancel", map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %s", resp.StatusCode, raw)
	}
}

func TestTheAppAndTheClaimPageAreServedWithoutTheSession(t *testing.T) {
	r := newRig(t)
	v := r.visitor()
	for _, p := range []string{"/", "/auth", "/r/anything", "/v1/version"} {
		if resp, _ := v.do("GET", p, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s answered %d", p, resp.StatusCode)
		}
	}
}
