# Design

Root-only per-item reviewed commits; exact committed-tree checks then integration push and fork/main release workflow. Verify tag/assets/image digest then actual application server version/health; no force pushes/guessed target.

Use parent design.md section “Commits, delivery and rollback” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/release-delivery.md.

Dependencies: All R1-R24 checks/commits and actual target/path/origin. Ownership: root coordinator; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
