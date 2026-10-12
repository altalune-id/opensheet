# opensheet error codes

Codes owned by opensheet's domain modules. The template codes live in [`README.md`](README.md); the
rules (append-only, `900`-`999` reserved) are the same, and `TestCodes_EveryRefIsDocumented` reads
every `docs/errors/*.md` file.

`CRD007` and `CRD009` are retired: encryption failures carry the template's `ENC001` and `ENC002`.
The `KEY` group of opensheet's own API keys is retired with them; key failures carry the template's
`APK` codes.

The data plane (`/api/v1/`) answers with these codes in a JSON envelope,
`{"error":{"code":"SHT013","message":"row not found"}}`, unlike the template's data plane, which
answers an opaque outcome word only.

## CRD — Credentials

| Code     | Constant                                     | Meaning                          |
| -------- | -------------------------------------------- | -------------------------------- |
| `CRD001` | `apperror.CodeCredentialNotFound`            | Credential Not Found             |
| `CRD002` | `apperror.CodeCredentialAlreadyExists`       | Credential Already Exists        |
| `CRD003` | `apperror.CodeCredentialInvalidName`         | Credential Invalid Name          |
| `CRD004` | `apperror.CodeCredentialInvalidKind`         | Credential Invalid Kind          |
| `CRD005` | `apperror.CodeCredentialInUse`               | Credential In Use                |
| `CRD006` | `apperror.CodeCredentialReauthNeeded`        | Credential Needs Reauthorization |
| `CRD008` | `apperror.CodeCredentialStateInvalid`        | Connect State Invalid Or Expired |
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
| `SHT030` | `apperror.CodeSheetPreconditionFailed`    | Sheet Precondition Failed     |
| `SHT031` | `apperror.CodeSheetInvalidClause`         | Sheet Invalid Filter Clause   |
| `SHT032` | `apperror.CodeSheetUnqueryableColumn`     | Sheet Unqueryable Column      |
| `SHT033` | `apperror.CodeSheetInvalidLimit`          | Sheet Invalid Row Limit       |
| `SHT034` | `apperror.CodeSheetInvalidCursor`         | Sheet Invalid Page Cursor     |
| `SHT035` | `apperror.CodeSheetUnknownQueryParam`     | Sheet Unknown Query Parameter |
| `SHT036` | `apperror.CodeSheetStaleCursor`           | Sheet Stale Page Cursor       |
| `SHT037` | `apperror.CodeSheetHintNotApplicable`     | Sheet Hint Not Applicable     |
| `SHT038` | `apperror.CodeSheetHintOperand`           | Sheet Invalid Hint Operand    |
| `SHT039` | `apperror.CodeSheetInvalidSort`           | Sheet Invalid Sort            |
| `SHT040` | `apperror.CodeSheetSortDepth`             | Sheet Sorted Walk Too Deep    |
| `SHT041` | `apperror.CodeSheetUnknownSortColumn`     | Sheet Unknown Sort Column     |

## GSH — Google Sheets

| Code     | Constant                              | Meaning                        |
| -------- | ------------------------------------- | ------------------------------ |
| `GSH001` | `apperror.CodeGoogleNotFound`         | Google Document Not Found      |
| `GSH002` | `apperror.CodeGooglePermissionDenied` | Google Permission Denied       |
| `GSH003` | `apperror.CodeGoogleQuotaExceeded`    | Google Quota Exceeded          |
| `GSH004` | `apperror.CodeGoogleUnavailable`      | Google Temporarily Unavailable |
