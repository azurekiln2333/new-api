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

The card header starts with the existing channel name renderer after optional
selection/reorder controls, with ID on the second line. Do not render the type
badge, provider logo or an empty type spacer in cards; table type cells remain.
Place the shared concurrency `current / limit` display and status/action icons next, then used
Tokens, billed used/total quota, response time and last test. Priority and weight
share the final row. The last-test value uses the existing relative-time
formatter (for example, minutes or days ago); the exact timestamp may remain
in its tooltip, but is not the inline value. This supersedes the earlier full
inline timestamp requirement by the user decision of 2026-11-24. Use direct
dt/dd pairs for metrics and preserve keyboard activation and responsive wrapping.
Response and Last Tested labels and values must remain fully readable in narrow
cards and longer translations; allow wrapping instead of clipping the text.

The existing quota region always keeps billed `channel.used_quota` as the
numerator, formatted by the shared currency/quota formatter. Its denominator
is the configured local `limit_quota` only when both the ledger and budget are
enabled; a known ledger-off, disabled or unconfigured budget displays 0. For example, used 125 with no enabled
budget shows `125 / 0`, and with limit 500 shows `125 / 500`, in the selected
display unit. Never substitute `remaining_quota` or period usage for the billed
numerator. Loading or failed budget reads keep the known numerator and an honest
unknown denominator, not a fabricated 0. Privacy masking and aggregate-row
semantics remain. Reuse the page budget context, preserve budget details and
upstream balance details, and never infer a total by adding upstream balance to
local usage. This supersedes the remaining/total card contract on 2026-11-26.

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
height. Expanded log cards, collapsed mobile rows and token breakdowns show
plain exact counts. Mobile rows/cards lead with a wrapping model title, then
stream status and real available TPS; Input/Output are separately labeled
secondary values rather than one abbreviated sentence. Reuse `StreamTpsCell`
with `compact={tokensPerSecond == null}` to retain shared rounding for both
streaming modes and omit unavailable throughput. Keep the stream/error control
outside the collapsed row's expansion Button. Desktop columns remain unchanged.
Cover these contracts in the existing usage-log mobile-card/table-cell fixture.

Mobile common-log summary badges use one compact nowrap row with exact values
and horizontal overflow for extreme counts. The cache badge uses the existing
Zap icon plus percentage and shared `QuotaDetailsPopover` for a fully named
tap/keyboard explanation; desktop retains its full label and spacing. Match-mode
selection uses the actual Base UI `aria-pressed` state with unambiguous theme
colors; Contains remains fuzzy and Exact remains exact in URL/API semantics.

Do not apply the channel exact-plus-abbreviation formatter to other features.
Dashboard summaries and user rankings keep their prior compact displays;
distribution chart tooltips keep plain exact counts. Rankings retain their
fixed K/M/B/T precision through `formatRankingTokens`, with exact cache counts
in titles. User balances and the separately sortable Total Used column reuse
`formatQuotaWithCurrency`, including token display mode. The fingerprint page
uses plain `formatNumber` counts for usage, budgets and output limits. Keep
normalized Intl locales at these call sites. Outside its own tests, shared
`formatTokens` is used only by channel metrics.

The priority-guide demo regression retains exact playback boundaries and full
dialog removal after dismissal. Under fake timers, await asynchronous advancement
inside `act` for the existing closed interval: Base UI completes close after an
animation frame and a Promise continuation, even when `getAnimations` returns an
empty array. Do not increase the interval, disable animations or replace DOM
absence with a weaker closed-state assertion. Reopening must reset the demo.
