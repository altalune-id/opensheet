package controlplane

import (
	"maps"

	apikeyv1connect "altalune.id/opensheet/gen/go/apikey/v1/apikeyv1connect"
	authv1connect "altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	orgv1connect "altalune.id/opensheet/gen/go/org/v1/orgv1connect"
	projectv1connect "altalune.id/opensheet/gen/go/project/v1/projectv1connect"
	"altalune.id/opensheet/internal/platform/authn"
)

// ScopeTable declares the scope a key principal must hold for every mounted procedure. SECURITY: a procedure missing from this map is denied to key principals, not admitted.
func ScopeTable() authn.ScopeTable {
	t := authn.ScopeTable{
		apikeyv1connect.APIKeyServiceListProcedure:           authn.ScopeAPIKeysRead,
		apikeyv1connect.APIKeyServiceCreateProcedure:         authn.ScopeAPIKeysWrite,
		apikeyv1connect.APIKeyServiceRevokeProcedure:         authn.ScopeAPIKeysWrite,
		projectv1connect.ProjectServiceListProjectsProcedure: authn.ScopeSheetsRead,
		orgv1connect.MemberServiceListMembersProcedure:       authn.ScopeMembersRead,
		authv1connect.AuthServiceWhoamiProcedure:             authn.ScopeAPIKeysRead,
	}
	maps.Copy(t, opensheetProcedureScopes())
	return t
}
