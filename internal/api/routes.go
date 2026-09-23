package api

// routes is CONTRACT §3, one line per row of its two tables, with the access
// rule beside each. Reading this function is how you check the claim that the
// agent surface is socket-only and that the human surface is unreachable from an
// MCP session.
func (s *Server) routes() {
	// --- agent surface, socket only -------------------------------------
	s.handle("POST /v1/session", accessHandshake, s.openSession)
	s.handle("POST /v1/session/intent", accessAgent, s.setIntent)
	s.handle("POST /v1/connections/{id}/explain", accessAgent, s.explain)
	s.handle("POST /v1/connections/{id}/query", accessAgent, s.query)
	s.handle("GET /v1/tickets/{id}", accessAgent, s.ticket)
	s.handle("POST /v1/requests", accessAgent, s.openRequest)
	s.handle("GET /v1/requests/{id}", accessShared, s.requestState)

	// --- description, read-only, either surface --------------------------
	s.handle("GET /v1/connections", accessShared, s.listConnections)
	s.handle("GET /v1/connections/{id}", accessShared, s.describeConnection)
	s.handle("GET /v1/connections/{id}/schema", accessShared, s.schema)
	s.handle("GET /v1/version", accessPublic, s.version)

	// --- human surface, CLI over the socket and the UI over loopback -----
	s.handle("POST /v1/connections", accessHuman, s.registerConnection)
	s.handle("GET /v1/audit", accessHuman, s.listAudits)
	s.handle("POST /v1/connections/{id}/audit", accessHuman, s.auditConnection)
	s.handle("PATCH /v1/connections/{id}", accessHuman, s.patchConnection)
	s.handle("DELETE /v1/connections/{id}", accessHuman, s.removeConnection)
	s.handle("PUT /v1/connections/{id}/denylist", accessHuman, s.putDenylist)

	s.handle("GET /v1/approvals", accessHuman, s.listApprovals)
	s.handle("POST /v1/approvals/{ticket}/decide", accessHuman, s.decide)

	s.handle("GET /v1/grants", accessHuman, s.listGrants)
	s.handle("POST /v1/grants", accessHuman, s.createGrant)
	s.handle("DELETE /v1/grants/{id}", accessHuman, s.revokeGrant)

	s.handle("GET /v1/catalog/{connection}", accessHuman, s.getCatalog)
	s.handle("PUT /v1/catalog/{connection}/columns", accessHuman, s.putCatalogColumns)
	s.handle("POST /v1/catalog/{connection}/init", accessHuman, s.initCatalog)
	s.handle("POST /v1/catalog/{connection}/grants", accessHuman, s.suggestGrants)

	s.handle("GET /v1/activity", accessHuman, s.activity)
	s.handle("GET /v1/activity/{audit_id}", accessHuman, s.activityRecord)

	s.handle("GET /v1/doctor", accessHuman, s.doctor)
	s.handle("POST /v1/daemon/shutdown", accessHuman, s.shutdown)

	// The vault opens itself and never locks, so these two are all that is left
	// of its surface: take a copy, and change the key it is under. Both are
	// human-only — an agent that could export the vault would be carrying off
	// every connection keeper exists to stand in front of.
	s.handle("POST /v1/vault/export", accessHuman, s.exportVault)
	s.handle("POST /v1/vault/rotate-master", accessHuman, s.rotateMaster)

	s.raw("GET /v1/events", accessHuman, s.events)

	// --- resolving a local request, the browser's side -------------------
	// Loopback only. The kind is fixed when the request is minted, and the page
	// reads it from GET /v1/requests/{id} above.
	s.handle("POST /v1/requests/{id}/submit", accessLoopback, s.submitRequest)
	s.handle("POST /v1/requests/{id}/decide", accessLoopback, s.decideRequest)
	s.handle("POST /v1/requests/{id}/cancel", accessLoopback, s.cancelRequest)

	// --- the embedded UI, loopback ---------------------------------------
	s.raw("GET /r/{request_id}", accessPublic, s.serveApp)
	s.raw("GET /", accessPublic, s.serveApp)
}
