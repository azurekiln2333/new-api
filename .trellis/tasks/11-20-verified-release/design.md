# Design

Root-only per-item reviewed commits; exact committed-tree cloud CI then integration push and fork/main release workflow. Verify tag/assets/image digest; no force pushes/guessed target. The user explicitly canceled server verification: do not request server access, run deployment checks, or claim that the server pulled the release. The user's latest instruction also excludes further local tests/builds; complete remaining validation through cloud CI.

Use parent design.md section “Commits, delivery and rollback” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/release-delivery.md.

Dependencies: All R1-R24 checks/commits and fork publication access. Ownership: root coordinator; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off in cloud CI. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
