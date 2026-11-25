# Deployment runbook — console forwarding to us01 (DRAFT)

> Status: DRAFT for root review; **the final handoff in `../report.md` supersedes this draft**.
> Corrected by pi-fwd after reading actual NODE_NAME/config/forwarder semantics.
> Written from `design.md` §7 and §11 item 8 plus the
> env names actually parsed in `common/console_forward.go`. Not verified on real hosts.
> Placeholders `{{...}}` mark values that are not fixed in the design and MUST be
> replaced per deployment. No `docs/` file is created; this stays under the task dir.

Authority: active task `.trellis/tasks/11-25-console-forward-to-master`.
Deployment boundary: WireGuard is the encrypted transport. A public hop that is not
behind WireGuard is NOT acceptable.

## 0. Topology recap

```
browser ──https──> Cloudflare/reverse proxy ──> worker public ingress (us03, hk01, hk02, hk03)
                                                     │
                                                     │ internal HTTP over WireGuard
                                                     ▼
                                        us01 (master, co-located DB+Redis) :3000
```

- Each node keeps its own **public console domain**. The browser always talks to the
  worker domain; forwarding is invisible to the browser.
- Relay traffic (`/v1`, `/v1beta`, `/mj`, `/pg`, task protocol) stays on the worker.
  Only the `/api` prefix is forwarded (health `/api/status/test` stays local).
- **hk02 / hk03 cannot reach us01 directly.** Their `CONSOLE_FORWARD_MASTER_URL`
  points at an **hk01 WireGuard address** where hk01 runs an nginx `stream`
  TCP-forward to `us01:3000` (block in §6).

## 1. Environment variables (source of truth)

Parsed once at startup by `common.LoadConsoleForwardConfig` (all unset = today's
behavior). Malformed partial config fails startup.

| Variable | Master (us01) | Worker | Notes |
|---|---|---|---|
| `CONSOLE_FORWARD_MASTER_URL` | **must be unset** | required | `http://<internal-ip>:3000`, no path/query/userinfo |
| `CONSOLE_FORWARD_SECRET` | required | required | ≥32 bytes, identical on all nodes |
| `CONSOLE_FORWARD_TRUSTED_CIDRS` | required | required | master: socket peers it sees; workers: the internal peers allowed to present a signed envelope |
| `CONSOLE_FORWARD_PUBLIC_ORIGINS` | required | required | comma-separated exact **HTTPS** worker domains (both ends identical) |
| `CONSOLE_FORWARD_DIAL_TIMEOUT_MS` | optional | optional | default 2000, bounds 1–10000 |
| `CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS` | optional | optional | default 120, bounds 1–600 |
| `CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS` | optional | optional | default 300, bounds 1–3600 |

Fixed constants (not configurable, do NOT try to disable): proof version 1, 30s
skew, 60s nonce TTL, bounded entry/request-id 128 bytes, HMAC-SHA256.

## 2. Generate the shared secret (32+ random bytes)

Run once, copy the same value to every node's compose env:

```sh
openssl rand -hex 32      # 64 characters passed as the environment secret
# or
openssl rand -base64 48   # 64 base64 characters encoding 48 random bytes
```

Rules:
- Never commit it, never put it in a log line, never expose it via a status API.
- The secret authenticates **cluster membership only**; it is not a user credential.
  It must match exactly across us01 + all workers.

## 3. Placeholders used below

| Placeholder | Meaning |
|---|---|
| `{{US01_WG_IP}}` | us01 WireGuard address reachable by hk01/us03 (e.g. `10.100.2.1`) |
| `{{HK01_WG_IP}}` | hk01 WireGuard address reachable by hk02/hk03 |
| `{{US03_WG_IP}}` | us03 WireGuard address (seen by us01 as a socket peer) |
| `{{HK01_WG_IP_AS_SEEN_BY_US01}}` | hk01 WG source address that us01 sees as remote `RemoteAddr` |
| `{{DOMAIN_US03}}` | us03 public console domain |
| `{{DOMAIN_HK01}}` | hk01 public console domain |
| `{{DOMAIN_HK02}}` | hk02 public console domain |
| `{{DOMAIN_HK03}}` | hk03 public console domain |
| `{{DOMAIN_MAIN}}` | canonical main console domain (Cloudflare → an HK node) |

## 4. Per-node docker compose environment blocks

Add these under each service's `environment:` in the node's compose file. Values are
illustrative; replace placeholders.

### 4.1 us01 — master

Forwarding target **must be absent**. Master only verifies envelopes and never
forwards.

```yaml
services:
  new-api:
    environment:
      NODE_TYPE: master            # existing; makes common.IsMasterNode true
      NODE_NAME: us01              # common/node_identity.go reads NODE_NAME
      CONSOLE_FORWARD_SECRET: "{{SHARED_SECRET}}"
      # Socket peers us01 actually sees:
      #   hk01 (and therefore hk02/hk03, which arrive via hk01) + us03
      CONSOLE_FORWARD_TRUSTED_CIDRS: "{{HK01_WG_IP_AS_SEEN_BY_US01}}/32,{{US03_WG_IP}}/32"
      CONSOLE_FORWARD_PUBLIC_ORIGINS: "https://{{DOMAIN_US03}},https://{{DOMAIN_HK01}},https://{{DOMAIN_HK02}},https://{{DOMAIN_HK03}}"
      # CONSOLE_FORWARD_MASTER_URL: intentionally NOT set
      # Optional timeout overrides:
      # CONSOLE_FORWARD_DIAL_TIMEOUT_MS: "2000"
      # CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS: "120"
      # CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS: "300"
```

> If hk02/hk03 traffic is NAT'd differently on us01, add those observed source
> addresses here too. `TRUSTED_PROXIES` on us01 stays as today; do not widen it for
> forwarding.

### 4.2 us03 — worker (direct WireGuard path to us01)

```yaml
services:
  new-api:
    environment:
      NODE_TYPE: slave
      NODE_NAME: us03
      CONSOLE_FORWARD_MASTER_URL: "http://{{US01_WG_IP}}:3000"
      CONSOLE_FORWARD_SECRET: "{{SHARED_SECRET}}"
      CONSOLE_FORWARD_TRUSTED_CIDRS: "{{US01_WG_IP}}/32"
      CONSOLE_FORWARD_PUBLIC_ORIGINS: "https://{{DOMAIN_US03}},https://{{DOMAIN_HK01}},https://{{DOMAIN_HK02}},https://{{DOMAIN_HK03}}"
      # Narrow ingress trust for the local HTTPS-terminating proxy/CF edge:
      TRUSTED_PROXIES: "{{US03_INGRESS_PROXY_CIDR}}"
```

### 4.3 hk01 — worker (direct WireGuard path to us01, and TCP-stream host for hk02/hk03)

```yaml
services:
  new-api:
    environment:
      NODE_TYPE: slave
      NODE_NAME: hk01
      CONSOLE_FORWARD_MASTER_URL: "http://{{US01_WG_IP}}:3000"
      CONSOLE_FORWARD_SECRET: "{{SHARED_SECRET}}"
      CONSOLE_FORWARD_TRUSTED_CIDRS: "{{US01_WG_IP}}/32"
      CONSOLE_FORWARD_PUBLIC_ORIGINS: "https://{{DOMAIN_US03}},https://{{DOMAIN_HK01}},https://{{DOMAIN_HK02}},https://{{DOMAIN_HK03}}"
      TRUSTED_PROXIES: "{{HK01_INGRESS_PROXY_CIDR}}"

  # See §6 for the nginx stream proxy hk02/hk03 use to reach us01.
```

### 4.4 hk02 / hk03 — workers (target = hk01 WireGuard address, nginx stream → us01)

```yaml
services:
  new-api:
    environment:
      NODE_TYPE: slave
      NODE_NAME: hk02                  # or hk03
      # hk02/hk03 cannot reach us01 directly; target the hk01 TCP-stream hop.
      CONSOLE_FORWARD_MASTER_URL: "http://{{HK01_WG_IP}}:13000"
      CONSOLE_FORWARD_SECRET: "{{SHARED_SECRET}}"
      # These workers only ever connect to hk01, so trust hk01 as the internal peer.
      CONSOLE_FORWARD_TRUSTED_CIDRS: "{{HK01_WG_IP}}/32"
      CONSOLE_FORWARD_PUBLIC_ORIGINS: "https://{{DOMAIN_US03}},https://{{DOMAIN_HK01}},https://{{DOMAIN_HK02}},https://{{DOMAIN_HK03}}"
      TRUSTED_PROXIES: "{{HK02_OR_HK03_INGRESS_PROXY_CIDR}}"
```

> Port `13000` is a placeholder; keep it identical to the hk01 nginx `stream`
> `listen` port in §6. Worker `CONSOLE_FORWARD_TRUSTED_CIDRS` is an **inbound envelope
> peer allowlist**, not a target allowlist. The fixed private target is validated
> independently. The master sees hk02/hk03 traffic as hk01's socket address, so us01
> must trust the actual hk01 source rather than hk02/hk03's original addresses.
>
> Set narrow, explicit `TRUSTED_PROXIES` on every node, or `none` if it has no
> ingress proxy. Never rely on the broad private-range default on a client-reachable
> LAN/WireGuard socket. Entry proxies must overwrite untrusted forwarded IP/scheme
> headers. The application warns when forwarding is enabled with the default.
>
> Include `https://{{DOMAIN_MAIN}}` in every public-origin allowlist above (and in
> the secure-cookie/OAuth allowlists below) when the main domain aliases an HK node.

## 5. Secure cookies + OAuth allowed origins (recommended)

Forwarding works with `SESSION_COOKIE_SECURE` false or true (design §11 item 7). The
recommendation for production:

- **`SESSION_COOKIE_SECURE=true`** on every node, with
  **`SESSION_COOKIE_TRUSTED_URL`** listing **every** node's exact HTTPS origin:

```yaml
      SESSION_COOKIE_SECURE: "true"
      SESSION_COOKIE_TRUSTED_URL: "https://{{DOMAIN_US03}},https://{{DOMAIN_HK01}},https://{{DOMAIN_HK02}},https://{{DOMAIN_HK03}}"
```

- **OAuth `allowed_origins`** (admin → System Settings → OAuth) must list every
  public console domain, and each provider's registered callback URLs must include
  the `https://{{DOMAIN_*}}/oauth/{{provider}}` redirect for every domain the provider
  supports multiple URIs for.
- **Passkey**: set explicit `passkey.origins` / `passkey.rp_id` (authoritative; avoids
  the residual raw-header risk for direct requests recorded in `fwd-auth.md`).
  Distinct unrelated node domains do not share a passkey credential; configure
  supported RP IDs/origins or use the existing credential-domain selection.
- `ServerAddress` stays the **canonical public site**, never the internal master URL.

## 6. hk01 nginx stream proxy (for hk02 / hk03)

hk02/hk03 target hk01; hk01 TCP-streams to us01:3000. Add to hk01's nginx config
(`/etc/nginx/nginx.conf` top level, since `stream` is a top-level block):

```nginx
# /etc/nginx/conf.d/console-forward-stream.conf  (included from the top-level context)
stream {
    # hk02/hk03 CONSOLE_FORWARD_MASTER_URL points here.
    server {
        listen {{HK01_WG_IP}}:13000;
        proxy_pass {{US01_WG_IP}}:3000;

        proxy_connect_timeout 5s;
        proxy_timeout         300s;   # >= CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS
        proxy_socket_keepalive on;
    }
}
```

Reload: `nginx -t && systemctl reload nginx`.

> Keep `listen` bound to the WireGuard address only (never `0.0.0.0`). Because this
> is a raw TCP stream, us01 sees hk02/hk03 traffic as coming from hk01's WG address —
> this is exactly why us01's `CONSOLE_FORWARD_TRUSTED_CIDRS` lists hk01's address.

## 7. Rollout order (master first, then one worker, verify, then the rest)

1. **us01 (master) first.** Deploy its forwarding env (secret + trusted CIDRs +
   public origins, **no** target), restart, confirm it starts (no config error).
   Do not enable any worker yet — master with verification enabled is a no-op for
   today's traffic until workers start sending envelopes.
2. **One worker only — pick hk01** (or us03). Deploy its env, restart. Keep the other
   workers unset so they keep today's behavior.
3. **Verify** (§8): a console request on the chosen worker domain must be processed on
   us01 (`processing_node=us01`), and relay (`/v1`, `/pg`) must stay on the worker.
   Exercise login, refresh, logout, OAuth callback, passkey, and a channel save.
4. Only after step 3 is clean, **enable the remaining workers one at a time**
   (us03, then hk02, then hk03 — hk02/hk03 after the hk01 stream proxy in §6 is live).
   Re-run the §8 checks per node.
5. Roll out a single worker at a time and keep the previous worker's change revertible
   (§9) so a bad node can return to local execution without affecting the rest.

**Blocker before any rollout:** shared auth secrets and settings (session cookie
secret, `SERVER_ADDRESS`/`ServerAddress`, DB/Redis reachability from us01) must be
consistent across nodes, or local fallback auth will diverge.

## 8. How to verify forwarding works (log fields)

The forwarder and ingress verifier log bounded safe fields. Grep the node logs:

```sh
# On a worker (e.g. hk01): successful proxy response reports verified master identity
docker compose logs new-api | grep 'console forwarding complete'
# Expect: entry_node="hk01" processing_node="us01" forwarded=true request_id="..."
# Master logs use 'console ingress' with the same request id.

# On a successful proxy hop, the worker logs entry=HK, process=us01.
# On a pre-send fallback (master unreachable before any byte): processing stays local
docker compose logs new-api | grep 'console forwarding pre-send fallback'
# Expect: process=worker (local, correct but slow), class=dial

# Post-send failure becomes 502 and is never replayed:
docker compose logs new-api | grep 'console forwarding failed'
# Expect: class=post-send, and no local handler execution
```

Expected observable outcomes:
- Worker log: `entry_node=<worker> processing_node=us01 forwarded=true`.
- Master log: same `entry_node`, plus its own `processing_node=us01`.
- Request ID is the **same** on both nodes and in the response header.
- `/v1`, `/v1beta`, `/mj`, `/pg`, task-protocol and SPA/static never appear as
  forwarded — they run locally on the worker.
- A console write (e.g. channel save) succeeds and is visible cluster-wide via the
  existing sync path.

Quick negative checks:
- A public request with spoofed `X-New-Api-Console-*` headers has those markers
  stripped; it still follows normal worker `/api` forwarding (not forced local).
- Sending a **valid** signed envelope to a **worker** (not master) returns a loop
  guard error (508/502), never re-forwards.

## 9. Rollback

Fastest safe rollback (per node, no code change):

1. **Unset EVERY `CONSOLE_FORWARD_*` variable** on the worker and restart it.
   Unsetting only the target is invalid partial config and refuses startup.
   The node returns to local console execution (correct but slow); relay was never
   forwarded. Keep `NODE_TYPE`/`NODE_NAME`, ingress `TRUSTED_PROXIES` and auth secrets intact.
2. For hk02/hk03, also confirm the hk01 stream listener is no longer needed before
   removing it (only after hk02/hk03 forwarding is disabled).
3. **Roll back workers before removing the master verifier/secret.** Never leave a
   worker sending signed requests to a master with ingress verification disabled —
   that would give wrong client-IP/origin semantics.
4. Remove hk01's nginx `stream` block only after hk02/hk03 forwarding is off; restore
   prior firewall configuration through deployment change control.
5. No DB migration, data rollback or cache-format change is involved; the existing
   sync/publication path remains authoritative.

Full-cluster rollback order: disable hk02/hk03 → hk01 → us03 → then us01
(unset secret/CIDRs/origins on us01 last).

## 10. Open items for root to finalize before this is "final"

- Fill in every `{{...}}` placeholder from the real deployment inventory.
- Confirm the hk01 stream listen port and the exact us01-observed hk01 source IP.
- Decide whether to enable `SESSION_COOKIE_SECURE` during this rollout (recommended).
- Note the ≤30s signed-envelope window after a master restart (design §11 item 5):
  replay registry is in memory; authentication challenges remain single-use.
