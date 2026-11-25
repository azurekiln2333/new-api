# Independent channel Key allocations

This child task covers parent R1, R2, R3 and R5. Requirements come from the user's request. The operating rules in the design are proposals for the consolidated review.

## Goal and requirements

- R1. Allow the same upstream credential to be added more than once in one channel. Each allocation has its own identity and can be public to the channel's groups or bound to one user, including a user in another group. A bound allocation is exclusive to that user.
- R2. In the multi-Key manager, set or change an allocation's quota and reset its used amount separately. The allocation quota checks available capacity before forwarding, using the existing billing estimate. Record actual settled use even when it exceeds the estimate. Continue normal user wallet or subscription and API token charges. Reset affects the allocation counter only and preserves consumption history.
- R3. Provide an explicit deduplication action. Compare the credential, user or public permission, quota setup, model restrictions, status and other effective restrictions. Preserve entries whose effective settings differ.
- R5. Include allowed bound models in the API and console model lists. The effective scope is the channel's models intersected with the allocation's scope and applicable token restrictions. Preserve the existing billing group and precedence of an eligible user binding on the first attempt.

## Constraints

Append currently removes equal credentials at `controller/channel.go:1381`. Remapping at `model/channel.go:1108` picks the first matching credential, and status/discovery also use text or indices. Identity must remain stable when identical rows are removed, reordered or updated concurrently. Binding and both model lists already have an implementation in `service/channel_select.go:394`, `controller/model.go:266` and `controller/user.go:676`. Extend those rules.

`controller/relay.go:272` prepares request billing once while upstream attempts can switch allocations. The allocation counter follows each actual attempt. `model/channel.go:1676` updates historical channel statistics, potentially in batches. The new allocation limit requires authoritative admission state across gateway nodes. Use the same budget lifecycle for channel limits in the sibling task.

## Acceptance

- AC1. Add and reload public, Alice-bound and Bob-bound rows sharing an upstream credential. Selection, scope edits, reorder, disable, discovery and deletion consistently affect the specified allocation.
- AC2. Edit a limit without changing the used amount. Reset an allocation without changing channel budget, prior consumption logs or user funding. Concurrent attempts cannot reserve more than the available estimate capacity. Record an actual amount greater than its estimate, then reject new attempts once the budget is exhausted. Retried failures and task adjustments do not duplicate or misattribute usage.
- AC3. Preview and merge equivalent rows. Keep one configured limit and conserve used counters. Skip allocations with in-flight attempts or nonterminal tasks. Rows with different eligibility remain separate. Old settlements and refunds still resolve the correct historical entries.
- AC5. A bound user sees and can request allowed cross-group models. Other users cannot see or use them by virtue of that binding. Revocation, quota exhaustion or model changes remove that binding's contribution while leaving independently eligible models visible.
- AC7. Verify fresh and upgraded SQLite, MySQL and PostgreSQL instances, rerun migrations and verify IDs and existing state. Verify the console actions and all seven locales.

## Scope boundary

The sibling tasks cover manual channel budget controls and request diagnostics. User group policy, upstream provider pricing, existing station pricing formulas and cross-region deployment architecture are outside this change. The managed credential account uniqueness and refresh contract remains in force.
