package api

import "net/http"

// routes is CONTRACT §3, one line per row of its two tables, with the access
// rule beside each. Reading this function is how you check the claim that the
// agent surface is socket-only and that the human surface is unreachable from an
// MCP session.
func (s *Server) routes() {
	// --- agent surface, socket only -------------------------------------
	s.handle("POST /v1/session", accessHandshake, s.openSession)
	s.handle("POST /v1/connections/{id}/explain", accessAgent, s.explain)
	s.handle("POST /v1/connections/{id}/query", accessAgent, s.query)
	s.handle("GET /v1/tickets/{id}", accessAgent, s.ticket)
	s.handle("POST /v1/tickets/{id}/cancel", accessAgent, s.cancelTicket)
	s.handle("POST /v1/tickets/{id}/answer", accessAgent, s.answerTicket)
	s.handle("POST /v1/requests", accessAgent, s.openRequest)
	s.handle("GET /v1/requests/{id}", accessLocalPage, s.requestState)

	// --- description, read-only, either surface --------------------------
	s.handle("GET /v1/connections", accessShared, s.listConnections)
	s.handle("GET /v1/connections/{id}", accessShared, s.describeConnection)
	s.handle("GET /v1/connections/{id}/schema", accessShared, s.schema)
	s.handle("GET /v1/version", accessPublic, s.version)

	// --- human surface, CLI over the socket and the UI over loopback -----
	s.handle("GET /v1/hosts", accessHuman, s.listHosts)
	s.handle("POST /v1/hosts", accessHuman, s.registerHost)
	s.handle("DELETE /v1/hosts/{id}", accessHuman, s.removeHost)
	s.handle("POST /v1/connections", accessHuman, s.registerConnection)
	s.handle("PATCH /v1/connections/{id}", accessHuman, s.patchConnection)
	s.handle("DELETE /v1/connections/{id}", accessHuman, s.removeConnection)
	s.handle("POST /v1/connections/{id}/test", accessHuman, s.testConnection)

	s.handle("GET /v1/approvals", accessHuman, s.listApprovals)
	s.handle("GET /v1/requests", accessHuman, s.listRequests)
	s.handle("GET /v1/approvals/{ticket}", accessHuman, s.getApproval)
	s.handle("POST /v1/approvals/{ticket}/decide", accessHuman, s.decide)

	s.handle("GET /v1/catalog/{connection}", accessHuman, s.getCatalog)
	s.handle("PUT /v1/catalog/{connection}/columns", accessHuman, s.putCatalogColumns)
	s.handle("POST /v1/catalog/{connection}/init", accessHuman, s.initCatalog)

	s.handle("GET /v1/activity", accessHuman, s.activity)
	s.handle("GET /v1/activity/{audit_id}", accessHuman, s.activityRecord)

	s.handle("GET /v1/doctor", accessHuman, s.doctor)
	s.handle("GET /v1/settings", accessHuman, s.getSettings)
	s.handle("PUT /v1/settings", accessHuman, s.putSettings)
	s.handle("POST /v1/daemon/shutdown", accessHuman, s.shutdown)

	// The vault opens itself and never locks, so these two are all that is left
	// of its surface, and both are socket only, because the loopback port is open to every process on the machine: take a copy, restore one, and change the key it is under. All are
	// human-only — an agent that could export the vault would be carrying off
	// every connection keeper exists to stand in front of.
	s.handle("POST /v1/vault/export", accessCLI, s.exportVault)
	s.handle("POST /v1/vault/import", accessCLI, s.importVault)

	s.handle("GET /v1/ui-session", accessCLI, s.uiSession)
	s.handle("POST /v1/ui-session/claim", accessLoopback, s.claimUISession)

	s.raw("GET /v1/events", accessHuman, s.events)

	// --- resolving a local request, the browser's side -------------------
	// Loopback only. The kind is fixed when the request is minted, and the page
	// reads it from GET /v1/requests/{id} above.
	s.handle("POST /v1/requests/{id}/submit", accessLoopback, s.submitRequest)
	s.handle("POST /v1/requests/{id}/cancel", accessLoopback, s.cancelRequest)

	// A path keeper does not serve is not found for every method; without
	// these the UI's catch-all would turn it into a 405 for anything but GET.
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		s.mux.HandleFunc(method+" /v1/", http.NotFound)
	}

	// --- the embedded UI, loopback ---------------------------------------
	s.raw("GET /auth", accessPublic, s.serveAuth)
	s.raw("GET /r/{request_id}", accessPublic, s.serveApp)
	s.raw("GET /", accessPublic, s.serveApp)
}
