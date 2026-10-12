#!/usr/bin/env bash
# Print what a review should cover, and remember what was reviewed.
#
#   scripts/scope.sh               delta if a mark exists for this HEAD, else branch
#   scripts/scope.sh delta         changes since the last `mark`
#   scripts/scope.sh wip           uncommitted changes (tracked + untracked) vs HEAD
#   scripts/scope.sh branch        everything since the merge-base with main, committed or not
#   scripts/scope.sh commit <sha>  that one commit alone
#   scripts/scope.sh since <ref>   everything from <ref> to the working tree
#   scripts/scope.sh mark          record the current tree as reviewed
#   add --patch to any mode to print the full diff instead of the file list
#
# Snapshots are git tree objects built in a throwaway index, so the real index, stash
# and working tree are never touched. Nested git repos (agent worktrees) are skipped.
# The mark lives in the per-worktree git dir.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
gitdir="$(git rev-parse --git-dir)"
markfile="$gitdir/altalune-go-review.mark"
main="${ALTALUNE_REVIEW_MAIN:-main}"

snapshot() {
  local idx
  idx="$(mktemp)"
  rm -f "$idx"
  GIT_INDEX_FILE="$idx" git read-tree HEAD
  GIT_INDEX_FILE="$idx" git add -u
  git ls-files -z --others --exclude-standard | tr '\0' '\n' | { grep -v '/$' || true; } | tr '\n' '\0' |
    GIT_INDEX_FILE="$idx" xargs -0 git add --
  GIT_INDEX_FILE="$idx" git write-tree
  rm -f "$idx"
}

patch=0
args=()
for a in "$@"; do
  if [ "$a" = "--patch" ]; then patch=1; else args+=("$a"); fi
done
mode="${args[0]:-auto}"
arg="${args[1]:-}"

head="$(git rev-parse HEAD)"

if [ "$mode" = "mark" ]; then
  now="$(snapshot)"
  printf '%s %s\n' "$head" "$now" >"$markfile"
  printf 'marked tree %s at HEAD %s\n' "${now:0:12}" "${head:0:12}"
  exit 0
fi

if [ "$mode" = "auto" ]; then
  mode=branch
  if [ -f "$markfile" ] && [ "$(cut -d' ' -f1 "$markfile")" = "$head" ]; then
    mode=delta
  fi
fi

case "$mode" in
  delta)
    [ -f "$markfile" ] || { echo "no mark yet — run: scope.sh branch" >&2; exit 2; }
    base="$(cut -d' ' -f2 "$markfile")"
    now="$(snapshot)"
    label="since last review mark" ;;
  wip)
    base="$(git rev-parse 'HEAD^{tree}')"
    now="$(snapshot)"
    label="uncommitted vs HEAD" ;;
  branch)
    base="$(git rev-parse "$(git merge-base "$main" HEAD)^{tree}")"
    now="$(snapshot)"
    label="since merge-base with $main" ;;
  commit)
    [ -n "$arg" ] || { echo "usage: scope.sh commit <sha>" >&2; exit 2; }
    git rev-parse --verify --quiet "$arg^" >/dev/null ||
      { echo "$arg has no parent (root commit) — use: git show $arg" >&2; exit 2; }
    base="$(git rev-parse "$arg^^{tree}")"
    now="$(git rev-parse "$arg^{tree}")"
    label="commit $arg alone" ;;
  since)
    [ -n "$arg" ] || { echo "usage: scope.sh since <ref>" >&2; exit 2; }
    base="$(git rev-parse "$arg^{tree}")"
    now="$(snapshot)"
    label="since $arg" ;;
  *)
    echo "unknown mode: $mode" >&2; exit 2 ;;
esac

printf '=== scope: %s (%s)\n' "$mode" "$label"
if [ "$base" = "$now" ]; then
  echo "nothing changed."
  exit 0
fi
if [ "$patch" -eq 1 ]; then
  git diff "$base" "$now"
  exit 0
fi
git diff --stat=100 "$base" "$now" | tail -1
git diff --name-status "$base" "$now"
printf '\nfull diff:      %s %s%s --patch\n' "$0" "$mode" "${arg:+ $arg}"
printf 'one file:       git diff %s %s -- <path>\n' "${base:0:12}" "${now:0:12}"
