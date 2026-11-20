# Integration checkpoint

Status: implementation committed; final exact-SHA cloud verification and release
remain pending. Do not read earlier worker WIP sections as the final task status.

## Implemented

- Shared fixed-channel diagnostic admission, current policy after waiting,
  supported endpoint conversion and exactly-once known-usage settlement.
- Fingerprint default-three randomized samples with refreshed policy per sample.
- Specialized management transport, static headers, enabled credentials, total
  deadlines, cancellation and response-body-owned capacity.
- Balance/pricing/inference/Wham/Ollama requests use applicable saved policy.
- Complete enabled-key authoritative model inventories; explicit per-key sync
  remains add-only partial-success recovery.
- Saved previews preserve explicit form overlays and current saved capacity/TLS.
- Guarded model/balance/OAuth persistence preserves administrator replacements,
  current routing and scheduling metadata.
- Fixed management errors use existing translated categories; arbitrary upstream
  details remain unchanged.

## Last independent review

Controller/discovery review and independent Codex service audit found no
remaining source blocker. Two final review findings were corrected: explicit
sync preserves current ChannelInfo scheduling state, and model Apply reloads
the persisted row before rebuilding abilities. Existing regression suites were
extended without local execution.

## Cloud evidence

Earlier green checkpoints are recorded in implement.md. Run 44817543281 at
24f607376 passed backend/frontend and the database selectors but failed its
final race stage because the balance fixture lacked i18n initialization. The
fixture was fixed in 3e59f70ec. That failure is not a product completion pass.

593c0976f contains the final model-management implementation; ab2cf1b36 expands
external database selectors. All required backend, frontend, real SQLite/MySQL/
PostgreSQL, Redis and race jobs must pass on the final integration SHA before
main integration or release.

## Boundaries

No local tests, builds, lint, typecheck, benchmark, browser verification or paid
upstream probes were run. No server/SSH verification is requested. Ordinary
API-origin async task polling retains its separate task-bound lifecycle and is
not claimed as newly covered console management behavior. No schema or
dependency upgrade was introduced.
