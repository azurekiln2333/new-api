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

Card metrics have two wrapping columns: used/remaining balance and used tokens
on the left; response time, the full last-tested date/time and shared concurrency
on the right. Priority and weight share a separate bottom row. Use description
lists with direct dt/dd pairs inside metric groups. Narrow cards may stack the
columns and split the date from the time, but must not truncate either. Retain
the priority event target and accessible card/name actions.

`ChannelUsedTokens` uses `compact` only in card layout: call the shared
`formatCompactNumber` with a normalized Intl locale to display Chinese 万/亿
on one line, without a badge frame. Its keyboard-focusable tooltip uses
`formatTokens` to show the exact integer followed by parenthesized compact
values. The table and details retain the full value. Do not change global
formatter defaults or add one polling request per card; reuse usage and
concurrency contexts, including privacy masking and aggregate rows.

Extend the existing card-entry and channel-usage tests when changing this
contract: exact/compact values for card versus table, full timestamp text,
privacy placeholders and nested control activation remain observable behavior.
