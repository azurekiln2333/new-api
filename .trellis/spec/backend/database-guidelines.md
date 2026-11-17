# Database Guidelines

> Database patterns and conventions for this project.

---

## Overview

<!--
Document your project's database conventions here.

Questions to answer:
- What ORM/query library do you use?
- How are migrations managed?
- What are the naming conventions for tables/columns?
- How do you handle transactions?
-->

(To be filled by the team)

---

## Query Patterns

<!-- How should queries be written? Batch operations? -->

(To be filled by the team)

---

## Migrations

<!-- How to create and run migrations -->

(To be filled by the team)

---

## Naming Conventions

<!-- Table names, column names, index names -->

(To be filled by the team)

---

## Per-key and per-group channel configuration (channel key scoping)

Channel-level routing configuration that needs no schema change lives in two existing
JSON columns. Both are whitelists intersected with `channels.models`, so stale names
are ignored instead of erroring.

| Config | Struct | Column | Contract |
|---|---|---|---|
| key index -> models | `model.ChannelInfo.MultiKeyModels` | `channels.channel_info` | missing/empty = the key serves every channel model |
| key index -> user id | `model.ChannelInfo.MultiKeyUsers` | `channels.channel_info` | missing/0 = public key; a bound key is exclusive to that user |
| group -> models | `relaykit/dto.ChannelOtherSettings.GroupModels` | `channels.settings` | missing/empty = the group sees every channel model |

Rules that keep this consistent:

- **Key-indexed maps follow key contents, not indexes.** Any path that changes the key
  list (`Channel.Update`, `UpdateChannel` key append/replace, `ManageMultiKeys` delete
  actions) must remap the per-key maps by "old key string -> new index"
  (`Channel.remapKeyScopes`). A map keyed by index alone silently moves a binding onto a
  neighbouring key after a delete.
- **One derivation for `group x model`.** `Channel.channelAbilities()` in `model/ability.go`
  is the only place that turns a channel into abilities, and `model.InitChannelCache()`
  builds the in-memory routing table from the same inputs (`PublicModels()` and
  `GroupModels`). Changing only the abilities table leaves `MemoryCacheEnabled` routing
  (the default) wrong, so both must be updated together.
- **Abilities describe public keys only.** A channel whose keys are all bound to users
  produces no abilities at all; bound-key access is a separate path
  (`model.GetUserBoundChannel`) that runs before group routing and before session
  affinity, and it must never write affinity state another user could hit.
- **Key enable status is not part of abilities.** A temporarily disabled key must not
  change the routing table; the per-request key selection reports the miss instead.
- **"No key serves this request" is not a channel error.** Use
  `types.ErrorCodeChannelKeyScopeUnsatisfied` (503, no `channel:` prefix) so the channel is
  not auto-disabled; the distributor then falls back to the next candidate channel instead
  of forwarding with an empty key.

---

## Common Mistakes

<!-- Database-related mistakes your team has made -->

(To be filled by the team)
