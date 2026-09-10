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

## CRD — Credentials

| Code     | Constant                                     | Meaning                          |
| -------- | -------------------------------------------- | -------------------------------- |
| `CRD001` | `apperror.CodeCredentialNotFound`            | Credential Not Found             |
| `CRD002` | `apperror.CodeCredentialAlreadyExists`       | Credential Already Exists        |
| `CRD003` | `apperror.CodeCredentialInvalidName`         | Credential Invalid Name          |
| `CRD004` | `apperror.CodeCredentialInvalidKind`         | Credential Invalid Kind          |
| `CRD005` | `apperror.CodeCredentialInUse`               | Credential In Use                |
| `CRD006` | `apperror.CodeCredentialReauthNeeded`        | Credential Needs Reauthorization |
| `CRD007` | `apperror.CodeEncryptionUnavailable`         | Encryption Unavailable           |
| `CRD008` | `apperror.CodeCredentialStateInvalid`        | Connect State Invalid Or Expired |
| `CRD009` | `apperror.CodeEncryptionOpenFailed`          | Encryption Open Failed           |
| `CRD010` | `apperror.CodeCredentialNotSealed`           | Credential Not Sealed            |
| `CRD011` | `apperror.CodeCredentialGoogleNotConfigured` | Google Connect Not Configured    |

## SPR — Spreadsheets

| Code     | Constant                                | Meaning                        |
| -------- | --------------------------------------- | ------------------------------ |
| `SPR001` | `apperror.CodeSpreadsheetNotFound`      | Spreadsheet Not Found          |
| `SPR002` | `apperror.CodeSpreadsheetAlreadyExists` | Spreadsheet Already Registered |
| `SPR003` | `apperror.CodeSpreadsheetInvalidFileID` | Spreadsheet Invalid File ID    |
| `SPR004` | `apperror.CodeSpreadsheetInvalidTitle`  | Spreadsheet Invalid Title      |
| `SPR005` | `apperror.CodeSpreadsheetNotWritable`   | Spreadsheet Not Writable       |

## SHT — Sheets

| Code     | Constant                                  | Meaning                       |
| -------- | ----------------------------------------- | ----------------------------- |
| `SHT001` | `apperror.CodeSheetNotFound`              | Sheet Not Found               |
| `SHT002` | `apperror.CodeSheetAlreadyExists`         | Sheet Slug Taken              |
| `SHT003` | `apperror.CodeSheetInvalidSlug`           | Sheet Invalid Slug            |
| `SHT004` | `apperror.CodeSheetInvalidVisibility`     | Sheet Invalid Visibility      |
| `SHT005` | `apperror.CodeSheetInvalidTTL`            | Sheet Invalid Cache TTL       |
| `SHT006` | `apperror.CodeSheetPublicDisabled`        | Public Sheets Disabled        |
| `SHT007` | `apperror.CodeSheetPayloadTooLarge`       | Sheet Payload Too Large       |
| `SHT008` | `apperror.CodeSheetTabNotFound`           | Sheet Tab Not Found           |
| `SHT009` | `apperror.CodeSheetNotWritable`           | Sheet Not Writable            |
| `SHT010` | `apperror.CodeSheetNoIDColumn`            | Sheet Has No ID Column        |
| `SHT011` | `apperror.CodeSheetAmbiguousIDColumn`     | Sheet ID Column Ambiguous     |
| `SHT012` | `apperror.CodeSheetDuplicateID`           | Sheet Duplicate ID            |
| `SHT013` | `apperror.CodeSheetRowNotFound`           | Sheet Row Not Found           |
| `SHT014` | `apperror.CodeSheetUnknownColumn`         | Sheet Unknown Column          |
| `SHT015` | `apperror.CodeSheetReadOnlyColumn`        | Sheet Read-Only Column        |
| `SHT016` | `apperror.CodeSheetInvalidRow`            | Sheet Invalid Row             |
| `SHT017` | `apperror.CodeSheetWriteInFlight`         | Sheet Write In Flight         |
| `SHT018` | `apperror.CodeSheetIdempotencyMismatch`   | Sheet Idempotency Mismatch    |
| `SHT019` | `apperror.CodeSheetInvalidTabTitle`       | Sheet Invalid Tab Title       |
| `SHT020` | `apperror.CodeSheetInvalidRowIndex`       | Sheet Invalid Row Index       |
| `SHT021` | `apperror.CodeSheetDuplicateColumn`       | Sheet Duplicate Column        |
| `SHT022` | `apperror.CodeSheetEmptyID`               | Sheet Empty Row ID            |
| `SHT023` | `apperror.CodeSheetContractViolation`     | Sheet Contract Violation      |
| `SHT024` | `apperror.CodeSheetInvalidRange`          | Sheet Invalid Cell Range      |
| `SHT025` | `apperror.CodeSheetNothingToFix`          | Sheet Nothing To Fix          |
| `SHT026` | `apperror.CodeSheetColumnNotEmpty`        | Sheet Column Not Empty        |
| `SHT027` | `apperror.CodeSheetIDMismatch`            | Sheet Row ID Mismatch         |
| `SHT028` | `apperror.CodeSheetBatchTooLarge`         | Sheet Batch Too Large         |
| `SHT029` | `apperror.CodeSheetSoftDeleteUnsupported` | Sheet Soft Delete Unsupported |

## KEY — API keys

| Code     | Constant                               | Meaning                    |
| -------- | -------------------------------------- | -------------------------- |
| `KEY001` | `apperror.CodeAPIKeyNotFound`          | API Key Not Found          |
| `KEY002` | `apperror.CodeAPIKeyInvalidName`       | API Key Invalid Name       |
| `KEY003` | `apperror.CodeAPIKeyInvalidScope`      | API Key Invalid Scope      |
| `KEY004` | `apperror.CodeAPIKeyUnauthorized`      | API Key Unauthorized       |
| `KEY005` | `apperror.CodeAPIKeyInsufficientScope` | API Key Insufficient Scope |

`KEY004` is the single opaque code for missing, malformed, unknown, revoked and expired keys:
distinguishing them on the wire tells an attacker which guesses were closer.

## GSH — Google Sheets

| Code     | Constant                              | Meaning                        |
| -------- | ------------------------------------- | ------------------------------ |
| `GSH001` | `apperror.CodeGoogleNotFound`         | Google Document Not Found      |
| `GSH002` | `apperror.CodeGooglePermissionDenied` | Google Permission Denied       |
| `GSH003` | `apperror.CodeGoogleQuotaExceeded`    | Google Quota Exceeded          |
| `GSH004` | `apperror.CodeGoogleUnavailable`      | Google Temporarily Unavailable |
