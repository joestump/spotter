#!/usr/bin/env bash
# check-spec-ids.sh — fail if two OpenSpec specs claim the same SPEC-NNNN ID.
#
# A spec's ID is its frontmatter `id: SPEC-NNNN` field and/or its
# `# SPEC-NNNN:` title line. Specs without either are unnumbered and not
# checked. Two numbered specs sharing one ID, or one spec whose frontmatter
# and title disagree, is a governance violation (issue #311) and fails.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

declare -A owner=()
fail=0
count=0

for spec in docs/openspec/specs/*/spec.md; do
  [ -e "$spec" ] || continue
  count=$((count + 1))
  spec_name=$(basename "$(dirname "$spec")")

  fm_id=$(sed -n 's/^id:[[:space:]]*\(SPEC-[0-9]\{4\}\).*/\1/p' "$spec" | head -n 1)
  title_id=$(sed -n 's/^#[[:space:]]*\(SPEC-[0-9]\{4\}\).*/\1/p' "$spec" | head -n 1)

  if [ -n "$fm_id" ] && [ -n "$title_id" ] && [ "$fm_id" != "$title_id" ]; then
    echo "spec-id: $spec_name: frontmatter says $fm_id but title says $title_id" >&2
    fail=1
    continue
  fi
  id=${fm_id:-$title_id}
  [ -n "$id" ] || continue

  if [ -n "${owner[$id]:-}" ]; then
    echo "spec-id: $id claimed by both '${owner[$id]}' and '$spec_name'" >&2
    fail=1
  else
    owner[$id]="$spec_name"
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "spec-id: duplicate SPEC IDs found (issue #311)" >&2
  exit 1
fi
echo "spec-id: all SPEC IDs unique ($count specs checked)"
