package web

// DataPath returns the path the data plane serves a published sheet's rows from, under <basePath>/api/v1/.
func DataPath(basePath, orgSlug, projectSlug, sheetSlug string) string {
	return Path(basePath, "/api/v1/orgs/"+orgSlug+"/projects/"+projectSlug+"/sheets/"+sheetSlug)
}
