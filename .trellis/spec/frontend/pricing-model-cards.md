# Pricing model card presentation

## Scope and components

`ModelCard` composes existing pricing summaries, `CopyButton`, Details and
`ModelBillingModeBadge`. Its order is model identity, actual prices, explicit
Details/Copy actions, bounded description/tags, supplied metadata, then
`ModelPerfBadge`. Preserve all existing price calculations, units, group/recharge
inputs, Details callbacks and Copy behavior when rearranging presentation.

## Optional metadata

Render only provided vendor, billing, endpoint, group and context information.
`context_length` is optional and is not supplied by the current Go pricing
response. Show context only for a finite positive supplied value, formatted
through `formatCompactNumber` with `toIntlLocale`; never invent a model limit.

## Performance contract

`ModelPerfBadgeData` supplies average latency, average throughput, success rate,
optional window bounds and `recent_success_series`. The pricing grid requests
recent-model data: the backend can include up to 24 nonempty hourly samples
within seven days and extend model aggregates to the oldest returned sample.
Use the neutral translated `Performance` label; do not describe this as a fixed
last-24-hours window.

Latency/throughput must be finite and positive to display a measurement. Success
rate must be finite and in 0–100, with zero a valid measurement. Otherwise show
unknown values. Sort an allocated valid sample list and retain at most 24 actual
samples; do not pad missing hours or fabricate availability. Reuse `LearnMore`
for the existing success-rate caveat about excluded business rejections and the
current partial hour.

## Observable regressions

Extend the existing `pricing/__tests__/model-cards.test.tsx` suite for section
order, unchanged Details/Copy behavior and pricing, conditional context,
unknown/zero/invalid metrics, and the accessible caveat. An older-than-24-hour
sample must remain visible with its real metrics under the neutral label.
Source review does not replace hosted typecheck, lint, formatting and tests.
