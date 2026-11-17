# Finish outstanding workspace changes

## Goal

Finish and verify the existing `merge/upstream` worktree after the Herdr Claude pane `w4C:pV` stopped in merge-conflict resolution. Preserve the feature work already represented by the active Trellis tasks and produce a tested commit ready for push/tag/CI.

## Requirements

- Resolve every unmerged path without dropping compatible work from either side.
- Remove all conflict markers and keep Go modules, frontend code, locale files, and workflows consistent.
- Run supported focused and broad checks; report unavailable database/provider/hosted checks exactly.
- Commit with the repository's standard message, push the branch, create and push a tag, and report CI/CD status.

## Acceptance Criteria

- [ ] No paths remain in `git diff --name-only --diff-filter=U`.
- [ ] No tracked file contains merge conflict markers.
- [ ] Affected Go and frontend checks pass, with unsupported checks documented.
- [ ] Final commit preserves the existing worktree changes and is pushed successfully.
- [ ] A release tag is pushed and the triggered workflow is identified.

## Out of scope

- New product behavior beyond current worktree changes.
- Reverting unrelated user changes.
- Claiming live three-database compatibility without running the required instances.
