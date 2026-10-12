# Add an error code

Use this when a new failure mode needs a code a user can quote from a screenshot. Raising an error
at all: [`errors.md`](errors.md). What each existing code means:
[`error codes`](../errors/README.md).

## Steps

1. **Pick the domain mnemonic.** Three letters. Reuse the domain's existing one (`BLG`, `TAG`,
   `ORG`); a new mnemonic means a new bounded context.
2. **Pick the next free number in that domain's block.** Codes are `<DOM><NNN>` and append-only:
   never renumbered, never reused after retirement, because users quote them. `900`-`999` is
   reserved for unexpected or internal failures.
3. **Add the constant** to its domain group in `internal/apperror/codes.go`:

   ```go
   CodeTagInUse = "TAG004"
   ```

   The name starts with `Code`, and the pinning test scrapes the file textually — one tab-indented
   constant per line, inside the `const` block.

4. **Add the row** to that domain's table in [`error codes`](../errors/README.md), with all
   four columns. The Status column is the `google.golang.org/grpc/codes` value the envelope
   carries, or `—` when nothing constructs the code yet.
5. **Construct it** from a `ToAppError()`. The gRPC code passed to `apperror.New` must be the one
   the row claims.
6. **`make check`.**

## The pinning tests

All in `internal/apperror/codes_test.go`.

| Test                                              | Fails on                                               |
| ------------------------------------------------- | ------------------------------------------------------ |
| `TestCodes_EveryRefIsDocumented`                  | a constant with no row, **and** a row with no constant |
| `TestCodes_RefsAreWellFormed`                     | a ref not matching `^[A-Z]{3}[0-9]{3}$`                |
| `TestCodes_RefsAreUnique`                         | two constants sharing one ref                          |
| `TestCodes_UnexpectedFailuresUseTheReservedBlock` | any ref but `GEN900` in the `900`-`999` block          |

## Gotchas

- **The doc scraper only sees table rows.** It matches a leading `` `ABC123` `` cell in a Markdown
  table, so a code named in a prose paragraph does not count as documented.
- **The constant scraper only sees the canonical shape.** A constant declared outside the
  tab-indented `const` block, or with a non-standard spacing, is invisible to the test — it will
  pass while the code is genuinely undocumented.
- **Deleting a code is never the fix for a mistake.** Stop constructing it and leave both the
  constant and the row in place.
- **A code aimed at S3 or S4 is dead weight.** Those surfaces emit opaque outcome words, not
  codes — see [`errors.md`](errors.md).
