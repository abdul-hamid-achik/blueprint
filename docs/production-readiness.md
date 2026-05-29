# Production Readiness

What "1.0" means for Blueprint, and the measurable gates we use to get there.
This file is the source of truth for release decisions; the prose roadmap lives in
[roadmap.md](./roadmap.md) and the tickable work tracker is
[BACKLOG.md](https://github.com/abdul-hamid-achik/blueprint/blob/main/BACKLOG.md).

This is a live document. Every time we ship a change that satisfies (or breaks)
one of the gates below, update the relevant row in the **status table** and add
a CHANGELOG entry under `[Unreleased]`.

---

## Definition of Done — 1.0

Blueprint hits 1.0 when **all five pillars** below pass their gates on `main`.
These are not aspirations; they are the contract a user opens a `.bp` file
expecting.

### Pillar 1 — Language stability

The grammar, semantic rules, and the keyword set are frozen. Behaviour-level
changes happen through additive surfaces only; anything that would invalidate
an existing valid `.bp` file requires a deprecation cycle.

| Gate | Threshold | Verified by |
|------|-----------|-------------|
| `SPEC.md` matches the parser | All grammar productions documented | Manual audit per release |
| All `testdata/valid/*.bp` parse and check | 100% | `go test ./internal/parser ./internal/checker` |
| All `testdata/invalid/*.bp` produce the expected error | 100% | `internal/checker` testdata harness |
| Fuzzers do not find new crashes in a 5-minute run | 0 new failures | `go test -fuzz=Fuzz... -fuzztime=5m` (release-gate) |

### Pillar 2 — Codegen correctness

Generated code is type-checked, runs against an in-memory database, and behaves
identically to its hand-written equivalent. No code path silently emits `any`
where a concrete type was derivable.

| Gate | Threshold | Verified by |
|------|-----------|-------------|
| Every example builds cleanly under `tsc --noEmit` | 100% | CI step: "Validate generated TypeScript" |
| Every example with endpoints passes `bp test --gen-tests` | 100% | CI step (add bun); manually verified today |
| `bp build` is idempotent | `bp diff --exit-code` returns 0 immediately after build | CI step: idempotency check (added in this commit) |
| Golden snapshots cover hello-world, todo-api, and one feature-complete example | true | `internal/codegen/js/golden_test.go` |
| No `: any` in route handler signatures or in middleware-injected context types | exception list documented | Manual audit per release |

### Pillar 3 — Developer experience

A first-time user types `bp init`, then `bp run`, and is in a working server.
Errors point at the source location, suggest a fix, and link to the docs.

| Gate | Threshold | Verified by |
|------|-----------|-------------|
| `bp check` errors include file:line:col + a `Hint:` | 100% of error kinds | All 54 checker `addError` sites populate `Hint` (verified `grep`) |
| Errors carry structured codes (P###/C###/...) | top 80% of common errors covered | `internal/diag` + `docs/error-codes.md`; C001–C005 covered today |
| `bp fmt` is idempotent (`fmt(fmt(x)) == fmt(x)`) | 100% of `testdata/valid/*` | CI step `bp fmt --check` (extend to all examples) |
| `bp lint` produces 0 warnings on every shipped example | 100% | CI step "Lint examples" |
| LSP provides at least: diagnostics, go-to-def, hover for `@` intents | implemented and tested in VS Code | `internal/lsp` tests + manual VS Code probe |
| `bp doctor` checks every external dependency the toolchain assumes | bun, node, postgres, drizzle-kit | Manual probe per release |

### Pillar 4 — Testing & migrations

Tests run with no external services. Migrations against a real Postgres are
documented and exercised on the canonical examples in CI.

| Gate | Threshold | Verified by |
|------|-----------|-------------|
| `bp test` is self-contained (in-memory PGlite) | 100% — no `DATABASE_URL` needed | Verified today across todo-api / ecommerce-api / auth-service |
| Auto-generated contract tests cover every endpoint | 100% | `--gen-tests` walks `endpoints` |
| `bp migrate generate` produces a valid migration for every example | 100% | CI step: run `bunx drizzle-kit generate` on each example |
| Authored `test {}` blocks compose with the in-memory harness | 100% | `genTest` mocks `src/lib/db` when `--gen-tests` is on |

### Pillar 5 — Deploy

`bp deploy` takes a checked `.bp` file to a running container or to Fly.io with
one command, with predictable failure modes when prerequisites are missing.

| Gate | Threshold | Verified by |
|------|-----------|-------------|
| `bp deploy --target docker` builds and runs the generated image | smoke test passes | Manual probe per release |
| `bp deploy --target fly` works with a pre-existing `fly.toml` | smoke test passes | Manual probe per release |
| Generated Dockerfile uses a non-root user and a multi-stage build | true today | Code review |
| Generated `index.ts` shuts down cleanly on `SIGTERM` (server + DB pool) | true | Verified in current codegen |

---

## Status table

Replace the date when a row's state changes; PRs that move a gate update this
table and the CHANGELOG together.

| Pillar | Gate | Status | As of |
|--------|------|--------|-------|
| 1 | Grammar fuzz, 5min | ✅ green | 2026-05-29 |
| 1 | testdata valid+invalid | ✅ green | 2026-05-29 |
| 2 | Examples tsc clean | ✅ green | 2026-05-29 (CI) |
| 2 | Examples pass `bp test --gen-tests` | ✅ verified locally | 2026-05-29 |
| 2 | `bp build` idempotent | ✅ green (CI gate) | 2026-05-29 |
| 2 | No `: any` in handler boundaries | 🟡 audit pending | 2026-05-29 |
| 3 | Errors have `Hint:` on every kind | ✅ green (54/54 checker sites) | 2026-05-29 |
| 3 | Structured error codes | 🟡 12 of ~15 common sites covered (C001–C012); `bp explain` shipped | 2026-05-29 |
| 3 | `bp fmt --check` on all examples | 🟡 hello-world wired in CI; data-op shorthand bug fixed (`internal/ast/printer.go`), remaining 4 examples blocked only on column-alignment + blank-line preservation — see BACKLOG | 2026-05-29 |
| 3 | LSP feature depth | 🔴 basic only | 2026-05-29 |
| 4 | Self-contained `bp test` | ✅ shipped | 2026-05-29 |
| 4 | `bp migrate generate` on every example | 🟡 only hello/todo wired | 2026-05-29 |
| 5 | `bp deploy --target docker` smoke | 🟡 preview | 2026-05-29 |
| 5 | `bp deploy --target fly` smoke | 🔴 not implemented | 2026-05-29 |

Legend: ✅ meets gate · 🟡 partial · 🔴 not started · ⏳ implemented, gate not yet enforced in CI.

### Multi-target codegen progress

Not a 1.0 gate (post-1.0 beta per "What's out of scope"), but tracked here so
the Python work doesn't drift.

| Phase | What | Status | As of |
|-------|------|--------|-------|
| 1 | Static endpoints → FastAPI app, uv project | ✅ shipped | 2026-05-29 |
| 2 | `model` → Pydantic + SQLAlchemy + Alembic | ✅ shipped | 2026-05-29 |
| 3 | Endpoint bodies with `\|>` data ops → sync SQLAlchemy (`try`/`recover`/`map` deferred to 3c) | ✅ shipped | 2026-05-29 |
| 3b | `where(col == val, ...)`, `first`, `when` block form | ✅ shipped | 2026-05-29 |
| 3c | `order(...)`, FK access, `when` inline, `try`/`recover`, `map`, `log` | ✅ shipped | 2026-05-29 |
| 3d/5 | `fn` decls + scaffold, `middleware` → FastAPI Depends, `sum()`, `now` | ✅ shipped — 4/5 examples compile | 2026-05-29 |
| 5 | STREAM (SSE), WS, `cache redis` | ✅ shipped — **5/5 examples compile** | 2026-05-29 |
| 3d | Non-`==` `where`, bare-expression steps, partial-commit rollback, structured `log` | 🔴 not started | 2026-05-29 |
| 4 | Middleware, fn/pipe (`impl python`), `bp test` with testcontainers | 🟡 `--gen-tests` shipped (pytest + testcontainers harness); middleware / fn impl python still pending | 2026-05-29 |

---

## Engineering workflow

This is the rhythm that gets gates from 🔴 to ✅ without regressing the ones
already at ✅.

1. **Pick from the BACKLOG.** Anything not in `BACKLOG.md` is not in scope for
   this iteration; add it there first, with a one-line "why."
2. **Branch.** Never push directly to `main`. CI runs on PR and on push.
3. **Pre-flight locally before opening a PR:**
   - `go test ./... -race`
   - `gofmt -l .` returns empty
   - `go vet ./...`
   - For codegen changes: build at least one example with and without
     `--gen-tests`, then `bp diff --exit-code` on the same example
     immediately afterward (verifies idempotency).
4. **Goldens.** A golden snapshot change is intentional. If `go test` says a
   golden mismatches, regenerate with `UPDATE_GOLDEN=1 go test ./internal/codegen/js/`
   and explain the diff in the PR body — *never* `UPDATE_GOLDEN=1` to silence
   an unintentional change.
5. **Changelog.** Every PR that changes generated output, language behavior, or
   CLI surface adds a bullet under `[Unreleased]`. Bug fix vs. addition vs.
   change — match the existing `[0.3.x]` style.
6. **Backlog hygiene.** When a backlog item ships, move it under the
   `## Now` → `[x]` line with a one-line "_shipped — see CHANGELOG_."
7. **Status table.** If the PR moves a production-readiness gate, update the
   row above in the same PR.
8. **Release.** A tag push fires `.github/workflows/release.yml` which runs the
   full test suite, then GoReleaser. Don't tag until the status table has zero
   🔴 rows for the pillar you're shipping.

---

## What's explicitly out of scope for 1.0

Stating these up front so we don't drift:

- **Languages other than TypeScript/Node.** Multi-target codegen (Python,
  Go) becomes possible after IR slices 2–4 ship, but it ships as a separate
  beta and is not gated on 1.0.
- **Hosted service.** Blueprint is a compiler and CLI; we do not run anyone
  else's code. `bp deploy` shells out to user-owned infrastructure.
- **Custom DSL extensions at runtime.** The plugin system in the roadmap
  is post-1.0.
- **`bp generate` (LLM slot resolution) as a default.** It stays opt-in,
  requires `ANTHROPIC_API_KEY`, and is documented as preview. Review the
  generated code before shipping.
