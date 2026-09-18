// Package pgaudit implements ports.Auditor: SPEC §4.1's G0 privilege audit.
// It connects with pgx, runs a fixed set of read-only introspection queries
// against current_user, disconnects, and reports what it found as
// []types.Finding. It never refuses a credential — report and accept, never
// silent, never a hard stop: SPEC R4.1.
package pgaudit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Auditor is the concrete ports.Auditor. It holds no state: every Audit call
// opens its own connection and closes it before returning.
type Auditor struct{}

var _ ports.Auditor = (*Auditor)(nil)

// New returns an Auditor.
func New() *Auditor { return &Auditor{} }

// Audit runs G0 against dsn and reports every finding for the given role.
// SPEC R4.1. The whole audit runs inside one read-only transaction; nothing
// it does can write.
func (a *Auditor) Audit(ctx context.Context, dsn string, role ports.Role) ([]types.Finding, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgaudit: connect: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("pgaudit: begin: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	currentUser, err := fetchCurrentUser(ctx, tx)
	if err != nil {
		return nil, err
	}

	var findings []types.Finding

	attrs, err := fetchAttributes(ctx, tx)
	if err != nil {
		return nil, err
	}
	findings = append(findings, attributeFindings(attrs)...)

	members, err := fetchMemberships(ctx, tx)
	if err != nil {
		return nil, err
	}
	findings = append(findings, membershipFindings(currentUser, members)...)

	relPrivs, err := fetchRelationPrivileges(ctx, tx)
	if err != nil {
		return nil, err
	}
	findings = append(findings, relationWriteFindings(currentUser, relPrivs, role)...)

	schemaPrivs, err := fetchSchemaPrivileges(ctx, tx)
	if err != nil {
		return nil, err
	}
	findings = append(findings, schemaCreateFindings(currentUser, schemaPrivs)...)

	fnPrivs, err := fetchFunctionPrivileges(ctx, tx)
	if err != nil {
		return nil, err
	}
	findings = append(findings, functionExecFindings(currentUser, fnPrivs)...)

	secDefs, err := fetchSecurityDefiners(ctx, tx)
	if err != nil {
		return nil, err
	}
	findings = append(findings, securityDefinerFindings(secDefs)...)

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgaudit: commit: %w", err)
	}
	return findings, nil
}

func fetchCurrentUser(ctx context.Context, tx pgx.Tx) (string, error) {
	var name string
	if err := tx.QueryRow(ctx, "SELECT current_user").Scan(&name); err != nil {
		return "", fmt.Errorf("pgaudit: current_user: %w", err)
	}
	return name, nil
}

func fetchAttributes(ctx context.Context, tx pgx.Tx) (roleAttributesRow, error) {
	rows, err := tx.Query(ctx, attributesQuery)
	if err != nil {
		return roleAttributesRow{}, fmt.Errorf("pgaudit: attributes: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[roleAttributesRow])
	if err != nil {
		return roleAttributesRow{}, fmt.Errorf("pgaudit: attributes: %w", err)
	}
	return row, nil
}

func fetchMemberships(ctx context.Context, tx pgx.Tx) ([]membershipRow, error) {
	rows, err := tx.Query(ctx, membershipQuery, membershipRoleNames)
	if err != nil {
		return nil, fmt.Errorf("pgaudit: memberships: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[membershipRow])
	if err != nil {
		return nil, fmt.Errorf("pgaudit: memberships: %w", err)
	}
	return out, nil
}

func fetchRelationPrivileges(ctx context.Context, tx pgx.Tx) ([]relationPrivRow, error) {
	rows, err := tx.Query(ctx, relationPrivilegeQuery)
	if err != nil {
		return nil, fmt.Errorf("pgaudit: relation privileges: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[relationPrivRow])
	if err != nil {
		return nil, fmt.Errorf("pgaudit: relation privileges: %w", err)
	}
	return out, nil
}

func fetchSchemaPrivileges(ctx context.Context, tx pgx.Tx) ([]schemaPrivRow, error) {
	rows, err := tx.Query(ctx, schemaPrivilegeQuery)
	if err != nil {
		return nil, fmt.Errorf("pgaudit: schema privileges: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[schemaPrivRow])
	if err != nil {
		return nil, fmt.Errorf("pgaudit: schema privileges: %w", err)
	}
	return out, nil
}

func fetchFunctionPrivileges(ctx context.Context, tx pgx.Tx) ([]functionPrivRow, error) {
	rows, err := tx.Query(ctx, functionPrivilegeQuery, fileAndProgramFunctions)
	if err != nil {
		return nil, fmt.Errorf("pgaudit: function privileges: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[functionPrivRow])
	if err != nil {
		return nil, fmt.Errorf("pgaudit: function privileges: %w", err)
	}
	return out, nil
}

func fetchSecurityDefiners(ctx context.Context, tx pgx.Tx) ([]secDefRow, error) {
	rows, err := tx.Query(ctx, securityDefinerQuery)
	if err != nil {
		return nil, fmt.Errorf("pgaudit: security definer functions: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[secDefRow])
	if err != nil {
		return nil, fmt.Errorf("pgaudit: security definer functions: %w", err)
	}
	return out, nil
}
