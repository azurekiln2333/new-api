# Security and validation baseline

Planning record, 2026-11-21. No compliance or completed-test claim is made.

## Applicable guidance

The official ASVS project identifies 5.0.0 as the stable release when checked on 2026-11-21. Its tagged CSV was retrieved and the exact requirement descriptions below were checked with Invoke-WebRequest and ConvertFrom-Csv. The Authentication, Session Management, Authorization and Logging Cheat Sheets were retrieved from the official OWASP CheatSheetSeries repository; the applicable identity, access and logging sections guide this plan. No login/session mechanism change is planned. Implementation must re-read the applicable guidance before modifying credential/authorization flows and record the controls actually verified.

Primary references:

- https://asvs.owasp.org/
- https://raw.githubusercontent.com/OWASP/ASVS/v5.0.0/5.0/docs_en/OWASP_Application_Security_Verification_Standard_5.0.0_en.csv
- https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
- https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html
- https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html

Applicable ASVS 5.0.0 controls (summarized):

| IDs | Work-specific verification |
|---|---|
| V2.2.2, V8.3.1 | Validate quota and binding operations on the server. |
| V2.3.3, V2.3.4, V15.4.2 | Atomic quota reservations and configuration checks. |
| V8.2.1–V8.2.3 | Enforce function, record and field permissions. |
| V16.2.1, V16.2.5 | Correlate node/request/allocation without usable credentials. |
| V16.4.1, V16.4.2 | Encode log data against injection and protect stored logs and detail reads from unauthorized access/modification. |
| V3.2.2 | Render prompts as text rather than active HTML. |

## Planned security tests

- Ordinary users cannot change channel/key budgets, rebind entries, deduplicate rows or read another user's diagnostic request data.
- Existing channel operate permissions govern mutation; log administration permissions govern privileged body/node details. Unknown or altered references do not bypass these checks.
- Same credentials with different entry identities never share a user's binding or local allowance. Stale index/version actions fail before mutation.
- Mutations produce existing-style administrative audit metadata without complete upstream credentials, incoming access tokens or captured prompt text.
- Request capture is limited to relay text fields. No Authorization/header/session capture and no accidental params/body duplication into process logs.
- Prompt strings containing script-like content remain inert; oversized, binary and malformed bodies have bounded explicit states.

## Validation environment findings

- The CI database service baseline is MySQL 8.4 and PostgreSQL 16 (.github/workflows/ci.yml:85–105). Existing fixtures use TEST_MYSQL_DSN, TEST_POSTGRES_DSN and controller-specific TEST_CHANNEL_SQL_DSN.
- The current PowerShell PATH has Node but no Go, Docker or Bun. Standard C:/Go and Program Files Go/Docker executable paths were absent. This is environment discovery, not a conclusion that test execution is impossible; implementation should locate usable WSL or other installed toolchains before provisioning a test environment.
- No product code or database data has been changed, and no test suite has been run in this planning phase.
- Required implementation evidence: exact database versions, non-skipped dialect results, fresh and previous-release migration fixtures, two startup/migration passes, preserved data/indexes/uniqueness, and ClickHouse log details/read/cleanup where used.
- Frontend commands come from web/package.json: bun run test for affected suites, bun check -b, targeted lint/format, i18n sync and bun run build. Preserve protected headers and identities.
