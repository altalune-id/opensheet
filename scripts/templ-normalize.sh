#!/usr/bin/env bash
# Rewrite templ's FileName strings to repo-root-relative paths.
# NOTE: templ bakes its invocation path into every templ.Error, so a `templ lsp` watcher
# running inside the templates directory rewrites every generated file on save.
set -euo pipefail

cd "$(dirname "$0")/.."

changed=0
while IFS= read -r gen; do
	rel="${gen#./}"
	dir="$(dirname "$rel")"
	base="$(basename "$rel" _templ.go)"
	before="$(cat "$gen")"
	# shellcheck disable=SC2016
	after="$(printf '%s' "$before" | sed "s|FileName: \`${base}.templ\`|FileName: \`${dir}/${base}.templ\`|g")"
	if [ "$before" != "$after" ]; then
		if [ "${1:-}" = "--check" ]; then
			echo "  not root-relative: $rel" >&2
		else
			printf '%s\n' "$after" > "$gen"
			echo "  normalized $rel"
		fi
		changed=$((changed + 1))
	fi
done < <(find . -name '*_templ.go' -not -path './node_modules/*')

if [ "${1:-}" = "--check" ] && [ "$changed" -gt 0 ]; then
	echo "templ FileName paths were not root-relative; run 'make templ-normalize'" >&2
	exit 1
fi
