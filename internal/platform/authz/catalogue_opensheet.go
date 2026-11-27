package authz

import "altalune.id/opensheet/internal/platform/authn"

// The sheet, spreadsheet, credential, cache, member and key permissions.
const (
	SheetsRead               Permission = "sheets:read"
	SheetsWrite              Permission = "sheets:write"
	SheetsAdmin              Permission = "sheets:admin"
	SheetsEnableWrites       Permission = "sheets:enable_writes"
	SpreadsheetsRead         Permission = "spreadsheets:read"
	SpreadsheetsWrite        Permission = "spreadsheets:write"
	SpreadsheetsEnableWrites Permission = "spreadsheets:enable_writes"
	CredentialsRead          Permission = "credentials:read"
	CredentialsWrite         Permission = "credentials:write"
	CredentialsManage        Permission = "credentials:manage"
	CachePurge               Permission = "cache:purge"
	MembersRead              Permission = "members:read"
	APIKeysRead              Permission = "apikeys:read"
)

func appDefs() []Def {
	return []Def{
		{Permission: SheetsRead, LabelKey: "authz.perm.sheets_read", Scope: authn.ScopeSheetsRead, Holder: HolderAny},
		{Permission: SheetsWrite, LabelKey: "authz.perm.sheets_write", Scope: authn.ScopeSheetsWrite, Holder: HolderAny},
		{Permission: SheetsAdmin, LabelKey: "authz.perm.sheets_admin", Scope: authn.ScopeSheetsAdmin, Holder: HolderAny},
		{Permission: SheetsEnableWrites, LabelKey: "authz.perm.sheets_enable_writes", Scope: authn.ScopeSheetsAdmin, Holder: HolderAny},
		{Permission: SpreadsheetsRead, LabelKey: "authz.perm.spreadsheets_read", Scope: authn.ScopeSpreadsheetsRead, Holder: HolderAny},
		{Permission: SpreadsheetsWrite, LabelKey: "authz.perm.spreadsheets_write", Scope: authn.ScopeSpreadsheetsWrite, Holder: HolderAny},
		{Permission: SpreadsheetsEnableWrites, LabelKey: "authz.perm.spreadsheets_enable_writes", Scope: authn.ScopeSpreadsheetsWrite, Holder: HolderAny},
		{Permission: CredentialsRead, LabelKey: "authz.perm.credentials_read", Scope: authn.ScopeCredentialsRead, Holder: HolderAny},
		{Permission: CredentialsWrite, LabelKey: "authz.perm.credentials_write", Scope: authn.ScopeCredentialsWrite, Holder: HolderAny},
		{Permission: CredentialsManage, LabelKey: "authz.perm.credentials_manage", Scope: authn.ScopeCredentialsWrite, Holder: HolderAny},
		{Permission: CachePurge, LabelKey: "authz.perm.cache_purge", Scope: authn.ScopeCachePurge, Holder: HolderAny},
		{Permission: MembersRead, LabelKey: "authz.perm.members_read", Scope: authn.ScopeMembersRead, Holder: HolderAny},
		{Permission: APIKeysRead, LabelKey: "authz.perm.apikeys_read", Scope: authn.ScopeAPIKeysRead, Holder: HolderAny},
	}
}

func memberPermissions() []Permission {
	return []Permission{
		SheetsRead, SheetsWrite, SheetsAdmin,
		SpreadsheetsRead, SpreadsheetsWrite,
		CredentialsRead, CredentialsWrite,
		CachePurge, MembersRead, APIKeysRead,
	}
}
