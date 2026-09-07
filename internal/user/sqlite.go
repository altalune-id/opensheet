package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type sqliteStore struct {
	db          *sql.DB
	tablePrefix string
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, tablePrefix: tablePrefix}
}

func (s *sqliteStore) table() string { return s.tablePrefix + "users" }

const sqliteUserSelectCols = "id, email, name, is_admin, idp_issuer, password_hash, locale, terms_accepted_at, created_at"

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*User, error) {
	//nolint:gosec // G201: table identifier is fixed by config, never user input.
	q := fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", sqliteUserSelectCols, s.table())
	return s.queryOne(ctx, q, &NotFoundError{ID: id.String()}, id.String())
}

func (s *sqliteStore) ByEmail(ctx context.Context, email string) (*User, error) {
	//nolint:gosec // G201: table identifier is fixed by config, never user input.
	q := fmt.Sprintf("SELECT %s FROM %s WHERE email = ?", sqliteUserSelectCols, s.table())
	return s.queryOne(ctx, q, &NotFoundError{Email: email}, strings.ToLower(email))
}

func (s *sqliteStore) queryOne(ctx context.Context, q string, notFound error, arg any) (*User, error) {
	var (
		idStr        string
		email        string
		name         string
		isAdmin      int64
		idpIssuer    sql.NullString
		passwordHash string
		locale       string
		terms        sql.NullString
		createdAt    string
	)
	err := s.db.QueryRowContext(ctx, q, arg).Scan(
		&idStr, &email, &name, &isAdmin, &idpIssuer, &passwordHash, &locale, &terms, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("user.queryOne: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("user.queryOne: parse id: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, fmt.Errorf("user.queryOne: parse created_at: %w", err)
	}
	u := &User{
		ID:           id,
		Email:        email,
		Name:         name,
		Source:       sourceFrom(isAdmin == 1, idpIssuer.Valid, passwordHash != ""),
		PasswordHash: passwordHash,
		IsAdmin:      isAdmin == 1,
		Locale:       locale,
		CreatedAt:    created,
	}
	if terms.Valid && terms.String != "" {
		t, err := time.Parse(time.RFC3339Nano, terms.String)
		if err != nil {
			return nil, fmt.Errorf("user.queryOne: parse terms_accepted_at: %w", err)
		}
		u.TermsAcceptedAt = &t
	}
	return u, nil
}

func (s *sqliteStore) Save(ctx context.Context, u *User) error {
	isAdmin := int64(0)
	if u.IsAdmin || u.Source == SourceGenesis || u.Source == SourceLocal {
		isAdmin = 1
	}
	//nolint:gosec // G201: table identifier is fixed by config, never user input.
	q := fmt.Sprintf(`
INSERT INTO %s (id, email, name, avatar_url, is_admin, password_hash, locale, terms_accepted_at, created_at, updated_at)
VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	email = excluded.email,
	name = excluded.name,
	is_admin = excluded.is_admin,
	password_hash = excluded.password_hash,
	locale = excluded.locale,
	terms_accepted_at = excluded.terms_accepted_at,
	updated_at = excluded.updated_at
`, s.table())
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var termsArg any
	if u.TermsAcceptedAt != nil {
		termsArg = u.TermsAcceptedAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := s.db.ExecContext(ctx, q,
		u.ID.String(), u.Email, u.Name, isAdmin, u.PasswordHash, u.Locale, termsArg, u.CreatedAt.UTC().Format(time.RFC3339Nano), now,
	); err != nil {
		if isSQLiteUnique(err) {
			return &AlreadyExistsError{Field: "email", Value: u.Email}
		}
		return fmt.Errorf("user.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) HasLocalUsers(ctx context.Context) (bool, error) {
	//nolint:gosec // G201: table identifier is fixed by config, never user input.
	q := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE password_hash <> '')", s.table())
	var has int64
	if err := s.db.QueryRowContext(ctx, q).Scan(&has); err != nil {
		return false, fmt.Errorf("user.HasLocalUsers: %w", err)
	}
	return has == 1, nil
}

func (s *sqliteStore) UpdateLocale(ctx context.Context, id uuid.UUID, locale string) error {
	//nolint:gosec // G201: table identifier is fixed by config, never user input.
	q := fmt.Sprintf("UPDATE %s SET locale = ?, updated_at = ? WHERE id = ?", s.table())
	res, err := s.db.ExecContext(ctx, q, locale, time.Now().UTC().Format(time.RFC3339Nano), id.String())
	if err != nil {
		return fmt.Errorf("user.UpdateLocale: %w", err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func isSQLiteUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: UNIQUE")
}
