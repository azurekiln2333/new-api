# Model identity and case policy

## 1. Scope and trigger

Model spelling is resolved before model authorization and retained through channel
selection, authoritative admission, retries, task routing and outbound conversion.
The site option `global.model_id_case_insensitive` defaults to false. Channel
`setting.model_id_case_insensitive` is an optional boolean: absent or null inherits
the site option, true enables it, and false disables it. Read the effective policy
at admission so a cached channel cannot retain an obsolete site decision.

## 2. Signatures and owners

- `model.ResolveModelIDWithCatalog(requested string, userID int, groups []string) (string, string, error)`
  returns canonical public identity and exact catalog spelling.
- `service.ResolveRequestModelIdentity(c *gin.Context, requested string) (string, error)`
  freezes requested, canonical and catalog identities in context and installs
  `dto.FilterModelIdentity` with the authenticated user and permitted groups.
- `(*model.Channel).AdmitsModelIdentity(requested, canonical string, catalogModels ...string) bool`
  checks the current channel catalog and effective policy.
- `(*model.Channel).RoutingUpstreamModelNameForKey(index int, modelName string) (string, bool)`
  applies the selected allocation's mapping after public identity resolution.

## 3. Contracts

Build the catalog from permitted public group models and allocations bound to the
caller. Exact catalog matches win. Otherwise fold ASCII A-Z only and require one
unambiguous catalog spelling with case matching enabled. Health, quota or a
disabled override must not arbitrarily resolve a spelling collision. Unknown
names retain the existing exact routing/plugin validation path.

Preserve the matched catalog spelling independently of the canonical public name.
A full catalog ID resembling a legacy routing modifier must not fall through to
another channel's stripped model. Token permissions, group allowlists and Key
scopes remain exact comparisons after resolution. A selected Key's mapping and
its upstream target retain their configured case. A separately named price or
billing expression blocks substitution even if its numeric price is identical.

Passthrough may rewrite the outbound model to the resolved/mapped identity while
preserving unknown JSON members and the immutable original request capture.
Policy is rechecked before dispatch, including budget admission and reused
WebSocket channel state. Plugin declarations or aliases do not grant catalog
access by themselves. Preserve exact aliases before considering folded plugin
declarations.

For older channel editors, `MergeSettingPatch` preserves only this new override
when omitted; other settings retain their prior replacement behavior. Explicit
null restores inheritance. Frontend setting serialization follows the same rule.

## 4. Validation and errors

| Condition | Result |
| --- | --- |
| Exact reachable catalog spelling | Preserve that identity |
| One folded spelling, effective policy enabled | Use canonical spelling; retain requested spelling |
| Multiple folded spellings | Reject with an ambiguous-model error |
| Folded spelling with policy disabled | Reject the case substitution |
| Requested spelling has its own price/expression | Require exact catalog spelling |
| Current channel loses exact catalog/scope/policy eligibility | Reject that attempt |
| Catalog read fails | Propagate failure; never synthesize an empty successful catalog |

## 5. Good, base and bad cases

- Good: `DEEPSEEK-R1` resolves to sole permitted `deepseek-r1` when enabled, then
  the selected Key maps it to the exact upstream spelling `DeepSeek-R1`.
- Base: the default-disabled site and inherited channel keep exact matching.
- Bad: selecting a healthier `Model` over a second catalog spelling `model`,
  folding a token allowlist, or treating a case-only mapping hop as a self-cycle.

## 6. Required validation

The hosted controller matrix runs `TestChannelModelIDCasePolicy`,
`TestChannelModelIDCatalogFenceAndPassthroughAdmission` and
`TestChannelUpdateCasePolicyPersistsAndKeepsReplacementSemantics` with
`TEST_CHANNEL_SQL_DSN` for SQLite, MySQL and PostgreSQL. Cover exact-first matching,
collisions, group/Key scope, policy changes, mapping targets, setting inheritance
and observable outbound passthrough behavior. Middleware tests cover catalog
grants and selected-channel plugin/alias routing; the latter has its own real
three-database loop. `TestPreparePassthroughBodyCanonicalModel` covers immutable
request capture with memory and disk bodies. Run the affected frontend settings
suites and independent `GOWORK=off go build ./...` in `relaykit` in hosted CI.

Source review and selector presence do not establish a passing matrix or deployed
behavior. Record the tested SHA and actual engine versions with results.

## 7. Wrong versus correct

Wrong: lowercase a request and use it interchangeably as catalog, permission,
pricing and upstream identity, or copy a channel-wide mapping onto every Key.

Correct: freeze requested/canonical/catalog identities, enforce exact scopes and
current policy, recover the selected stable allocation, and only then apply its
exact mapping to the outbound model.
