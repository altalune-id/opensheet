package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	authv1 "altalune.id/template/gen/go/auth/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/config"
)

// Source names where a Principal's token came from.
type Source string

const (
	SourceFlag        Source = "flag"
	SourceEnv         Source = "env"
	SourceFile        Source = "file"
	SourceSession     Source = "session"
	SourceInteractive Source = "interactive"
	SourceAPI         Source = "api"
)

// Principal is the effective identity for a CLI invocation.
type Principal struct {
	UserID      uuid.UUID
	Email       string
	Name        string
	OrgID       uuid.UUID
	OrgSlug     string
	ProjectID   uuid.UUID
	ProjectSlug string
	Source      Source
	ExpiresAt   time.Time
	VerifiedBy  string
}

// ErrNotSignedIn signals no session file and no --token.
var ErrNotSignedIn = errors.New("not signed in — run `altempl auth login` or set --token")

// Resolve returns the effective principal for cmd's invocation.
func Resolve(ctx context.Context, cmd *cobra.Command, cfg *config.Config, bootClient ClientBootFn) (Principal, error) {
	if p, ok := principalFromCtx(ctx); ok {
		return p, nil
	}

	tok, src, err := readToken(cmd)
	if err != nil {
		return Principal{}, err
	}
	if tok != "" {
		p := Principal{Source: src, VerifiedBy: "trusted"}
		if bootClient != nil && cfg != nil && cfg.HTTP.BaseURL != "" {
			verified, vErr := verifyTokenViaWhoami(ctx, cfg, tok, bootClient)
			if vErr != nil {
				return Principal{}, vErr
			}
			if verified != nil {
				verified.Source = src
				p = *verified
			}
		}
		return p, nil
	}

	if cfg == nil || cfg.Session.Path == "" {
		return Principal{}, ErrNotSignedIn
	}
	sess, err := loadSessionFile(cfg.Session.Path)
	if err != nil {
		return Principal{}, fmt.Errorf("resolve: read session: %w", err)
	}
	if sess == nil {
		return Principal{}, ErrNotSignedIn
	}
	sp := sess.Principal
	return Principal{
		UserID:     sp.UserID,
		Email:      sp.Email,
		Name:       sp.Name,
		OrgID:      sp.ActiveOrgID,
		ProjectID:  sp.ActiveProjectID,
		Source:     SourceSession,
		VerifiedBy: string(SourceSession),
	}, nil
}

func readToken(cmd *cobra.Command) (string, Source, error) {
	if f := cmd.Root().PersistentFlags().Lookup("token"); f != nil && f.Changed {
		return strings.TrimSpace(f.Value.String()), SourceFlag, nil
	}
	if v := strings.TrimSpace(os.Getenv("ALT_TOKEN")); v != "" {
		return v, SourceEnv, nil
	}
	if f := cmd.Root().PersistentFlags().Lookup("token-file"); f != nil && f.Changed {
		return readTokenFile(f.Value.String(), SourceFlag)
	}
	if v := strings.TrimSpace(os.Getenv("ALT_TOKEN_FILE")); v != "" {
		return readTokenFile(v, SourceEnv)
	}
	return "", "", nil
}

func verifyTokenViaWhoami(ctx context.Context, cfg *config.Config, token string, bootClient ClientBootFn) (*Principal, error) {
	client, err := bootClient(ctx, cfg, token)
	if err != nil {
		return nil, fmt.Errorf("resolve: boot client: %w", err)
	}
	if client == nil || client.Conn == nil {
		return nil, nil
	}
	resp, err := client.Conn.Auth.Whoami(ctx, connect.NewRequest(&authv1.WhoamiRequest{}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnauthenticated {
			return nil, apperror.New(
				apperror.CodeUnauthenticated,
				"resolve: token rejected by server",
				codes.Unauthenticated,
				&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
			).WithCause(err)
		}
		return nil, fmt.Errorf("resolve: whoami: %w", err)
	}
	uid, _ := uuid.Parse(resp.Msg.GetUserId())
	oid, _ := uuid.Parse(resp.Msg.GetActiveOrgId())
	return &Principal{
		UserID:     uid,
		Email:      resp.Msg.GetEmail(),
		Name:       resp.Msg.GetName(),
		OrgID:      oid,
		OrgSlug:    resp.Msg.GetActiveOrgSlug(),
		VerifiedBy: string(SourceAPI),
	}, nil
}

func readTokenFile(path string, src Source) (string, Source, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path from operator config.
	if err != nil {
		return "", "", fmt.Errorf("resolve: read token file %q: %w", path, err)
	}
	tok := string(bytes.TrimSpace(b))
	if tok == "" {
		return "", "", fmt.Errorf("resolve: token file %q is empty", path)
	}
	return tok, src, nil
}
