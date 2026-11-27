# Backend Development Guidelines

> Best practices for backend development in this project.

---

## Overview

This directory contains guidelines for backend development. Fill in each file with your project's specific conventions.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Module organization and file layout | To fill |
| [Database Guidelines](./database-guidelines.md) | Allocation snapshots, mapped capabilities, migrations and cache boundaries | Partially defined |
| [Error Handling](./error-handling.md) | Error types, handling strategies | To fill |
| [Quality Guidelines](./quality-guidelines.md) | Async quota fixture boundaries; other conventions pending | Partially defined |
| [Logging Guidelines](./logging-guidelines.md) | Original relay request capture, protected stores, privileged reads and retention | Defined |
| [Log user scopes](./log-user-scopes.md) | Shared administrator row predicates, immutable self access and viewer projections | Defined |
| [CORS Origins](./cors-origins.md) | Exact credentialed browser origins and preflight behavior | Defined |
| [Secure verification](./secure-verification.md) | Durable audit before proof, bounded waits and non-replayable factors | Defined |
| [Merge and Release Contract](./merge-release-contract.md) | Preserve both merge parents and verify actual release artifacts | Defined |

---

## How to Fill These Guidelines

For each guideline file:

1. Document your project's **actual conventions** (not ideals)
2. Include **code examples** from your codebase
3. List **forbidden patterns** and why
4. Add **common mistakes** your team has made

The goal is to help AI assistants and new team members understand how YOUR project works.

---

**Language**: All documentation should be written in **English**.
