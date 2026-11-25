# Implementation Plan: [FEATURE]

**Branch**: `[###-feature-name]` | **Date**: [DATE] | **Spec**: [link]

**Input**: Feature specification from `/specs/[###-feature-name]/spec.md`

**Note**: This template is filled in by the `/speckit-plan` command. See `.specify/templates/plan-template.md` for the execution workflow.

## Summary

[Extract from feature spec: primary requirement + technical approach from research]

## Technical Context

<!--
  ACTION REQUIRED: Replace the content in this section with the technical details
  for the project. The structure here is presented in advisory capacity to guide
  the iteration process.
-->

**Language/Version**: [e.g., Python 3.11, Swift 5.9, Rust 1.75 or NEEDS CLARIFICATION]

**Primary Dependencies**: [e.g., FastAPI, UIKit, LLVM or NEEDS CLARIFICATION]

**Storage**: [if applicable, e.g., PostgreSQL, CoreData, files or N/A]

**Testing**: [e.g., pytest, XCTest, cargo test or NEEDS CLARIFICATION]

**Target Platform**: [e.g., Linux server, iOS 15+, WASM or NEEDS CLARIFICATION]

**Project Type**: [e.g., library/cli/web-service/mobile-app/compiler/desktop-app or NEEDS CLARIFICATION]

**Performance Goals**: [domain-specific, e.g., 1000 req/s, 10k lines/sec, 60 fps or NEEDS CLARIFICATION]

**Constraints**: [domain-specific, e.g., <200ms p95, <100MB memory, offline-capable or NEEDS CLARIFICATION]

**Scale/Scope**: [domain-specific, e.g., 10k users, 1M LOC, 50 screens or NEEDS CLARIFICATION]

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Verify compliance with `.specify/memory/constitution.md` (v1.0.0) and `AGENTS.md`:

- [ ] **V1 Scope & Zero Redundant Infra**: Single Go binary + PostgreSQL 17 only (zero Redis, zero ClickHouse, no public self-registration, no prohibited ORM/DI dependencies).
- [ ] **L0–L6 Layering & Explicit Wiring**: Strictly downward imports only; no `internal/agent` ↔ `internal/task` mutual imports; zero business logic in `internal/web`; dependencies wired explicitly in `cmd/synapse-server/wire.go`.
- [ ] **Multi-Tenant Fail-Closed & Crypto**: Tenant ops enforce `tenant.Require(ctx)`; async goroutines use `tenant.Detach(ctx)`; new tenant tables lead PK with `company_id VARCHAR(64) NOT NULL`; secrets use `SecretCipher` (no plaintext/env fallback).
- [ ] **Infrastructure Isolation**: No direct `pgx`/`goose` outside `infra/db`/`storage/postgres`/`cmd`; no direct `os/exec.Command` (use `spi.ProcessLauncher`); no raw `http.Client` (use `TrustedClient` / `UntrustedEgressClient`).
- [ ] **PG17 Schema & Migrations**: Forward-only `goose` migrations (no `DROP TABLE`), zero physical FKs, UTC `TIMESTAMPTZ`, `NUMERIC(16,6)` fixed-point currency, `JSONB`, idempotent `ON CONFLICT`.
- [ ] **Resilience & Concurrency**: Explicit layered timeouts (streaming `Timeout=0`), `httpclient` error classification without business fallback, managed `BackgroundGroup` loops, hard OOM caps, lock-free I/O, 30s graceful shutdown compatibility.
- [ ] **API, MCP & Go Quality Gates**: `/api/v1` envelope & 6 protocol exceptions preserved (`POST /mcp` wraps errors as `isError: true`), `camelCase` DTOs, `gofmt` / `go vet ./...` / `go test -race ./...` (`arch_test` + `guard_test`) green.

## Project Structure

### Documentation (this feature)

```text
specs/[###-feature]/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)
<!--
  ACTION REQUIRED: Replace the placeholder tree below with the concrete layout
  for this feature. Delete unused options and expand the chosen structure with
  real paths (e.g., apps/admin, packages/something). The delivered plan must
  not include Option labels.
-->

```text
# [REMOVE IF UNUSED] Option 1: Single project (DEFAULT)
src/
├── models/
├── services/
├── cli/
└── lib/

tests/
├── contract/
├── integration/
└── unit/

# [REMOVE IF UNUSED] Option 2: Web application (when "frontend" + "backend" detected)
backend/
├── src/
│   ├── models/
│   ├── services/
│   └── api/
└── tests/

frontend/
├── src/
│   ├── components/
│   ├── pages/
│   └── services/
└── tests/

# [REMOVE IF UNUSED] Option 3: Mobile + API (when "iOS/Android" detected)
api/
└── [same as backend above]

ios/ or android/
└── [platform-specific structure: feature modules, UI flows, platform tests]
```

**Structure Decision**: [Document the selected structure and reference the real
directories captured above]

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| [e.g., 4th project] | [current need] | [why 3 projects insufficient] |
| [e.g., Repository pattern] | [specific problem] | [why direct DB access insufficient] |
