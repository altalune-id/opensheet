package authz

// The permissions every app carries.
const (
	ProjectsWrite Permission = "projects:write"
	MembersWrite  Permission = "members:write"
	APIKeysWrite  Permission = "apikeys:write"
	RolesWrite    Permission = "roles:write"
)

func coreDefs() []Def {
	return []Def{
		{Permission: ProjectsWrite, LabelKey: "authz.perm.projects_write", Holder: HolderConsole},
		{Permission: MembersWrite, LabelKey: "authz.perm.members_write", Holder: HolderConsole},
		{Permission: APIKeysWrite, LabelKey: "authz.perm.apikeys_write", Holder: HolderConsole},
		{Permission: RolesWrite, LabelKey: "authz.perm.roles_write", Holder: HolderConsole},
	}
}
