# Codex /goal routing signal research

Research date: 2026-11-20 Asia/Shanghai. Research only; no product changes, customer requests, or credentials used.

## Scope and source version

The user means Codex's actual `/goal` feature, not a JSON property named `goal`, an ordinary function named `goal`, or prose discussing a goal.

Inspected official `openai/codex` main at exact commit **`ab00c6d640e19d771d77859dc3eeff66f8a9168f`**, whose GitHub commit record reports **2026-11-19T16:21:15Z**. This is a pinned source snapshot, not a claim about the user's installed Codex version or the latest release. Sources were fetched from the official GitHub API/raw repository; no local checkout was changed.

## Result: a useful structured signal exists, but it is narrower than all active-goal work

**Automatic goal continuation sets `turn_trigger` to `goal`.** `codex-rs/ext/goal/src/runtime.rs:434-492` reads the persisted goal, requires active status, builds a continuation request, and starts the next turn with `TurnStartOptions.turn_trigger = Some("goal")` at line 490. This is materially stronger than looking for tool names or prompt words.

**The Codex request metadata serializer has an on-wire representation for `turn_trigger`.** `codex-rs/core/src/responses_metadata.rs:234-238` defines the canonical transport as the JSON string in `client_metadata["x-codex-turn-metadata"]`. `turn_trigger` is a reserved core metadata key (`:62,104`), a typed snapshot field (`:268`), copied into the serialized payload (`:487`), and serialized as an optional string (`:700`). `client_metadata()` inserts the canonical blob (`:401-405`); `compatibility_headers()` also emits a direct `x-codex-turn-metadata` header (`:422-437`). `core/src/client.rs:1376-1394` attaches this client metadata to Responses requests; HTTP options include compatibility headers (`:1782-1784`). WebSocket `response.create` uses per-message client metadata (`:2572-2580`), and the DTO supports it (`codex-rs/codex-api/src/common.rs:467-502`).

Representative request fragment for a goal-triggered turn:

```json
{
  "model": "example-model",
  "client_metadata": {
    "x-codex-turn-metadata": "{\"turn_trigger\":\"goal\"}"
  }
}
```

The real metadata contains additional fields. The header compatibility form contains the same JSON object as its value. This finding identifies a versioned structured discriminator suitable for a **goal-triggered request** predicate. A captured fixture from a supported client version is still required before treating the whole launch-to-serialization path as integration-verified.

### Limits that must remain explicit

- `turn_trigger` denotes what started the turn, not a universal `goal_active` boolean. The source evidence above does **not** establish that every request made while a goal exists carries `turn_trigger=goal`. A manually submitted message, resumed state, a child agent, compaction, or another internal request may have different/missing attribution. Do not advertise this predicate as 100% detection of all active-goal traffic.
- Older Codex builds, custom providers/proxies, or header/body rewriting may omit this metadata. Missing metadata means **unknown**, not proof that there is no goal.
- A WebSocket handshake header describes that connection's setup and can become stale across later turns. Prefer per-`response.create` metadata; a gateway that pins a connection once cannot silently claim per-turn channel rerouting without an explicit reconnect/reselection policy.
- The inspected metadata is asserted by the client. Core reserves these keys against its own app-server callers, but arbitrary HTTP callers can forge them. This can support compatibility routing, not cryptographic attestation or an unbypassable policy against hostile clients.
- No dedicated standard Responses field `goal_active` or goal-specific mandatory HTTP header was established. Do not invent such a field as if official Codex emits it. A future explicit client integration/opt-in marker could provide a stronger contract, but requires corresponding client support.

## Signals that must not be mistaken for active /goal

1. **Tool availability.** `ext/goal/src/extension.rs:555-610` advertises get/create/update goal tools whenever the runtime says tools are visible. `ext/goal/src/runtime.rs:124-129` checks feature enabled and thread capability, not active goal state. These tools can therefore appear in ordinary requests when no goal exists. Their exact definitions are `ext/goal/src/spec.rs:9-11`.
2. **Tool calls in history.** A create/update/get call or result may be historical, may have failed, or may refer to a completed/paused/cleared goal. Searching any input item or tool schema for the names will overmatch. If state reconstruction is ever attempted, it needs ordered call/result pairing, latest status, and incomplete-history handling; it is not equivalent to a simple feature check.
3. **Text wrappers.** User goal updates use `<codex_internal_context source="user_goal">` (`core/src/context/user_goal.rs:85-89`), and host annotations distinguish user-authored state (`:41-59`). Automatic continuation uses internal source `goal` and an additional `goal_objective` context (`ext/goal/src/steering.rs:57-77`). Text alone is not provenance, persists in history, and can be copied by ordinary users. The official user-goal reader itself requires metadata, not only matching text.
4. **App-server state.** Goal set/get/clear RPCs are managed inside the client/app server (`app-server/src/request_processors/thread_goal_user_context.rs:9-49`); the gateway only sees the subsequent model requests. It cannot assume it receives those control RPCs.

## Integration with existing channel rules

There is no existing goal feature predicate in this repository's inspected routing types. Reuse `dto.ChannelFilter` / `ChannelConstraints` rather than adding a second candidate-selection path:

- `dto/channel_constraints.go:36-55` defines existing filter kinds and fields.
- `middleware/distributor.go:46-51` adds ordinary request-path constraints before selection.
- `model/channel_constraint.go:12-17,92-109,176` applies the same eligibility predicates to cached, database, and retry candidate paths.
- `service/channel_select.go:19-46` owns request-scoped constraints; `relay/responses_websocket.go:325` initializes WebSocket path constraints.

If the requested channel setting is labelled “reject Codex goal traffic”, distinguish the executable contract in UI/help and tests: the directly supported detector is **recognized Codex goal-triggered turns**. Do not exclude all Codex traffic just because goal tools are available. For “never send any active-goal-associated request to this channel”, a complete reliable upstream marker has not been proven; this remains an explicit compatibility gap requiring client integration or a separately chosen conservative policy.

Parse this feature before channel retries/conversions rewrite the body and preserve the classification through retry. Use the raw request's bounded metadata extraction rather than general full-text scanning. For body/header disagreement, follow a documented canonical-body precedence (as official source states), and classify malformed/conflicting unsupported signals explicitly rather than merging unrelated `goal` text. A channel filter must also apply when trying alternative channels, not only the initial selection.

## Required fixture/regression cases

- Body metadata JSON string with `turn_trigger=goal`; header-only compatibility case; no metadata; non-goal trigger.
- Goal tools advertised with no goal: ordinary request remains eligible.
- User prose `/goal`, arbitrary `goal` field/tool, quoted goal prompt, and stale completed-goal history: do not trigger text/name-only rejection.
- Malformed/oversized metadata, wrong JSON types, body/header mismatch; bounded extraction and defined behavior.
- Recognized goal request skips a disallowing channel and chooses an eligible one on both initial selection and retry; pinned/all-filtered cases produce the configured explicit failure rather than silently changing pin semantics.
- WebSocket per-message attribution across goal/non-goal turns; do not use a stale handshake marker as current truth.
- Real sanitized wire fixtures from a documented supported Codex version for `/goal` start, automatic continuation, ordinary turn with tools available, manual user steering during an active goal, pause/clear/resume, and child-agent traffic. This is where the full-coverage claim can be tested; do not infer it from schema alone.

## Primary references

All source URLs below are pinned to the inspected commit:

- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/ext/goal/src/runtime.rs#L434-L492
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/core/src/responses_metadata.rs#L234-L238
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/core/src/responses_metadata.rs#L370-L437
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/core/src/client.rs#L1376-L1394
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/core/src/client.rs#L2572-L2580
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/ext/goal/src/extension.rs#L555-L610
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/ext/goal/src/spec.rs
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/core/src/context/user_goal.rs
- https://github.com/openai/codex/blob/ab00c6d640e19d771d77859dc3eeff66f8a9168f/codex-rs/ext/goal/src/steering.rs#L57-L77

No production detector was implemented, no wire fixture was captured, and no 100% active-goal detection claim is supported by this research.
