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

Card metrics use three wrapping rows: used balance with tokens, priority with
weight, and response time with last-tested time. Use a description list for
each row, with direct dt/dd pairs inside its metric groups. Normal-width cards
keep paired values side by side; narrow cards wrap without truncating controls.
Retain the priority event target and accessible card/name actions when changing
the layout. Shared cells remain the source of values and formatting.
