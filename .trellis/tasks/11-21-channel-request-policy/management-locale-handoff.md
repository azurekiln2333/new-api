# Management error localization handoff

Status: bounded implementation complete; source-reviewed and locale-normalized. Runtime validation remains cloud-only and has not run for this change.

## Behavior and boundary

- The initial localization chunk, committed as `8ab5fb06a`, added exact handling for the 11 gateway failure strings confirmed against backend sources and the worker-4 audit. Eight locale keys cover distinct errors; the read/unsuccessful-pricing errors reuse `Upstream request failed`, and the oversized response reuses `Response too large`. The bounded follow-up below extends that formatter for the remaining audited discovery and diagnostic paths.
- Structured `message_key` / `message_params` continue to take precedence in `getServerMessage`. The new raw-message fallback uses the whitelist; unknown provider text returns verbatim, including whitespace, interpolation-looking content and strings that happen to have other locale entries.
- Both nested pricing failure paths now format known gateway errors: the all-source failure shown by the existing `ErrorState`, and the partial-source warning toast. Source display names and existing selection/retry behavior are preserved.
- Stored balance errors use the same whitelist in the existing `ChannelBalanceDetails` line. Whitespace-only errors still render nothing, and nonblank provider text is no longer trimmed before display.
- Reused the existing server-message formatter, `ErrorState`, balance detail component, dialogs and toast paths. No new UI component or interaction was needed. No backend, pricing arithmetic, authentication, dependencies, unrelated files, docs or plugin documentation changed.

## Exact code/test files

- `web/src/lib/server-error-message.ts`
- `web/src/lib/server-error-message.test.ts`
- `web/src/features/channels/components/channel-balance-details.tsx`
- `web/src/features/channels/components/__tests__/channel-balance-cell.test.tsx`
- `web/src/features/system-settings/models/upstream-ratio-sync.tsx`
- `web/src/features/system-settings/__tests__/pricing-sync.test.tsx`
- `web/src/i18n/locales/en.json`
- `web/src/i18n/locales/zh.json`
- `web/src/i18n/locales/zh-TW.json`
- `web/src/i18n/locales/fr.json`
- `web/src/i18n/locales/ja.json`
- `web/src/i18n/locales/ru.json`
- `web/src/i18n/locales/vi.json`

## Locale script execution

- Read the full i18n skill and confirmed all eight English keys at literal `t(...)` call sites before locale mutation.
- Applied all locale writes through temporary `web/scripts/add-missing-keys.mjs`, using the skill's insertion/sorting structure and seven populated language maps. Its serializer preserved the existing escaped project attribution key, matching the repository sync script.
- From `web/`, executed `& 'D:\ProgramFiles\nodejs\node.exe' scripts/add-missing-keys.mjs`: exit 0, eight translations applied in each of seven locales (56 total). Deleted the temporary script afterward as required by the skill.
- Bun is unavailable on PATH and the conventional user Bun location was absent. Confirmed `web/package.json` defines `i18n:sync` as exactly `node scripts/sync-i18n.mjs`, then used the root-authorized fallback: `& 'D:\ProgramFiles\nodejs\node.exe' scripts/sync-i18n.mjs` from `web/`. Exit 0.
- Read back the generated sync report: `en.json` base, zero missing and zero extra keys in every locale. The report still lists unrelated existing untranslated candidates (ja 6, ru 7, zh 5); none is a new key.
- Reviewed locale diffs: exactly eight additions per locale and no deletions or other locale-value changes (56 inserted lines total). Protected attribution serialization and values remain unchanged. Generated reports are ignored and are not handoff code changes.

## Tests authored, not run

- Existing server-error test file: all 11 fixed gateway strings in Chinese; unknown provider text including a separately translated key, prefix/suffix variants and interpolation syntax; structured key precedence. Existing structured-parameter coverage remains.
- Existing balance-cell test file: stored fixed error translated in Chinese; provider error with an existing locale entry remains English and retains surrounding whitespace.
- Existing pricing-sync test file: all-source nested failure displays a translated gateway reason plus unchanged provider detail; retry exercises the partial-source warning with the same detail.

No local tests, builds, lint, typecheck, browser checks, installs or network probes ran. Per the task, this handoff does not claim those checks passed. Source readback covered helper precedence, the 11 backend literals, both pricing consumers, stored balance presentation, the new test fixtures, all seven locale diffs and the sync report. No stage, commit or push was performed.

## Remaining follow-up

- Root owns independent review, exact-SHA cloud frontend tests/typecheck/lint/build and the rest of the task validation. Run the three modified existing frontend test files in cloud.
- The whitelist deliberately does not translate arbitrary upstream text or pre-existing management errors outside the audited set.
- `.trellis/tasks/11-21-channel-request-policy/management-i18n-handoff.md` is the earlier interrupted worker note; this handoff supersedes its pending implementation/script steps without editing that other file.

## Bounded discovery and diagnostic follow-up

Status: implementation and source readback complete; frozen for root review. No local runtime validation has run.

### Exact ownership

This follow-up changes only `web/src/lib/server-error-message.ts`, its existing `server-error-message.test.ts`, the seven existing locale files listed above, and this handoff. The earlier pricing/balance component and component-test changes are already in `8ab5fb06a` and are unchanged by this follow-up. No backend or worker-owned file was edited. Root alone stages, commits and validates in cloud.

### Formatter behavior

- Exact new policy, diagnostic, Gemini inventory/pagination, Ollama inventory/pull and Codex discovery failure strings reuse existing locale keys. The original whitelist remains intact. Known canonical `Invalid response`, `Response too large`, `Invalid parameters`, channel-header/proxy and upstream-request errors are also handled after discovery flattens their backend message objects. Only the three explicitly audited Codex version-failure compositions are recognized; there is no general prefix translation.
- `getServerMessage` continues to prioritize structured keys. Only `Failed to fetch models: {{error}}` and `Failed to fetch Ollama version: {{error}}` format their string `message_params.error` through the whitelist. Other structured keys and parameters retain their existing behavior. Parameter objects are not mutated. For those two wrappers, nesting stays disabled and interpolation does not HTML-encode provider details; React remains responsible for escaping rendered text.
- The existing raw `error` detail branch in `getServerErrorMessage` now applies the same whitelist, covering Ollama SSE errors without adding a frontend-specific error parser. Existing stable auth-code and safe-error precedence is unchanged.
- The numeric gateway wrapper `enabled key <nonnegative integer> model discovery failed: <reason>` is formatted only when the entire reason is one exact fixed gateway failure. It retains the backend's original index. Unknown provider reasons, suffix detail, leading/trailing whitespace, a final newline, invalid index shapes and recursive wrappers remain verbatim. Recognition does not depend on whether a translation differs from the English source.
- Added one literal key, `Model discovery failed for enabled key {{index}}: {{error}}`, with actual translations in all seven locales. Existing source/provider detail is not used as a translation key.

### Authored regression coverage, unrun

- New fixed discovery/diagnostic/Ollama/Codex strings and reused canonical messages through raw `message` and raw `error` details.
- Both recognized structured wrapper keys, unchanged handling for another structured key, source-parameter immutability, and existing structured-key precedence.
- Known numeric-wrapper reasons in Chinese and English; provider reasons, appended detail, whitespace/newline suffixes and negative/fractional indices remain untouched.
- Provider text containing whitespace, angle brackets, interpolation syntax and nesting syntax remains verbatim through direct error details and both recognized structured wrappers.

### Locale normalization and readback

- Used temporary `web/scripts/add-missing-keys.mjs` with the mandatory seven-language insertion/sorting structure, preserving the repository's escaped attribution-key serialization. Executed the approved Node fallback from `web/`: `& 'D:\ProgramFiles\nodejs\node.exe' scripts/add-missing-keys.mjs`; exit 0, one translation per locale, seven total.
- Executed the exact package-script equivalent `& 'D:\ProgramFiles\nodejs\node.exe' scripts/sync-i18n.mjs`; exit 0. Removed the temporary insertion script afterward.
- Read back all seven locale diffs: exactly one addition each, no deletions or unrelated value/attribution changes. The generated report records zero missing/extra keys in every locale; unchanged unrelated untranslated candidates remain ja 6, ru 7 and zh 5.
- Reviewed the final formatter/test diff and exact whitelist/nested-wrapper boundaries. No local tests, builds, lint, typecheck, browser checks, installs or upstream probes ran. Cloud checks remain required, including the existing server-error test file.

### Remaining boundary

Backend owners are still integrating discovery and diagnostics; root should recheck literals against their final frozen sources. The new preview message `header_override must be a JSON object: <decoder error>` and inherited/provider-controlled prose are deliberately outside this whitelist follow-up. The earlier read-only audit recommended using the existing structured header-override validation message at that backend producer; this worker made no backend edit.
