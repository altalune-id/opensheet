package controlplane

import (
	"maps"
	"slices"

	apikeyv1connect "altalune.id/opensheet/gen/go/apikey/v1/apikeyv1connect"
	authv1connect "altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	orgv1connect "altalune.id/opensheet/gen/go/org/v1/orgv1connect"
	projectv1connect "altalune.id/opensheet/gen/go/project/v1/projectv1connect"
	"altalune.id/opensheet/internal/platform/surfaces"
)

// VerbTable names the domain verb every mounted procedure exposes, keyed by procedure path.
func VerbTable() map[string]surfaces.Verb {
	t := map[string]surfaces.Verb{
		apikeyv1connect.APIKeyServiceListProcedure:           {Module: "apikey", Aggregate: "apikey", Operation: "list"},
		apikeyv1connect.APIKeyServiceCreateProcedure:         {Module: "apikey", Aggregate: "apikey", Operation: "create"},
		apikeyv1connect.APIKeyServiceRevokeProcedure:         {Module: "apikey", Aggregate: "apikey", Operation: "revoke"},
		authv1connect.AuthServiceWhoamiProcedure:             {Module: "auth", Aggregate: "session", Operation: "whoami"},
		projectv1connect.ProjectServiceListProjectsProcedure: {Module: "project", Aggregate: "project", Operation: "list"},
		orgv1connect.MemberServiceListMembersProcedure:       {Module: "org", Aggregate: "member", Operation: "list"},
	}
	maps.Copy(t, opensheetVerbs())
	return t
}

// Verbs returns the domain verbs S2 exposes, deduplicated.
func Verbs() []surfaces.Verb {
	return slices.Collect(maps.Values(VerbTable()))
}
