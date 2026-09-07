# Error codes

Every user-visible failure carries a code. It is shown on the error page and on inline form errors,
next to the request id, so a report can be matched to a log line.

Codes are `<DOM><NNN>` — a three-letter domain mnemonic plus a per-domain sequence. They are
**append-only**: a code is never renumbered and a retired code is never reused, because users quote
them from screenshots. `NNN` in the range `900`-`999` is reserved for unexpected or internal failures.

`docs/ERROR_CODES.md` is verified against `internal/apperror/codes.go` by `TestCodes_EveryRefIsDocumented`.

## GEN — General / cross-cutting

| Code     | Constant                       | Meaning          |
| -------- | ------------------------------ | ---------------- |
| `GEN001` | `apperror.CodeTenantMissing`   | Tenant Missing   |
| `GEN002` | `apperror.CodeUnauthenticated` | Unauthenticated  |
| `GEN003` | `apperror.CodeForbidden`       | Forbidden        |
| `GEN004` | `apperror.CodeValidation`      | Validation       |
| `GEN005` | `apperror.CodeNotFound`        | Not Found        |
| `GEN006` | `apperror.CodeAlreadyExists`   | Already Exists   |
| `GEN900` | `apperror.CodeUnexpectedError` | Unexpected Error |

## USR — Users

| Code     | Constant                         | Meaning             |
| -------- | -------------------------------- | ------------------- |
| `USR001` | `apperror.CodeUserNotFound`      | User Not Found      |
| `USR002` | `apperror.CodeUserAlreadyExists` | User Already Exists |
| `USR003` | `apperror.CodeUserNotInvited`    | User Not Invited    |
| `USR004` | `apperror.CodeUserInvalidEmail`  | User Invalid Email  |
| `USR005` | `apperror.CodeUserInvalidName`   | User Invalid Name   |

## ORG — Organizations

| Code     | Constant                            | Meaning                |
| -------- | ----------------------------------- | ---------------------- |
| `ORG001` | `apperror.CodeOrgNotFound`          | Org Not Found          |
| `ORG002` | `apperror.CodeOrgAlreadyExists`     | Org Already Exists     |
| `ORG003` | `apperror.CodeOrgInvalidSlug`       | Org Invalid Slug       |
| `ORG004` | `apperror.CodeOrgInvalidName`       | Org Invalid Name       |
| `ORG005` | `apperror.CodeOrgMembershipExists`  | Org Membership Exists  |
| `ORG006` | `apperror.CodeOrgMembershipMissing` | Org Membership Missing |
| `ORG007` | `apperror.CodeOrgCreationDisabled`  | Org Creation Disabled  |
| `ORG008` | `apperror.CodeOrgSystemProtected`   | Org System Protected   |
| `ORG009` | `apperror.CodeOrgSelfRemoval`       | Org Self Removal       |
| `ORG010` | `apperror.CodeOrgOwnerRemoval`      | Org Owner Removal      |

## PRJ — Projects

| Code     | Constant                              | Meaning                  |
| -------- | ------------------------------------- | ------------------------ |
| `PRJ001` | `apperror.CodeProjectNotFound`        | Project Not Found        |
| `PRJ002` | `apperror.CodeProjectAlreadyExists`   | Project Already Exists   |
| `PRJ003` | `apperror.CodeProjectInvalidSlug`     | Project Invalid Slug     |
| `PRJ004` | `apperror.CodeProjectSystemProtected` | Project System Protected |

## INV — Invites

| Code     | Constant                         | Meaning             |
| -------- | -------------------------------- | ------------------- |
| `INV001` | `apperror.CodeInviteNotFound`    | Invite Not Found    |
| `INV002` | `apperror.CodeInviteExpired`     | Invite Expired      |
| `INV003` | `apperror.CodeInviteAlreadyUsed` | Invite Already Used |
| `INV004` | `apperror.CodeInviteInvalidRole` | Invite Invalid Role |
| `INV005` | `apperror.CodeInviteDisabled`    | Invite Disabled     |

## TDO — Todos

| Code     | Constant                          | Meaning              |
| -------- | --------------------------------- | -------------------- |
| `TDO001` | `apperror.CodeTodoNotFound`       | Todo Not Found       |
| `TDO002` | `apperror.CodeTodoInvalidTitle`   | Todo Invalid Title   |
| `TDO003` | `apperror.CodeTodoAlreadyDeleted` | Todo Already Deleted |

## SGN — Signup

| Code     | Constant                      | Meaning         |
| -------- | ----------------------------- | --------------- |
| `SGN001` | `apperror.CodeSignupRequired` | Signup Required |

## AUT — Authentication

| Code     | Constant                              | Meaning                  |
| -------- | ------------------------------------- | ------------------------ |
| `AUT001` | `apperror.CodeAuthInvalidCredentials` | Auth Invalid Credentials |
| `AUT002` | `apperror.CodeAuthOIDCUnavailable`    | Auth OIDC Unavailable    |
| `AUT003` | `apperror.CodeAuthOIDCClaimMissing`   | Auth OIDC Claim Missing  |

## TKN — Tokens

| Code     | Constant                    | Meaning       |
| -------- | --------------------------- | ------------- |
| `TKN001` | `apperror.CodeTokenExpired` | Token Expired |

## ONB — Onboarding

| Code     | Constant                             | Meaning                 |
| -------- | ------------------------------------ | ----------------------- |
| `ONB001` | `apperror.CodeOnboardingRequired`    | Onboarding Required     |
| `ONB002` | `apperror.CodeOnboardingAlreadyDone` | Onboarding Already Done |
