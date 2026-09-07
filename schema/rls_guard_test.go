package schema

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	pcfg "altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
)

func TestCheckRLSGuard_NotBypass_OK(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnRows(sqlmock.NewRows([]string{"rolbypassrls"}).AddRow(false))
	if err := CheckRLSGuard(context.Background(), conn, false); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCheckRLSGuard_Bypass_NotAllowed(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnRows(sqlmock.NewRows([]string{"rolbypassrls"}).AddRow(true))
	err = CheckRLSGuard(context.Background(), conn, false)
	if !errors.Is(err, ErrRLSBypass) {
		t.Errorf("err=%v want ErrRLSBypass", err)
	}
}

func TestCheckRLSGuard_Bypass_AllowedInDev(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnRows(sqlmock.NewRows([]string{"rolbypassrls"}).AddRow(true))
	if err := CheckRLSGuard(context.Background(), conn, true); err != nil {
		t.Errorf("expected nil with allowBypass=true, got %v", err)
	}
}

func TestCheckRLSGuard_QueryError_Wraps(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	sentinel := fmt.Errorf("connection lost")
	mock.ExpectQuery(`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		WillReturnError(sentinel)
	err = CheckRLSGuard(context.Background(), conn, false)
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("err=%v want wraps connection lost", err)
	}
}

func TestRLSGuard_NonPostgres_NoOp(t *testing.T) {
	cfg := &pcfg.Config{DB: db.DBConfig{Driver: db.DriverSQLite}}
	if err := RLSGuard(context.Background(), nil, cfg); err != nil {
		t.Errorf("expected nil for sqlite, got %v", err)
	}
}

func TestRLSGuard_NilConfig(t *testing.T) {
	if err := RLSGuard(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestRLSGuard_AllowBypass_SkipsChecks(t *testing.T) {
	conn, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	cfg := &pcfg.Config{DB: db.DBConfig{Driver: db.DriverPostgres, AllowBypassRLS: true}}
	if err := RLSGuard(context.Background(), conn, cfg); err != nil {
		t.Errorf("expected nil with AllowBypassRLS=true, got %v", err)
	}
}

func TestRLSAuditError_Error_IncludesCounts(t *testing.T) {
	e := &RLSAuditError{
		MissingRLS:    []string{"a"},
		MissingForce:  []string{"a", "b"},
		MissingPolicy: []string{"a", "b", "c"},
	}
	msg := e.Error()
	if !strings.Contains(msg, "1 missing RLS") ||
		!strings.Contains(msg, "2 missing FORCE") ||
		!strings.Contains(msg, "3 missing tenant policy") {
		t.Errorf("Error() = %q; want counts 1/2/3", msg)
	}
}

func TestIsRLSAuditError_UnwrapsThroughFmt(t *testing.T) {
	inner := &RLSAuditError{MissingRLS: []string{"altempl_todos"}}
	wrapped := fmt.Errorf("boot: %w", inner)
	if !IsRLSAuditError(wrapped) {
		t.Errorf("IsRLSAuditError(wrapped) = false; want true")
	}
	if IsRLSAuditError(errors.New("other")) {
		t.Errorf("IsRLSAuditError(other) = true; want false")
	}
}

func TestAuditPolicies_Empty_NoOp(t *testing.T) {
	if err := AuditPolicies(context.Background(), nil, nil); err != nil {
		t.Errorf("expected nil for empty tables, got %v", err)
	}
}
