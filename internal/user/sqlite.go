package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
)

type sqliteStore struct {
	db    *sql.DB
	table *sqliteent.Users
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewUsers(tablePrefix)}
}

type sqliteUserRow struct {
	ID              string         `alias:"users.id"`
	Email           string         `alias:"users.email"`
	Name            string         `alias:"users.name"`
	IsAdmin         int64          `alias:"users.is_admin"`
	IDPIssuer       sql.NullString `alias:"users.idp_issuer"`
	IDPSubject      sql.NullString `alias:"users.idp_subject"`
	PasswordHash    string         `alias:"users.password_hash"`
	Locale          string         `alias:"users.locale"`
	TermsAcceptedAt sql.NullString `alias:"users.terms_accepted_at"`
	CreatedAt       string         `alias:"users.created_at"`
}

func (r *sqliteUserRow) toUser() (*User, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("user.sqlite: parse id: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("user.sqlite: parse created_at: %w", err)
	}
	u := &User{
		ID:           id,
		Email:        r.Email,
		Name:         r.Name,
		IDPIssuer:    r.IDPIssuer.String,
		IDPSubject:   r.IDPSubject.String,
		PasswordHash: r.PasswordHash,
		IsAdmin:      r.IsAdmin == 1,
		Locale:       r.Locale,
		CreatedAt:    created,
	}
	u.Source = sourceFrom(u.IsAdmin, u.IDPIssuer != "", u.PasswordHash != "")
	if r.TermsAcceptedAt.Valid && r.TermsAcceptedAt.String != "" {
		t, err := time.Parse(time.RFC3339Nano, r.TermsAcceptedAt.String)
		if err != nil {
			return nil, fmt.Errorf("user.sqlite: parse terms_accepted_at: %w", err)
		}
		u.TermsAcceptedAt = &t
	}
	return u, nil
}

func (s *sqliteStore) userSelectCols() []sqlite.Projection {
	return []sqlite.Projection{
		s.table.ID, s.table.Email, s.table.Name, s.table.IsAdmin,
		s.table.IDPIssuer, s.table.IDPSubject, s.table.PasswordHash, s.table.Locale,
		s.table.TermsAcceptedAt, s.table.CreatedAt,
	}
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return s.queryOne(ctx, s.table.ID.EQ(sqlite.String(id.String())), &NotFoundError{ID: id.String()})
}

func (s *sqliteStore) ByEmail(ctx context.Context, email string) (*User, error) {
	cond := s.table.Email.EQ(sqlite.String(strings.ToLower(email)))
	return s.queryOne(ctx, cond, &NotFoundError{Email: email})
}

func (s *sqliteStore) ByIDP(ctx context.Context, issuer, subject string) (*User, error) {
	cond := s.table.IDPIssuer.EQ(sqlite.String(issuer)).
		AND(s.table.IDPSubject.EQ(sqlite.String(subject)))
	return s.queryOne(ctx, cond, &NotFoundError{Subject: subject})
}

func (s *sqliteStore) queryOne(ctx context.Context, cond sqlite.BoolExpression, notFound error) (*User, error) {
	cols := s.userSelectCols()
	stmt := sqlite.SELECT(cols[0], cols[1:]...).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row sqliteUserRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("user.sqlite.queryOne: %w", err)
	}
	return row.toUser()
}

func (s *sqliteStore) Save(ctx context.Context, u *User) error {
	isAdmin := int64(0)
	if u.IsAdmin || u.Source == SourceGenesis || u.Source == SourceLocal {
		isAdmin = 1
	}
	now := sqliteent.SQLiteTime(time.Now())
	stmt := s.table.INSERT(
		s.table.ID,
		s.table.Email,
		s.table.Name,
		s.table.IDPIssuer,
		s.table.IDPSubject,
		s.table.AvatarURL,
		s.table.PasswordHash,
		s.table.IsAdmin,
		s.table.Locale,
		s.table.TermsAcceptedAt,
		s.table.CreatedAt,
		s.table.UpdatedAt,
	).
		VALUES(
			u.ID.String(),
			u.Email,
			u.Name,
			sqliteNullableString(u.IDPIssuer),
			sqliteNullableString(u.IDPSubject),
			"",
			u.PasswordHash,
			isAdmin,
			u.Locale,
			sqliteNullableTime(u.TermsAcceptedAt),
			sqliteent.SQLiteTime(u.CreatedAt),
			now,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(sqlite.SET(
			s.table.Email.SET(sqlite.String(u.Email)),
			s.table.Name.SET(sqlite.String(u.Name)),
			s.table.IDPIssuer.SET(sqliteNullableString(u.IDPIssuer)),
			s.table.IDPSubject.SET(sqliteNullableString(u.IDPSubject)),
			s.table.IsAdmin.SET(sqlite.Int64(isAdmin)),
			s.table.PasswordHash.SET(sqlite.String(u.PasswordHash)),
			s.table.Locale.SET(sqlite.String(u.Locale)),
			s.table.TermsAcceptedAt.SET(sqliteNullableTime(u.TermsAcceptedAt)),
			s.table.UpdatedAt.SET(sqlite.String(now)),
		))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		if isSQLiteUnique(err) {
			if strings.Contains(err.Error(), "idp_subject") || strings.Contains(err.Error(), "idp_issuer") {
				return &AlreadyExistsError{Field: "idp_subject", Value: u.IDPSubject}
			}
			return &AlreadyExistsError{Field: "email", Value: u.Email}
		}
		return fmt.Errorf("user.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) HasLocalUsers(ctx context.Context) (bool, error) {
	stmt := sqlite.SELECT(s.table.ID).
		FROM(s.table).
		WHERE(s.table.PasswordHash.NOT_EQ(sqlite.String(""))).
		LIMIT(1)
	var row struct {
		ID string `alias:"users.id"`
	}
	err := stmt.QueryContext(ctx, s.db, &row)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("user.sqlite.HasLocalUsers: %w", err)
}

func (s *sqliteStore) UpdateLocale(ctx context.Context, id uuid.UUID, locale string) error {
	stmt := s.table.UPDATE(s.table.Locale, s.table.UpdatedAt).
		SET(sqlite.String(locale), sqlite.String(sqliteent.SQLiteTime(time.Now()))).
		WHERE(s.table.ID.EQ(sqlite.String(id.String())))
	res, err := stmt.ExecContext(ctx, s.db)
	if err != nil {
		return fmt.Errorf("user.sqlite.UpdateLocale: %w", err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

// NOTE: users_idp_idx is partial on idp_issuer IS NOT NULL, so "" here would collide every password user.
func sqliteNullableString(v string) sqlite.StringExpression {
	if v == "" {
		return sqliteent.NullText()
	}
	return sqlite.String(v)
}

func sqliteNullableTime(t *time.Time) sqlite.StringExpression {
	if t == nil {
		return sqliteent.NullText()
	}
	return sqlite.String(sqliteent.SQLiteTime(*t))
}

func isSQLiteUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: UNIQUE")
}
