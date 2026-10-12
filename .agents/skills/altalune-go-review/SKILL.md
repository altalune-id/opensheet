---
name: altalune-go-review
description: Use when reviewing changes in opensheet (altalune-template) or a downstream fork — a checkpoint during plan implementation, a re-review after fixing findings, or a final pass before committing a spec or plan. Also use when asked whether code follows the codebase convention, is reusable, extensible, scalable, future-proof or maintainable, or duplicates something that should live in one place.
license: Proprietary
metadata:
  pairs-with: altalune-go-convention (how to write it) — this skill checks it
---

# Reviewing opensheet changes

A review answers two questions about every change: **does it follow the convention**, and
**when it changes next time, is that one edit in one place?** The findings are reported and
ranked; nothing is fixed until the user picks.

## 1. Pick the scope

```bash
.agents/skills/altalune-go-review/scripts/scope.sh [mode] [--patch]
```

| When                                       | Mode           | Covers                              |
| ------------------------------------------ | -------------- | ----------------------------------- |
| checkpoint mid-plan (every ~3 tasks)       | `delta`        | changes since the last `mark`       |
| re-review after fixing the last findings   | `delta`        | the fixes only                      |
| final pass before the plan's single commit | `branch`       | merge-base with main → working tree |
| only what is uncommitted                   | `wip`          | HEAD → working tree                 |
| one past commit                            | `commit <sha>` | that commit alone                   |
| everything after a tag or PR base          | `since <ref>`  | `<ref>` → working tree              |

No mode means `delta` when a mark exists for the current HEAD, else `branch`. `delta`
compares against the tree recorded by the last `mark`, so it shows only what changed since
that review. The user may also name files or a PR — that overrides the script.

## 2. Load the standards

Each lives in `.agents/skills/<name>/SKILL.md`; that project copy is the source of truth over any
same-named personal skill. Read the named sections, not the whole file; do not restate them from memory.

| Always                   | Read                                                                                        |
| ------------------------ | ------------------------------------------------------------------------------------------- |
| `go`                     | Core Principles, Interface Design, Concurrency Patterns, Error Handling, Testing Patterns   |
| `go-spec-reviewer`       | Step 2 table — the simplicity / YAGNI bar                                                   |
| `altalune-go-convention` | "Rules that are expensive to get wrong", plus the `docs/howto/` recipe for each change type |
| `comment-discipline`     | Keep, Delete, Traps                                                                         |

| Only if the diff touches                                            | Read                                       |
| ------------------------------------------------------------------- | ------------------------------------------ |
| an exported symbol in a root package or `internal/platform/`        | `go-release`: breaking change, Deprecation |
| a `go` statement, channel, `select`, `sync`, `errgroup` or a worker | `go-concurrency`: all of it                |
| `cmd/` or `internal/cli/`                                           | `cobra-viper`                              |
| a `.templ` file or htmx attribute                                   | `htmx-guidance`                            |

If a spec or plan drove the change (`docs/superpowers/specs/`, `docs/superpowers/plans/`),
read it: a change that does not do what the plan says is a finding.

## 3. Review in four passes

Read each changed file in full, not only its hunks — duplication hides outside the hunk.

1. **Bugs a user hits.** Wrong result, crash, data leak across tenants, blocked flow, broken
   boot. These are the only P0 candidates.
2. **Convention** → [`references/convention.md`](references/convention.md). The failures
   that pass review while wrong (every change), then the module checklist (domain modules).
3. **Design** → [`references/design.md`](references/design.md). One source of truth, reuse
   over reinvention, extension cost, scalability, and the counterweight against
   over-engineering.
4. **Gates.** Run `.agents/skills/altalune-go-convention/scripts/verify.sh --check` — read-only,
   it never writes a file. Add `--integration` if a `postgres.go` or migration changed. A red
   gate is a finding. Never run it without `--check` during a review: that mode regenerates
   and reformats files.

For a large diff with a subagent tool available, give passes 2 and 3 to parallel reviewers,
each with the scope command and its reference file. Merge and dedupe their findings.

## 4. Verify before reporting

Every finding needs evidence: a `file:line`, the second copy it duplicates, the command that
fails, or the input that breaks it. A finding you cannot show is a question, not a finding.
Drop anything a gate already enforces — a style nit `make lint` catches — unless the gate is red.

## 5. Report

Rank by severity, most severe first. The levels are the ones `BACKLOG.md` files under:

| Level | Meaning                                                           |
| ----- | ----------------------------------------------------------------- |
| P0    | critical, or a user or operator hits it right away and is blocked |
| P1    | correctness or security, not yet user-reachable                   |
| P2    | maintainability, reuse, extension cost, a missing guard or test   |
| P3    | cosmetic, naming, docs                                            |

Output, in this order:

```markdown
## Review: <scope mode> — <N> findings (P0 a · P1 b · P2 c · P3 d)

| #   | Sev | Category    | Where              | Finding                    | Fix (one line)            |
| --- | --- | ----------- | ------------------ | -------------------------- | ------------------------- |
| 1   | P0  | correctness | internal/x/y.go:42 | <what is wrong + evidence> | <smallest fix>            |
| 2   | P2  | duplication | a.go:10 vs b.go:88 | <the two copies>           | <which one becomes owner> |

**Questions** — ambiguities that need the author, not a fix.
**Gates** — each gate run, pass or fail.
```

Categories: `correctness`, `security`, `tenancy`, `convention`, `duplication`, `reuse`,
`extensibility`, `scalability`, `test`, `comment`, `docs`.

Then run `scope.sh mark`, so the next `delta` shows only what changes after this review.

## 6. Ask, then act

Ask the user which to do — fix a set of findings now, park the rest in `BACKLOG.md`, both, or
neither. Do not edit code before the answer. P0 is never parked — it is fixed or it blocks.
A parked P1–P3 finding goes under the first heading that starts with `## P<n> —` in
`BACKLOG.md` (create it if missing), as `### <finding>` plus the `file:line`, the evidence and
the fix.

After fixes, review again with `delta` — it covers the fixes only.

## Common mistakes

- Reviewing hunks only, and missing the existing helper three files away.
- Proposing an abstraction for one caller. That is its own finding (see design.md).
- Fixing before asking. The user decides.
- Committing. Never — the user commits once the plan is done.
