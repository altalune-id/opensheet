package boot

import (
	"altalune.id/opensheet/internal/dataplane"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/user"
)

// SECURITY: no tenant resolution; the authorizer binds the credential to the path's org. Without a resource to name in the 401 challenge, tokens get the masked not-found.
func dataAuthorizer(cfg *config.Config, s *Services, verifier tokens.Verifier) (authz dataplane.Authorizer, challenge string) {
	scheme := credentialScheme(s.KeyAuthn.Scheme(), cfg.Tokens.Introspection)
	resource, err := tokens.NewResource(cfg.Tokens.Audience)
	if cfg.Tokens.Issuer == "" || err != nil {
		return dataplane.NewCredentialAuthorizer(s.KeyAuthn, nil, s.Authz, scheme), ""
	}
	people := user.NewAuthenticator(tokens.NewAuthenticator(verifier, scheme), s.UserStore)
	return dataplane.NewCredentialAuthorizer(s.KeyAuthn, people, s.Authz, scheme), resource.InvalidToken()
}
