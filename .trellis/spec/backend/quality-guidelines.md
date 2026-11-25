# Quality Guidelines

## Asynchronous quota work in request fixtures

### 1. Scope / trigger

Apply when a controller test restores global database/Redis state after a request
that can asynchronously refund reserved wallet and token quota.

### 2. Signatures

`model.IncreaseUserQuota(id, quota, db)` and `DecreaseUserQuota(id, quota, db)`
schedule quota-cache work only when `common.RedisEnabled` is true. Retain their
existing inner cache guards, SQL ordering and asynchronous enabled-cache behavior.
`BillingSession.Refund` schedules wallet refund before token refund; handler
completion and its `refunded` flag are not completion signals for that work.

### 3. Contracts

The `TestChannelWaitRouting` fixture disables Redis, batching and unrelated quota
warnings. Capture its DB/LOG_DB handles. Await both exact persisted wallet and
token targets before asserting the remaining accounting invariants or restoring
fixture globals. Under these conditions the token refund SQL follows wallet SQL
and its disabled scheduling guard; no nested quota-cache worker is created.
This is a fixture-specific boundary, not a general Redis-enabled completion API.

### 4. Validation and error matrix

| Observation | Meaning |
| --- | --- |
| HTTP handler finished | Refund may only have been queued |
| Wallet restored, token still reserved | Refund is incomplete |
| Both exact SQL balances restored, Redis/batching disabled | The fixture-sensitive refund quota/cache work has finished |
| Redis enabled | SQL balances alone do not fence asynchronous cache work |
| Early assertion/request failure | Cancel and close the gateway, observe handler completion, then await accounting before LIFO cleanup restores globals |

### 5. Good / base / bad cases

Good: assert a full refund for a rejected request and exactly one charge for a
successful retry. Base: a canceled admission wait never reserves quota. Bad:
sleep, weaken expected balances, or treat global pool counts as request completion.

### 6. Tests required

Keep `TestChannelWaitRouting`'s exact `tc.wantCharge` targets, consume-log count,
one reservation across retries and capacity-release assertions. Its last-registered
cleanup may derive the charge from committed consume logs solely to handle an
early cancellation; it must not replace normal expectations. Use bounded query
contexts and observable-state polling. Verify the existing real-dialect and race
selectors in hosted CI; source review does not establish execution success.

### 7. Wrong vs correct

Wrong: `wallet == original` proves the refund and all cache tasks finished.
Correct: trace every nested task, avoid scheduling disabled cache work, and wait
for the exact wallet/token outcome under the explicit fixture conditions.

> Code quality standards for backend development.

---

## Overview

<!--
Document your project's quality standards here.

Questions to answer:
- What patterns are forbidden?
- What linting rules do you enforce?
- What are your testing requirements?
- What code review standards apply?
-->

(To be filled by the team)

---

## Forbidden Patterns

<!-- Patterns that should never be used and why -->

(To be filled by the team)

---

## Required Patterns

<!-- Patterns that must always be used -->

(To be filled by the team)

---

## Testing Requirements

<!-- What level of testing is expected -->

(To be filled by the team)

---

## Code Review Checklist

<!-- What reviewers should check -->

(To be filled by the team)
