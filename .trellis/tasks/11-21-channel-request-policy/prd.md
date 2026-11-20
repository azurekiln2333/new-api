# Unify channel policies for in-console upstream requests

## Goal

Make manual and scheduled tests, fingerprint detection and channel management requests honor applicable endpoint, concurrency, request and transport configuration; delegate four pi workers and validate in cloud.

## Requirements

- On 2026-11-21 the user requested planning followed by implementation by four existing pi workers. This authorizes task creation and activation without repeating approval.
- Ordinary, batch and scheduled tests must honor channel supported endpoints and actual directional protocol conversion capabilities, including Responses-only channels without a fingerprint prompt. Preserve absent-configuration compatibility.
- Generation diagnostics apply channel system prompts, disabled fields, parameter/header overrides, outbound/egress hooks, mapping and transport while settling usage once. Do not forward browser client headers.
- Share fixed-channel concurrency accounting, bounded cancellation-aware waiting and safe retries. Revalidate changed policy after waiting; never silently test another channel. Local admission and cancellation must not disable channels.
- Fingerprinting defaults to three randomized SDK challenges, retains explicit 1-3, and refreshes channel/key/model policy before each sample.
- Discovery, balance, price sync, inference and Ollama requests retain specialized endpoints and apply relevant channel transport, static headers, enabled keys and admission. Preserve unsaved channel settings; do not infer channel-wide model deletion from one key.
- Automatic endpoint UI follows channel policy using existing components.
- No local tests/build/lint/typecheck/browser verification or paid upstream probes. Use GitHub Actions. Root alone commits and pushes each independent change to fork; no server verification. Preserve unrelated dirty files and administrator diagnostic/recovery semantics.

## Acceptance Criteria

- [ ] Responses-only ordinary/scheduled tests reach Responses; unsupported directions/passthrough conflict fail safely.
- [ ] Upstream sees applicable prompt/field/parameter/plugin policy; no duplicate charging.
- [ ] Capacity 1 serializes pinned test work; cancellation and policy changes release or invalidate admission correctly.
- [ ] Fingerprint defaults to three samples and later samples respect current configuration.
- [ ] Management requests retain headers/transport/cleanup; multi-key discovery represents all applicable enabled keys safely.
- [ ] Focused regressions are authored and exact-SHA cloud checks pass before reporting completion.

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
