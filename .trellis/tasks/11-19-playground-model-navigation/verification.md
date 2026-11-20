# R2 review and handoff — 2026-11-20

R3 was committed separately as `41325a670`; the following ledger owns R2 only.

## Behavior and review

- Admin-only channel metadata returns `id`, `name`, and eligible `models`, with `Cache-Control: no-store`. Browser role/permission checks match the server's admin plus `ChannelRead` checks. Personal access tokens and ordinary users cannot list/pin channels.
- Pins are validated against enabled channel, group/model restrictions, active public keys, and caller-bound keys. Auto resolves the first eligible permitted group. The existing channel constraint system enforces request-path filters and disables silent failover for the selected channel.
- Switching back to groups or changing groups clears the pin; pins are excluded from persisted config. Channel navigation retains the existing natural model order. Missing/removed pins remain visible as unavailable and block sending.
- Review fixed an upstream leak: validated `channel_id` is removed from the reusable request body before passthrough. The regression failed before the fix and passed after. Opaque provider fields retain explicit zero, false, and large integer JSON values without float conversion.
- Reused `ModelGroupSelector`, `ToggleGroup`, `Button`, existing query/auth/constraint/JSON body storage services; no new parallel selection framework.

## Verification

- Portable Go 1.26.10, `GOMAXPROCS=2 go test -p 1 ./controller -run '^TestPlaygroundChannelNavigationAuthorizationAndRouting$' -count=1`: PASS, 36 scenarios across cache off/on using SQLite. This test uses `TEST_CHANNEL_SQL_DSN` and still requires MySQL/PostgreSQL runs in CI.
- Bun 1.4.4, `bun run test src/components/model-group-selector/__tests__/sorting.test.tsx src/features/playground/hooks/__tests__/use-playground-options.test.tsx src/features/playground/hooks/__tests__/use-playground-state.test.ts --maxWorkers=2`: PASS, 19 tests.
- `bun run typecheck`: PASS, 1,586 files / 2 projects after fingerprint route generation settled.
- Scoped `bun x --no-install oxlint -c .oxlintrc.json` on the 12 frontend paths below including shared types/payload builder: PASS.
- R2-only formatting preserves all copyright comments. Scoped `git diff --check`: PASS.
- A full-project lint invocation also reported pre-existing errors outside R2. No claim of global lint success is made.

## Commit file ledger

Backend files:

1. `controller/playground.go`
2. `controller/playground_test.go` (new)
3. `service/playground.go` (new)
4. `model/channel_satisfy.go`
5. `router/relay-router.go`
6. `dto/channel_constraints.go`: only `PinSourcePlayground` / `PinRankPlayground` additions; exclude concurrent R17 `FilterGoalTurn`.
7. `middleware/distributor.go`: only playground parsing/pin/stripping/strict-pin setup and affinity hunks; exclude concurrent R17 `ApplyGoalTurnFilter` call.
8. `middleware/access_token_routes.go`: only `GET /pg/channels: accessTokenSessionRule`; the existing dashboard route catalog test passes. Exclude concurrent R18 rotation route.

Frontend exclusive files:

1. `web/src/components/model-group-selector.tsx`
2. `web/src/components/model-group-selector/__tests__/sorting.test.tsx`
3. `web/src/features/playground/api.ts`
4. `web/src/features/playground/hooks/use-playground-options.ts`
5. `web/src/features/playground/hooks/use-playground-state.ts`
6. `web/src/features/playground/hooks/__tests__/use-playground-options.test.tsx` (new)
7. `web/src/features/playground/hooks/__tests__/use-playground-state.test.ts`
8. `web/src/features/playground/components/input/playground-input.tsx`
9. `web/src/features/playground/components/input/playground-input-controls.tsx`
10. `web/src/features/playground/index.tsx`

Frontend shared hunks:

- `web/src/features/playground/types.ts`: the two `channel_id?: number` additions only.
- `web/src/features/playground/lib/streaming/payload-builder.ts`: the `config.channel_id` forwarding block only.

Root-owned translations pending in all seven locales:

- `Model navigation`
- `No channels available`
- `Failed to load playground channels`
- `Selected channel is unavailable. Choose another channel or return to groups.`

No commits/staging performed by this reviewer. All R2 files released. Root still owns final integration/browser verification and real-database CI acceptance.

## Administrator visibility follow-up — 2026-11-20

- Confirmed the existing `GET /pg/channels` route, `PlaygroundChannels` handler and distributor pin boundary already require an administrator role plus `ChannelRead`; PAT rejection and effective group/model/key checks remain intact. No duplicate production backend guards were added.
- Shared the existing frontend role-plus-permission predicate between options and state. Channel queries are isolated by user, role and permission and consume the query cancellation signal. Non-administrators receive no channel options or channel list requests, including when custom grants or cached metadata exist.
- The state owner clears an ephemeral pin synchronously when the user, role or channel permission changes, before child rendering and request callbacks receive the next configuration. Non-administrators cannot assign a pin. Existing persisted-config parsing continues to exclude pins.
- Extended existing options/state tests for real selector visibility, cached metadata with a non-admin custom grant, administrator demotion, user/role/permission changes, no resurrection, restored/forged local pins and omission from the actual request payload. Existing server regression now covers an ordinary user's out-of-group list/pin forgery and verifies denied list responses expose no channel metadata.
- Security review followed OWASP Authentication, Session Management and Authorization Cheat Sheets, especially deny-by-default and server-side permission checks on every request. This focused review is not a claim of full ASVS compliance.
- Only targeted formatting ran locally. Tests, typecheck, lint, build, browser verification and database matrix execution were not run locally, per the user's instruction. Cloud CI must run the existing playground options/state and `TestPlaygroundChannelNavigationAuthorizationAndRouting` suites (including configured SQLite/MySQL/PostgreSQL jobs), plus frontend typecheck/lint.
- No new locale keys, production database changes or independent-module changes. No staging, commit or push was performed by this worker.
