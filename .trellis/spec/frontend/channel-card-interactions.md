# Channel card keyboard activation

Channel cards expose a focusable body with `role="button"` and
`data-activates-channel-details`. `ChannelsTable` delegates keyboard events
from the card region. Activate details only when the event target itself is
that body and the event has not already been prevented. Do not find an
activating ancestor with `closest`: Enter from an inline priority editor
would then save the value and open details simultaneously.

Keep explicit name/detail buttons usable. Regression coverage in
`channel-details-entry.test.tsx` must verify priority Enter saves without
opening details, focused card Enter/Space opens details, and explicit name
activation still works.

Keep the channel name and ID on their existing two lines. Place the shared
concurrency `current / limit` display and status/action icons next, then used
Tokens, remaining/total quota, response time and last test. Priority and weight
share the final row. The last-test value uses the existing relative-time
formatter (for example, minutes or days ago); the exact timestamp may remain
in its tooltip, but is not the inline value. This supersedes the earlier full
inline timestamp requirement by the user decision of 2026-11-24. Use direct
dt/dd pairs for metrics and preserve keyboard activation and responsive wrapping.

An enabled local channel budget occupies the existing remaining/total quota
region, using server `remaining_quota` and `limit_quota` with the shared quota
formatter. Do not add a separate local-budget row or combine upstream balance
with local usage to invent a total. Actual used Tokens still come from the
channel usage aggregation. Keep ledger-inactive, loading and unavailable states
distinct from unlimited capacity, preserve privacy masking, and retain the
budget details for used/reserved amounts and the upstream balance details.

`ChannelUsedTokens` uses `compact` only in card layout: call the shared
`formatCompactNumber` with a normalized Intl locale to display Chinese 万/亿
on one line, without a badge frame. Its keyboard-focusable tooltip uses
`formatTokens` to show the exact integer followed by parenthesized compact
values. The table and details retain the full value. Do not change global
formatter defaults or add one polling request per card; reuse usage and
concurrency contexts, including privacy masking and aggregate rows.

Extend the existing card-entry and channel-usage tests when changing this
contract: exact/compact values for card versus table, relative last-test text,
privacy placeholders and nested control activation remain observable behavior.

This formatting contract is scoped to channel metrics. Usage-log table cells
use `formatNumber` with the normalized locale: `41,258 / 115` and
`Cache↓ 41,088`, without parenthesized abbreviations. Keep the token cell
`whitespace-nowrap` and its cache line unwrapped so counts do not inflate row
height. Expanded log cards and token breakdowns also show plain exact counts;
the existing collapsed mobile rows retain their compact summaries. Cover the
desktop example in the existing usage-log mobile-card/table-cell fixture.

Do not apply the channel exact-plus-abbreviation formatter to other features.
Dashboard summaries and user rankings keep their prior compact displays;
distribution chart tooltips keep plain exact counts. Rankings retain their
fixed K/M/B/T precision through `formatRankingTokens`, with exact cache counts
in titles. User balances and the separately sortable Total Used column reuse
`formatQuotaWithCurrency`, including token display mode. The fingerprint page
uses plain `formatNumber` counts for usage, budgets and output limits. Keep
normalized Intl locales at these call sites. Outside its own tests, shared
`formatTokens` is used only by channel metrics.
