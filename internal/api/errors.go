package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/types"
)

// status maps a keeper code to an HTTP status. CONTRACT §3: 400 for syntax, 403
// for permission, 404 for unknown, 409 for approval required, 500 for internal.
//
// The codes are deliberately coarser than SQLSTATE so a mapped code discloses
// less (R6.4a), and the status is coarser again.
func status(c types.Code) int {
	switch c {
	case types.CodeSyntax, types.CodeMultiStatement:
		return http.StatusBadRequest
	case types.CodePermissionDenied, types.CodeUnclassified, types.CodeDenylisted,
		types.CodeDDLRefused, types.CodeOutOfWriteScope, types.CodeNoWriteCredential,
		types.CodeApprovalRefused, types.CodeRowCap:
		return http.StatusForbidden
	case types.CodeTicketUnknown:
		return http.StatusNotFound
	case types.CodeApprovalRequired:
		return http.StatusConflict
	case types.CodeStaleToken:
		return http.StatusConflict
	case types.CodeTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// asError converts anything that reached this boundary into the only error shape
// an agent may see. This is the one place the conversion happens (CONTRACT §4):
// an error keeper did not compose itself becomes a generic internal error rather
// than a string, because a PostgreSQL message interpolates the offending value
// and R6.4a's response is an allowlist, not a denylist.
func asError(err error) (*types.Error, int) {
	if err == nil {
		return nil, http.StatusOK
	}
	if e, ok := errors.AsType[*types.Error](err); ok {
		return e, status(e.Code)
	}
	if e, ok := errors.AsType[*types.ValidationError](err); ok {
		return &types.Error{
			Code:    types.CodeSyntax,
			Summary: e.Field + ": " + e.Reason,
			Action:  "correct the request and retry",
		}, http.StatusBadRequest
	}
	if e, ok := errors.AsType[*daemon.VersionSkew](err); ok {
		// §3.4: the client errors naming both versions and `keeper daemon
		// restart`. It never restarts automatically, because one window doing so
		// would cancel every other window's pending tickets.
		return &types.Error{
			Code:    types.CodeInternal,
			Summary: "keeper daemon " + e.Daemon + " does not match this client " + e.Client,
			Action:  "run `keeper daemon restart`",
		}, http.StatusConflict
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &types.Error{Code: types.CodeTimeout, Summary: "keeper did not finish within the time allowed", Action: "retry"}, http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return &types.Error{Code: types.CodeTimeout, Summary: "the request was cancelled", Action: "retry"}, http.StatusGatewayTimeout
	}
	return &types.Error{
		Code:    types.CodeInternal,
		Summary: "keeper could not complete the request",
		Action:  "run `keeper doctor`",
	}, http.StatusInternalServerError
}

func denied(summary, action string) *types.Error {
	return &types.Error{Code: types.CodePermissionDenied, Summary: summary, Action: action}
}

var (
	errAgentOnly = denied(
		"this route is served on the keeper socket only",
		"call it over the keeper socket")

	errHumanOnly = denied(
		"registering connections, approving, granting, editing the catalog and setting limits are CLI and UI actions only",
		"use `keeper` or the local page")

	errSessionNeeded = denied(
		"this route needs an agent session established on this connection",
		"call POST /v1/session first")

	errBadOrigin = denied(
		"the request did not come from keeper's own local page",
		"open keeper from the link it printed")

	errBadHost = denied(
		"the request did not address keeper's loopback listener by name",
		"open keeper from the link it printed")

	errNoCSRF = denied(
		"the request did not carry keeper's CSRF token",
		"reload the keeper page")

	errUIOnly = denied(
		"keeper's interface is served on its loopback listener only",
		"open keeper from the link it printed")

	errNoRoute = &types.Error{
		Code:    types.CodeTicketUnknown,
		Summary: "keeper serves no such route",
		Action:  "check the path",
	}

	errInternalStream = &types.Error{
		Code:    types.CodeInternal,
		Summary: "keeper could not open the stream",
		Action:  "run `keeper doctor`",
	}
)
