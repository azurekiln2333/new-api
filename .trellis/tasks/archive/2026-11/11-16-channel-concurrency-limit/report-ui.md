# UI implementation report

Completed native takeover of the stopped Pi UI worker. No Git writes.

Changed files:
- web/src/features/channels/components/channel-request-limits.tsx (new business composition)
- web/src/features/channels/components/drawers/channel-mutate-drawer.tsx
- web/src/features/channels/components/__tests__/channel-configuration.test.tsx
- web/src/features/channels/lib/channel-form.ts
- web/src/features/channels/lib/channel-configuration.ts
- web/src/features/channels/types.ts
- web/src/features/system-settings/request-policies/defaults.ts
- web/src/features/system-settings/request-policies/routing-form.ts
- web/src/features/system-settings/request-policies/retry-section.tsx
- web/src/features/system-settings/request-policies/__tests__/routing.test.tsx
- Seven locale JSON files (en, zh, zh-TW, fr, ja, ru, vi)

Behavior: channel-wide cap shared by all keys, zero/unset unlimited, timeout inheritance versus custom0/custom positive, safe integer bounds, full form load/save/create/update serialization, configured status/error tab discovery, sensitive-write field protection. Global timeout is saved alongside retry settings, missing retry budget defaults to2 while saved0 remains0. Help explains first BODY byte, upstream heartbeats, ongoing streams, zero-retry behavior, Redis/per-process limits, cache refresh propagation, retry504/524 and exhausted-candidate stopping.

Reuse: inspected existing channel form/Select patterns and project Form/Input/Select implementation, used these unchanged plus existing safeNumberFieldProps. New component only composes feature fields inside the existing sensitive fieldset; no generic UI behavior duplicated. Read shadcn-ui and i18n-translate skills. Added16 UI/backend source keys with112 translated values using temporary add-missing-keys.mjs, ran i18n:sync, then deleted the temporary script. All locales have missingCount0/extrasCount0; targeted scan verified25 used source keys in all7 languages. Existing unrelated untranslated report entries remain.

Validation:
- Added regression tests first and observed12 failures before implementation.
- bun run test src/features/channels/components/__tests__/channel-configuration.test.tsx src/features/system-settings/request-policies/__tests__/routing.test.tsx src/features/system-settings/request-policies/__tests__/settings.test.tsx: PASS,3 files/166 tests (95.79s).
- Additional focused new-settings/permission tests: PASS,13 cases.
- bun run typecheck: PASS,1471 files across2 projects.
- bun run build: PASS, production output generated.
- node node_modules/oxlint/bin/oxlint -c .oxlintrc.json [10 changed TS/TSX files]: PASS after fixing3 test braces.
- node node_modules/oxfmt/bin/oxfmt -c .oxfmtrc.json --check [10 changed TS/TSX files]: PASS.
- git diff --check: PASS.
- Full bun run lint was executed: FAIL with143 pre-existing errors in unrelated unchanged files (examples footer.tsx array-index keys, home/constants.ts type import, auth/lib/validation.ts replaceAll, scripts/sync-i18n.mjs curly). No changed-file lint errors remain; do not claim full lint passes.
- Full copyright check executed:24 pre-existing unchanged files need headers. The2 affected files formerly missing headers (routing-form.ts/routing.test.tsx) now have the standard complete header. Existing protected headers preserved; no bulk unrelated rewrites.

Integration: backend timeout report is report-timeout.md. Local Go race blocked by disabled CGO; DB/Redis matrix and Linux race remain root/CI work. Final controller localization/outer keepalive are owned by capacity worker. Main owns independent review, commit/push/tag and observed CI/CD completion.
