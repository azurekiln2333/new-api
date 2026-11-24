# Source-only findings, 2026-11-24
No local executable validation performed.
- fingerprint.tsx:250 calls /api/channel/search p1 page_size30. model/channel.go:1001-1030 unbounded Find; controller/channel.go:469-485 in-memory slice; channel_key_entry.go:215 AfterFind and :135 per-channel entry query. N matched channels =>1+N reads even to show30.
- normal list SQL pages, but hooks add per-page N. channel_cache.go:156 global reload mutex wraps all reads, so save can wait behind and repeat another full reload.
- controller/channel.go:1498 save ->1503 InitChannelCache ->1535 audit ->JSON. CacheUpdateChannel alone does not rebuild group routing.
- invitation model:33-40 bulk transaction then controller:65 audit. redemption controller:108-127 loops inserts. Neither creation request is idempotent. Existing redemption batch ID has claim-limit meaning, not request-id meaning.
- audit chain controller/audit.go:158 -> model/log.go:315 -> GetUsernameById/GetUserCache -> model/audit_log.go:134 LOG_DB.Create; actor name retrieval repeats Redis/version/database work.
- channel drawer:1735-1744, invitation index:317-320 and redemption drawer:199-219 close after response without waiting refetch. Fix backend response critical path; do not falsely blame frontend invalidation.
- code tables already have current-page selection. Invitation creation exports returned keys, but history lacks recoverable create-batch export. Redemption created-result export is hidden behind Save as file; selected rows lack download.
- card uses full timestamp; table already has formatRelativeTime. Budget quota uses formatQuotaWithCurrency and service remaining/limit; actual Tokens are separate usage total. Never fabricate total from upstream balance plus billed usage.
- FingerprintProbe needs actual Prompt/Output; challenge and probe result have strings, snapshot struct copy safe.
- API Info current DTO id/url/route/description/color; dashboard lg:grid-cols-2 makes it narrow. system_instance samples30s, stale90s, resource validity absent when monitoring disabled.
- homepage public-header desktop122/mobile317 ProfileDropdown; console app-header149 separate. Overview guide controls635/650/734 lack !isAdmin.
- common logs current order can be changed at definition; no persisted column order, only visibility/sizing.
