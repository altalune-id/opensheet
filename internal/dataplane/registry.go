package dataplane

import (
	"maps"
	"slices"

	"altalune.id/opensheet/internal/platform/surfaces"
)

// VerbTable names the domain verb every route this surface registers exposes, keyed by method and pattern.
func VerbTable() map[string]surfaces.Verb {
	const sheetPath = "/orgs/{org}/projects/{project}/sheets/{slug}"
	const tabsPath = "/orgs/{org}/projects/{project}/spreadsheets/{id}/tabs"
	return map[string]surfaces.Verb{
		"GET " + sheetPath:                   {Module: "sheet", Aggregate: "row", Operation: "list"},
		"POST " + sheetPath:                  {Module: "sheet", Aggregate: "row", Operation: "append"},
		"GET " + sheetPath + "/rows/{id}":    {Module: "sheet", Aggregate: "row", Operation: "get"},
		"POST " + sheetPath + "/rows":        {Module: "sheet", Aggregate: "row", Operation: "create"},
		"POST " + sheetPath + "/rows/batch":  {Module: "sheet", Aggregate: "row", Operation: "create_batch"},
		"PUT " + sheetPath + "/rows/{id}":    {Module: "sheet", Aggregate: "row", Operation: "replace"},
		"PATCH " + sheetPath + "/rows/{id}":  {Module: "sheet", Aggregate: "row", Operation: "update"},
		"DELETE " + sheetPath + "/rows/{id}": {Module: "sheet", Aggregate: "row", Operation: "delete"},
		"GET " + sheetPath + "/capabilities": {Module: "sheet", Aggregate: "sheet", Operation: "capabilities"},
		"DELETE " + sheetPath + "/cache":     {Module: "sheet", Aggregate: "sheet", Operation: "purge_cache"},
		"GET " + tabsPath:                    {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "list_tabs"},
		"POST " + tabsPath:                   {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "create_tab"},
	}
}

// Verbs returns the domain verbs S3 exposes, deduplicated.
func Verbs() []surfaces.Verb {
	return slices.Collect(maps.Values(VerbTable()))
}
