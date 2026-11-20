# Research: LinuxDO avatar sync on successful login

Scope: research only, no product edits. Requirement: on a successful LinuxDO login, sync the provider avatar; must not overwrite a custom avatar unless the current policy says the avatar source is the provider; avatar fetch/validation failure must not block login; trusted URL, cache and no SSRF. Trigger is login only (user-specified).

## 1. Current behavior (known, verified in this checkout)

### Provider-only avatar policy already exists and is reusable

- `oauth/provider.go:44-51`:
  ```go
  // AvatarProvider is implemented by providers whose user info carries a profile
  // picture. Only these providers take part in the login avatar sync: for the
  // others an absent picture is not evidence that the stored avatar is gone.
  type AvatarProvider interface {
      SupportsAvatar() bool
  }
  ```
- Implemented only by `GenericOAuthProvider` (`oauth/generic.go:301`) and `OIDCProvider` (`oauth/oidc.go:161`).
- `oauth/avatar.go`:
  - `AvatarURLMaxLength = 1024`.
  - `NormalizeAvatarURL(raw)` — trims, rejects >1024, rejects control chars, requires absolute `https` with a host, rejects opaque URLs and embedded userinfo. Returns `""` for unusable input.
  - `SyncUserAvatar(ctx, user, provider, avatarURL)` — no-op for nil/unsaved user; **no-op unless the provider implements `AvatarProvider`**; logs and keeps the current avatar when a non-empty claim is unusable; clears the stored avatar only when the claim is empty; no-op when unchanged; DB read/write failures only log. Returns nothing, so it cannot fail the login.
- Storage/ownership of the field:
  - `relaykit/dto/user_settings.go:21` `AvatarUrl string \`json:"avatar_url,omitempty"\`` with the comment that it is synced from the provider at OAuth/OIDC login and **the user cannot modify it**.
  - `model/user.go`: `SetSetting` :176, `UpdateUserSetting` :185, `GetUserSetting(id, fromDB)` :1303 (Redis cache on read).
  - `controller/user.go:534` exposes `avatar_url` in self-user data; `PUT /api/user/setting` does not accept it.
  - Regression test `controller/auth_flow_test.go:1325-1336` `TestUserSettingsPreserveProviderAvatar`: a request body containing `avatar_url: https://untrusted.example/avatar.png` is ignored, the provider value and the `language` field are preserved, and `buildSelfUserData` still reports the provider URL. **This is the enforcement of "provider source only".**
- Login call site: `controller/oauth.go:396`, inside `handleOAuthLogin` after `findOrCreateOAuthUser` and before the status check:
  ```go
  oauth.SyncUserAvatar(c.Request.Context(), user, provider, oauthUser.AvatarURL)
  ```
  The bind flow (`handleOAuthBind`, `controller/oauth.go:429+`) does not call it, so the current trigger is login-only, matching the requirement.
- Frontend: `web/src/stores/auth-store.ts:54-55` `avatar_url?: string` ("Provider avatar address synced on OAuth/OIDC login"); `web/src/components/user-avatar.tsx` + `web/src/lib/avatar.ts` `resolveUserAvatarUrl` (https-only, ignores unusable values) with tests `web/src/components/__tests__/user-avatar.test.tsx` and `web/src/lib/__tests__/avatar.test.ts`.

### LinuxDO gap (known)

- `oauth/linuxdo.go` `LinuxDOProvider` does **not** implement `SupportsAvatar()`, so `SyncUserAvatar` returns at the `AvatarProvider` type assertion and **no avatar is synced today**.
- `linuxdoUser` (`oauth/linuxdo.go:41-49`) decodes only `id, username, name, active, trust_level, silenced`. `GetUserInfo` returns `OAuthUser{ProviderUserID, Username, DisplayName, Extra: {trust_level, active, silenced}}` — `AvatarURL` is never populated.
- No test covers LinuxDO avatar behavior; `oauth/avatar_test.go:21-150` covers normalization, store/keep/clear/idempotency, and `SupportsAvatar` for OIDC and generic only.

## 2. Unverified prerequisite (do not invent a field name)

**Blocker:** the avatar claim shape of the LinuxDO userinfo endpoint is not present anywhere in this repository and has not been observed in this session. The endpoint defaults to `https://connect.linux.do/api/user` (`oauth/linuxdo.go`, overridable via `LINUX_DO_USER_ENDPOINT`), and the response struct above proves only which fields the gateway currently reads — not which fields the API returns.

Required before implementation:
1. Capture one real userinfo response (or the provider's documented schema) and confirm the avatar field name, type, and whether it is a relative template or an absolute URL.
2. Confirm the value is `https` and stable (CDN host), and whether it can be absent vs present-but-empty.
3. If the field is a relative path/template, decide the allowed base host explicitly; do not resolve against an arbitrary provider-supplied host.

Until this is verified, the plan must not hard-code `avatar_url` (or `avatar_template`) as a decoded field.

## 3. Required behavior for the change (design, not implemented)

1. `LinuxDOProvider` implements `AvatarProvider` (`SupportsAvatar() bool { return true }`) **only after** the claim shape is verified, and `GetUserInfo` maps the verified claim into `OAuthUser.AvatarURL`.
2. Reuse `oauth.NormalizeAvatarURL` and `oauth.SyncUserAvatar` unchanged; no second validator, no server-side download.
3. Absent vs empty: distinguish "field absent" from "field present and empty" (pointer/`json.RawMessage` presence flag). Absence must not clear a stored avatar; only a verified present-and-empty claim may, per the existing `SyncUserAvatar` comment contract. This is the one behavior worth adjusting for LinuxDO if the API omits the field for users without an avatar.
4. Custom avatar protection: the existing policy is provider-only and user writes are already rejected (`TestUserSettingsPreserveProviderAvatar`). Therefore provider overwrite is consistent with current policy; no new `avatar_source` column or user-editable avatar is needed. Do not add one.
5. Failure isolation: `SyncUserAvatar` returns nothing and only logs; keep that contract. Any decode/validation error in `GetUserInfo` for the avatar must be non-fatal — a malformed or missing avatar must not turn a valid login into an error.
6. SSRF: current design is store-only — the gateway never fetches the avatar; the browser loads the validated `https` URL. No new SSRF surface. If a proxy/cache fetch is ever added, it must use an allowlisted host, block private/link-local ranges, cap redirects and size, and set a timeout — out of scope here.
7. Cache: the avatar lives in the user setting (`GetUserSetting` uses Redis). Verify that `UpdateUserSetting` updates/invalidates the Redis user-setting cache so the new avatar is visible on the next request; if it only writes the DB, an explicit cache refresh is required. (Not fully verified in this session.)
8. Trigger: keep the single login call site `controller/oauth.go:396`. Do not sync on bind, refresh, or profile read.

## 4. Tests to extend (no new files for the same feature)

- `oauth/avatar_test.go`: LinuxDO provider returns `SupportsAvatar() == true`; claim mapped into `OAuthUser.AvatarURL`; unusable claim keeps the previous avatar; absent claim does not clear; login continues when the claim is malformed.
- `controller/auth_flow_test.go`: one case that a LinuxDO login with a failing avatar read/write still completes login (session issued), extending the existing OAuth flow test file rather than adding a new one.

## 5. Default choice

Reuse the existing provider-only avatar policy: LinuxDO becomes an `AvatarProvider`, `handleOAuthLogin` keeps calling `SyncUserAvatar`, the stored value is validated by `NormalizeAvatarURL`, and every avatar failure is logged and ignored. No user-editable avatar and no `avatar_source` field.

## 6. Factual blockers

- LinuxDO userinfo avatar claim shape unverified (prerequisite above); do not invent `avatar_url`.
- Whether the claim is ever present-but-empty vs absent (drives the clear-vs-keep branch).
- `UpdateUserSetting` Redis cache behavior not verified.
- Whether the deployed instance overrides `LINUX_DO_USER_ENDPOINT` (unknown; do not assume the default host).

## 7. Provider documentation addendum — 2026-11-20

This addendum supersedes the **field-name** portion of the prerequisite in sections 2 and 6; it does not establish every URL/template or empty-value behavior.

### Verified primary documentation

The source repository for the Linux DO community wiki is `chenyme/LINUX-DO-WIKI`; its GitHub repository metadata declares `https://wiki.linux.do` as its homepage. Inspected source commit: **`26cd5730d92b5c250bddf75c154c33fe371939c7`**, committed **2026-04-29T15:07:16Z**.

In `content/Community/LinuxDoConnect.mdx`, the user-information field table explicitly documents:

- Line 24: **`avatar_template`**, described as a user avatar template URL supporting multiple sizes.
- Lines 21-28: the surrounding `id`, `username`, `name`, `active`, `trust_level`, `silenced`, and `external_ids` fields.
- Line 42: the user-information endpoint is **`https://connect.linux.do/api/user`**.

Pinned primary source: https://github.com/chenyme/LINUX-DO-WIKI/blob/26cd5730d92b5c250bddf75c154c33fe371939c7/content/Community/LinuxDoConnect.mdx#L18-L42

Public documentation page: https://wiki.linux.do/Community/LinuxDoConnect

Thus the integration should investigate/decode **`avatar_template`**, not invent an `avatar_url` claim or an extra username-based API request. No access token or login was used for this verification.

### What the retrieved document does not establish

- It does not supply a concrete avatar value/schema proving absolute HTTPS versus relative paths, the exact size placeholder syntax, or all allowed CDN hosts.
- It does not define absent, null, and empty-string semantics. None should be treated as a verified provider command to delete the existing avatar based on this document alone.
- It does not prove the deployed instance uses the default userinfo endpoint rather than `LINUX_DO_USER_ENDPOINT`.

Direct unauthenticated fetches of the wiki website, Connect homepage, and forum JSON returned Cloudflare challenge pages; the documentation source above was retrieved successfully through GitHub. A previously considered forum URL example was **not retrieved/verified in this session and must not be used as evidence**.

### Supported implementation boundary

Use presence-aware decoding of the documented `avatar_template` claim. An already absolute, usable HTTPS URL may go through the existing validator and login-only synchronization. Preserve the current avatar on absent/null/empty/malformed/unresolved-template values until deletion semantics and template resolution are explicitly verified. Avatar-specific shape errors must not fail otherwise valid login. Do not build a guessed avatar URL from `username`, resolve arbitrary paths against a user-controlled host, or add a network download simply to fill this gap.

For the final normalizer, obtain a sanitized provider fixture or source-level schema covering a real default/custom avatar and its size template. This can be supplied by a test-owned provider response; do not capture access tokens, API keys, or full sensitive userinfo payloads in fixtures/logs. Tests should cover a valid absolute HTTPS claim, absent/null/empty/wrong-type claim preserving an existing avatar, unsafe URLs, and template handling only once that syntax is evidenced.
