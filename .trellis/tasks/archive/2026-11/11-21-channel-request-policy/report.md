# Integration checkpoint

Status: complete. Implementation, independent review and exact-SHA cloud
verification passed; fork/main contains the validated commit. Release run
44824384740 completed successfully and published custom-20261121-de5c967.
Do not read earlier worker WIP sections as the final task status.

## Implemented

- Shared fixed-channel diagnostic admission, current policy after waiting,
  supported endpoint conversion and exactly-once known-usage settlement.
- Fingerprint default-three randomized samples with refreshed policy per sample.
- Specialized management transport, static headers, enabled credentials, total
  deadlines, cancellation and response-body-owned capacity.
- Balance/pricing/inference/Wham/Ollama requests use applicable saved policy.
- Complete enabled-key authoritative model inventories; explicit per-key sync
  remains add-only partial-success recovery.
- Saved previews preserve explicit form overlays and current saved capacity/TLS.
- Guarded model/balance/OAuth persistence preserves administrator replacements,
  current routing and scheduling metadata.
- Fixed management errors use existing translated categories; arbitrary upstream
  details remain unchanged.

## Last independent review

Controller/discovery review and independent Codex service audit found no
remaining source blocker. Two final review findings were corrected: explicit
sync preserves current ChannelInfo scheduling state, and model Apply reloads
the persisted row before rebuilding abilities. Existing regression suites were
extended without local execution.

## Cloud evidence

Earlier green checkpoints are recorded in implement.md. Run 44817543281 at
24f607376 passed backend/frontend and the database selectors but failed its
final race stage because the balance fixture lacked i18n initialization. The
fixture was fixed in 3e59f70ec. That failure is not a product completion pass.

593c0976f contains the final model-management implementation; ab2cf1b36 and
f183380b7 expand external database and race selectors. Run 44820886185 exposed
two obsolete expectations: a missing inventory was treated as empty success,
and a TLS fixture changed only memory instead of the saved policy. Tests were
corrected in 3d05b13b5 and de5c96736 without relaxing production validation.

Final integration SHA: de5c96736f24968a81e62dee8b9960f360915ed1.
Cloud run 44822062923 passed all three jobs:

- Backend vet/build and complete tests for root, independently built relaykit
  and independently built tokenkit (GOWORK=off).
- Frontend frozen dependency install, typecheck, scoped lint, complete tests and
  production build.
- Complete model/service/controller database contracts, explicit dialect
  regression selectors, Redis integration and the admission/waiting/streaming/
  management/multikey/fingerprint race gate.

Recorded engines: SQLite 3.50.4, MySQL 8.4.13, PostgreSQL 16.16 and Redis 7.4.12.
Commands are the committed .github/workflows/ci.yml steps: go vet ./...,
GOWORK=off go build ./... in each module, make test, go test -p 1 -count=1 -v
./model ./service ./controller, explicit TEST_CHANNEL_SQL_DSN /
TEST_TASK_DB_DIALECT selectors for the external engines, and the full committed
go test -race selector. Frontend uses bun check -b, scoped bun x oxlint,
bun run test and bun run build. Raw integration evidence is retained in the
GitHub job 136585294774 logs. No database compatibility claim is based on mocks
or source review alone. No schema/migration change was made.

Root refetched fork/main, verified ancestry, pushed the exact validated SHA and
read back both remote branch heads. Main and merge/upstream both pointed to
the validated SHA when release run 44824384740 started.

## Release verification

Release run 44824384740 completed every job successfully: all three repeated
validation jobs, both architecture image builds, multi-platform publication,
Windows/macOS/Linux downloads and final release publication. Release 453230382
was published at 2026-11-21 06:19:21 Asia/Shanghai with draft=false and
prerelease=false. The actual Git tag custom-20261121-de5c967 and fork/main both
resolve to de5c96736f24968a81e62dee8b9960f360915ed1.

An independent read-only GHCR review at 2026-11-21 06:16:03 Asia/Shanghai
verified that custom-20261121-de5c967,
sha-de5c96736f24968a81e62dee8b9960f360915ed1 and latest all resolve to index
sha256:25a429d73f8be083058025a9976ba235387e534f4b2b47748b2d9a145849f942.
The linux/amd64 and linux/arm64 image configurations both identify revision
de5c96736f24968a81e62dee8b9960f360915ed1 and the expected OS/architecture.
No image was pulled or executed locally.

Root independently verified all seven uploaded, nonempty release assets:

- new-api-custom-20261121-de5c967
- new-api-arm64-custom-20261121-de5c967
- new-api-macos-custom-20261121-de5c967
- new-api-custom-20261121-de5c967.exe
- checksums-linux.txt
- checksums-macos.txt
- checksums-windows.txt

Each of the four binary checksum entries matches the corresponding GitHub
asset SHA-256 digest. Only the three small checksum files were downloaded; no
binary was downloaded, executed or locally built. Release/tag/image checks
used read-only API requests. Server pull/deployment was not inspected, as
explicitly requested by the user.

The task/archive/journal closeout commits follow the released SHA on
merge/upstream only. They do not change product code or move the verified
main/release tag, and do not trigger another main release.

## Boundaries

No local tests, builds, lint, typecheck, benchmark, browser verification or paid
upstream probes were run. No server/SSH verification is requested. Ordinary
API-origin async task polling retains its separate task-bound lifecycle and is
not claimed as newly covered console management behavior. No schema or
dependency upgrade was introduced.
