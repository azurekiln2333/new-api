# Execution plan

1. [x] Recover audit; inspect existing four idle pi workers in new-api workspace w4C.
2. [x] Record PRD/design, file ownership and user authorization to implement after planning.
3. [ ] Activate task; send each pi its task brief and confirm actual work begins.
4. [ ] Pi 1: diagnostic endpoint/request policy and narrow endpoint UI regressions.
5. [ ] Pi 2: shared fixed-channel admission contract; fingerprint freshness/defaults; coordinate pi 1 integration.
6. [ ] Pi 3: discovery/balance/header/settings/model union; integrate pi 4 Ollama controller requirements.
7. [ ] Pi 4: price/inference/Ollama connections and admission; publish shared helper contract.
8. [ ] Root reviews every changed path for double lease, settlement, cancellation, recovery, specialized endpoints and current policy. Continue interrupted workers.
9. [ ] Update narrow executable spec without staging pre-existing dirty specs. Commit independent changes with hooks disabled.
10. [ ] Push fork/merge/upstream; inspect exact-SHA cloud CI and fix failures. After checks pass, integrate fork/main and publish unique release tag; verify cloud release/build only.

## Cloud validation only

Existing .github/workflows/ci.yml runs root/relaykit/tokenkit vet/build/test, frontend typecheck/test/lint/build, real SQLite/MySQL/PostgreSQL policy integration and Redis/race checks. Extend focused selectors as needed. Do not execute local tests, builds, lint, typecheck, benchmark or browser checks; source review and formatting allowed. No real paid upstream calls.

Workers report exact files, helper contracts, authored-but-unrun tests, risks and completion to worker-N.md. Root alone stages/commits/pushes. Preserve unrelated dirty files. Do not contact deployment servers or claim they pulled a version.
