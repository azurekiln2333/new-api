#!/usr/bin/env bash
set -euo pipefail

# Check the complete branch change, so a later fix commit cannot hide an
# earlier formatting failure. Main pushes compare against their previous tip.
if [ -n "${CI_PR_BASE:-}" ]; then
  check_base=$(git merge-base HEAD "$CI_PR_BASE")
elif [ "$GITHUB_REF" = "refs/heads/$CI_DEFAULT_BRANCH" ] &&
     [[ "${CI_PUSH_BASE:-}" =~ ^[0-9a-f]{40}$ ]] &&
     [ "$CI_PUSH_BASE" != '0000000000000000000000000000000000000000' ]; then
  check_base="$CI_PUSH_BASE"
else
  check_base=$(git merge-base HEAD "origin/$CI_DEFAULT_BRANCH")
fi

if [ "$check_base" = "$GITHUB_SHA" ]; then
  check_base=$(git rev-parse HEAD^)
fi
git cat-file -e "$check_base^{commit}"
printf 'CI_DIFF_BASE=%s\n' "$check_base" >> "$GITHUB_ENV"
printf 'Checking changes from %s to %s\n' "$check_base" "$GITHUB_SHA"
