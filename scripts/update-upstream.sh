#!/bin/sh
# Review-only: creates a dedicated merge worktree; never pushes or runs upstream code.
set -eu
repo=$(git rev-parse --show-toplevel)
git remote get-url upstream >/dev/null 2>&1 || git remote add upstream https://github.com/digisamroc/eraser.git
git fetch origin main
git fetch upstream main --tags
branch="upstream-review-$(date -u +%Y%m%d-%H%M%S)"
worktree="${repo}/../${branch}"
git worktree add -b "$branch" "$worktree" origin/main
if git -C "$worktree" merge --no-commit --no-ff upstream/main; then
  echo "Merge staged in $worktree. Review all code and catalog changes before tests."
else
  echo "Resolve merge conflicts in $worktree. Preserve all security invariants in SECURITY_AUDIT.md."
fi
echo 'Run the full security/build checks, commit, push Gitea main, then fast-forward GitHub main. Never auto-merge upstream.'
