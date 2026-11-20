# playground model navigation

## Goal and acceptance

Own R2,R3 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Admin Group/Channel navigation uses real validated server pins and effective group/model/key restrictions; returning to groups clears pin. Sort copies naturally with valid fixed en collator, stable ties and preserved selection.

Validation: Selector desktop/mobile/filter/order tests; forged unauthorized/ineligible pins rejected; actual routed channel asserted.

## Dependencies and boundaries

Separate R2 and R3 commits; serialize selector changes. Owner: pJ.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Playground and upstream endpoints” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Completed and released in `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Full integration CI `44739249885` and release workflow `44742578933` passed. See the parent report and verified-release ledger for exact evidence. No server verification was performed, as requested.

## Confirmed visibility correction (2026-11-20)

Only administrators may see or use manual channel selection in the playground.
Every other user sees only the ordinary group/model selection allowed by their
group. Do not expose inaccessible channel names, IDs, counts or dropdown entries,
even when a non-administrator has an unrelated/custom channel permission.
Hide the Group/Channel switch and never fetch the channel list for them.
Reject a forged channel list request or channel pin on the server. Clear any
restored/stale channel selection when privileges or identity change so no hidden
pin is transmitted. Administrator channel choices still honor the effective
group/model/key eligibility rules. Keep backend and frontend checks consistent.

This is an independently revertible follow-up commit. All automated validation
runs in GitHub CI; no local tests/build/lint/browser checks or server rollout
verification are authorized.
