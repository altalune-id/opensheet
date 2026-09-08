package apperror

// Error code registry: <DOM><NNN>, a three-letter domain mnemonic plus a per-domain sequence.
// Codes are quoted by users off an error page, so they are append-only: never renumber, never reuse a retired code.
// NNN 900-999 is reserved per domain for unexpected or internal failures.
const (
	CodeTenantMissing   = "GEN001"
	CodeUnauthenticated = "GEN002"
	CodeForbidden       = "GEN003"
	CodeValidation      = "GEN004"
	CodeNotFound        = "GEN005"
	CodeAlreadyExists   = "GEN006"
	CodeUnexpectedError = "GEN900"

	CodeTodoNotFound       = "TDO001"
	CodeTodoInvalidTitle   = "TDO002"
	CodeTodoAlreadyDeleted = "TDO003"

	CodeUserNotFound      = "USR001"
	CodeUserAlreadyExists = "USR002"
	CodeUserNotInvited    = "USR003"
	CodeUserInvalidEmail  = "USR004"
	CodeUserInvalidName   = "USR005"

	CodeOrgNotFound          = "ORG001"
	CodeOrgAlreadyExists     = "ORG002"
	CodeOrgInvalidSlug       = "ORG003"
	CodeOrgInvalidName       = "ORG004"
	CodeOrgMembershipExists  = "ORG005"
	CodeOrgMembershipMissing = "ORG006"
	CodeOrgCreationDisabled  = "ORG007"
	CodeOrgSystemProtected   = "ORG008"
	CodeOrgSelfRemoval       = "ORG009"
	CodeOrgOwnerRemoval      = "ORG010"

	CodeProjectNotFound        = "PRJ001"
	CodeProjectAlreadyExists   = "PRJ002"
	CodeProjectInvalidSlug     = "PRJ003"
	CodeProjectSystemProtected = "PRJ004"

	CodeInviteNotFound    = "INV001"
	CodeInviteExpired     = "INV002"
	CodeInviteAlreadyUsed = "INV003"
	CodeInviteInvalidRole = "INV004"
	CodeInviteDisabled    = "INV005"

	CodeSignupRequired = "SGN001"

	CodeAuthInvalidCredentials = "AUT001" //nolint:gosec // error code, not a credential
	CodeAuthOIDCUnavailable    = "AUT002"
	CodeAuthOIDCClaimMissing   = "AUT003"

	CodeTokenExpired = "TKN001"

	CodeOnboardingRequired    = "ONB001"
	CodeOnboardingAlreadyDone = "ONB002"

	CodeCredentialNotFound      = "CRD001"
	CodeCredentialAlreadyExists = "CRD002"
	CodeCredentialInvalidName   = "CRD003"
	CodeCredentialInvalidKind   = "CRD004"
	CodeCredentialInUse         = "CRD005"
	CodeCredentialReauthNeeded  = "CRD006"
	CodeEncryptionUnavailable   = "CRD007"
	CodeCredentialStateInvalid  = "CRD008"
	CodeEncryptionOpenFailed    = "CRD009"
	CodeCredentialNotSealed     = "CRD010"

	CodeSpreadsheetNotFound      = "SPR001"
	CodeSpreadsheetAlreadyExists = "SPR002"
	CodeSpreadsheetInvalidFileID = "SPR003"
	CodeSpreadsheetInvalidTitle  = "SPR004"

	CodeSheetNotFound          = "SHT001"
	CodeSheetAlreadyExists     = "SHT002"
	CodeSheetInvalidSlug       = "SHT003"
	CodeSheetInvalidVisibility = "SHT004"
	CodeSheetInvalidTTL        = "SHT005"
	CodeSheetPublicDisabled    = "SHT006"
	CodeSheetPayloadTooLarge   = "SHT007"
	CodeSheetTabNotFound       = "SHT008"

	CodeAPIKeyNotFound          = "KEY001"
	CodeAPIKeyInvalidName       = "KEY002"
	CodeAPIKeyInvalidScope      = "KEY003"
	CodeAPIKeyUnauthorized      = "KEY004"
	CodeAPIKeyInsufficientScope = "KEY005"

	CodeGoogleNotFound         = "GSH001"
	CodeGooglePermissionDenied = "GSH002"
	CodeGoogleQuotaExceeded    = "GSH003"
	CodeGoogleUnavailable      = "GSH004"
)
