# Design

Admin Group/Channel navigation uses real validated server pins and effective group/model/key restrictions; returning to groups clears pin. Sort copies naturally with valid fixed en collator, stable ties and preserved selection.

Use parent design.md section “Playground and upstream endpoints” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/code-findings.md.

Dependencies: Separate R2 and R3 commits; serialize selector changes. Ownership: pJ; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
