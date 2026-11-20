# Management error i18n handoff

Status: implementation in progress; no local validation is authorized.

## Change boundary

- The gap is presentation of the task's fixed gateway errors: raw API messages, nested pricing-source errors and stored balance errors currently bypass translation.
- Extend the existing `web/src/lib/server-error-message.ts` formatter with an exact whitelist for the 11 source strings confirmed in `worker-4.md` and `service.ErrChannelBalanceSourceChanged`. Preserve structured `message_key` / `message_params` precedence and all unknown provider text.
- Wire that formatter into the two existing pricing error displays and the existing balance detail line. Reuse current UI components and layout; no new interaction or formatter for unrelated server text.
- Extend existing server-message, pricing-sync and balance-cell tests for Chinese fixed errors and unchanged unknown provider text. Tests are authored only and remain for cloud execution.
- Add eight source keys in all seven locales through temporary `web/scripts/add-missing-keys.mjs`; reuse existing `Response too large` and `Upstream request failed` for the three equivalent transport/pricing failures.
- No backend, pi1-owned UI, pricing arithmetic, authentication, dependencies or unrelated files are changed.

## Script execution

- `bun run i18n:sync` could not start because Bun is absent from this shell's PATH.
- Root explicitly approved `node scripts/sync-i18n.mjs`, which is the exact `i18n:sync` package script, as the normalization fallback.

## Pending

- Finish edits and script-only locale writes; inspect exact diffs.
- Root owns independent review, cloud tests/checks and all git writes.
