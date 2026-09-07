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
)
