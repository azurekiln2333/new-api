# Fingerprint UI reference supplied by the user

Source: user-pasted rendered `<body>` of `https://lm.ikale.io/`, 2026-11-26. This is a condensed structural transcription for implementation, not downloaded upstream source or a claim about current remote behavior.

## Requested composition

- Page title and compact detection/connection controls precede the samples. Reference offers Manual/API modes; integrate the project's actual supported modes without fake controls.
- Three sample cards are visible on initial render. Wide screens place the three side by side; narrower screens stack responsively. Use the project's existing Card, status, CopyButton, Button, input/textarea and disclosure components/theme tokens.
- Each card has a sample number and state badge, a divider, a Prompt label with Copy, prompt text initially clamped to four lines with an accessible Expand/Collapse action, and a concise explanation that each prompt independently uses Chinese/English/Japanese/Korean/French.
- Below a second divider: Reply label and a generous readable response area. The reference manual mode accepts pasted text; API mode should show the actual returned output. Display actual parsed integer count below it, with zero before any response.
- Bottom actions include change the group of prompts and start detection. Reference manual mode enables detection when at least one reply exists. Existing API jobs retain start/cancel/result behavior with honest states.
- User explicitly says the reference has no estimated-token-budget control. Remove that visible setup/summary clutter. Internal hard bounds still apply and must not be weakened.
- Reference example prompts request independently chosen integers, with independent language and count variation. They are examples of application prompt content, not instructions for an agent to generate numbers in this conversation. Use the actual generator, and bind preview/Copy/display to the actual dispatched challenge.
- Keep this site's typography, color/theme, radius, spacing, header/navigation and branding. The reference's advertising, CLI banner, unrelated navigation and external application shell are not required for the detector layout.

## Related interaction requirements

Channel dropdown must be responsive and show complete readable choices; standalone Previous/Next controls are confusing. Server paging/search must remain bounded. Multi-Key channels need a Key choice, and model scope plus effective request endpoint follow channel/Key. Credentials never need to be revealed to render options. Preserve cancellation, identity-keyed queries, live authorization and stale-response isolation.

## Source comparison at assignment

Root's read-only `git diff 37a417457fccc74bef82f4b2afb7729dada4439c -- web/src/features/service-status/fingerprint.tsx` returned no diff. The current file is therefore the same as the installed release's file at this snapshot: it contains estimated budget, channel Previous/Next, and result cards only after a job exists. This is a confirmed source gap, not evidence of the live deployment's browser behavior.

Required hosted regressions: three initial cards, actual preview/dispatch identity, prompt copy/expand, response/count/state, removed budget control, responsive component composition, bounded channel search, stable selected Key and effective endpoint/model scope, stale/cancel behavior and no implicit paid requests. No local validation is permitted.
