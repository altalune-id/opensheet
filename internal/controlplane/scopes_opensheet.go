package controlplane

import (
	credentialv1connect "altalune.id/opensheet/gen/go/credential/v1/credentialv1connect"
	sheetv1connect "altalune.id/opensheet/gen/go/sheet/v1/sheetv1connect"
	spreadsheetv1connect "altalune.id/opensheet/gen/go/spreadsheet/v1/spreadsheetv1connect"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/surfaces"
)

func opensheetProcedureScopes() authn.ScopeTable {
	return authn.ScopeTable{
		credentialv1connect.CredentialServiceListProcedure:                 authn.ScopeCredentialsRead,
		credentialv1connect.CredentialServiceUploadServiceAccountProcedure: authn.ScopeCredentialsWrite,
		credentialv1connect.CredentialServiceStartGoogleConnectProcedure:   authn.ScopeCredentialsWrite,
		credentialv1connect.CredentialServiceDeleteProcedure:               authn.ScopeCredentialsWrite,

		spreadsheetv1connect.SpreadsheetServiceListProcedure:     authn.ScopeSpreadsheetsRead,
		spreadsheetv1connect.SpreadsheetServiceGetProcedure:      authn.ScopeSpreadsheetsRead,
		spreadsheetv1connect.SpreadsheetServiceListTabsProcedure: authn.ScopeSpreadsheetsRead,
		spreadsheetv1connect.SpreadsheetServiceCreateProcedure:   authn.ScopeSpreadsheetsWrite,
		spreadsheetv1connect.SpreadsheetServiceUpdateProcedure:   authn.ScopeSpreadsheetsWrite,
		spreadsheetv1connect.SpreadsheetServiceDeleteProcedure:   authn.ScopeSpreadsheetsWrite,

		sheetv1connect.SheetServiceListProcedure:       authn.ScopeSheetsRead,
		sheetv1connect.SheetServiceGetProcedure:        authn.ScopeSheetsRead,
		sheetv1connect.SheetServiceCreateProcedure:     authn.ScopeSheetsAdmin,
		sheetv1connect.SheetServiceUpdateProcedure:     authn.ScopeSheetsAdmin,
		sheetv1connect.SheetServiceDeleteProcedure:     authn.ScopeSheetsAdmin,
		sheetv1connect.SheetServicePurgeCacheProcedure: authn.ScopeCachePurge,
	}
}

func opensheetVerbs() map[string]surfaces.Verb {
	return map[string]surfaces.Verb{
		credentialv1connect.CredentialServiceListProcedure:                 {Module: "credential", Aggregate: "credential", Operation: "list"},
		credentialv1connect.CredentialServiceUploadServiceAccountProcedure: {Module: "credential", Aggregate: "credential", Operation: "upload_service_account"},
		credentialv1connect.CredentialServiceStartGoogleConnectProcedure:   {Module: "credential", Aggregate: "credential", Operation: "start_google_connect"},
		credentialv1connect.CredentialServiceDeleteProcedure:               {Module: "credential", Aggregate: "credential", Operation: "delete"},

		spreadsheetv1connect.SpreadsheetServiceListProcedure:     {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "list"},
		spreadsheetv1connect.SpreadsheetServiceGetProcedure:      {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "get"},
		spreadsheetv1connect.SpreadsheetServiceListTabsProcedure: {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "list_tabs"},
		spreadsheetv1connect.SpreadsheetServiceCreateProcedure:   {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "create"},
		spreadsheetv1connect.SpreadsheetServiceUpdateProcedure:   {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "update"},
		spreadsheetv1connect.SpreadsheetServiceDeleteProcedure:   {Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "delete"},

		sheetv1connect.SheetServiceListProcedure:       {Module: "sheet", Aggregate: "sheet", Operation: "list"},
		sheetv1connect.SheetServiceGetProcedure:        {Module: "sheet", Aggregate: "sheet", Operation: "get"},
		sheetv1connect.SheetServiceCreateProcedure:     {Module: "sheet", Aggregate: "sheet", Operation: "create"},
		sheetv1connect.SheetServiceUpdateProcedure:     {Module: "sheet", Aggregate: "sheet", Operation: "update"},
		sheetv1connect.SheetServiceDeleteProcedure:     {Module: "sheet", Aggregate: "sheet", Operation: "delete"},
		sheetv1connect.SheetServicePurgeCacheProcedure: {Module: "sheet", Aggregate: "sheet", Operation: "purge_cache"},
	}
}
