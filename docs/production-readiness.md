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
| Language Reference matches the parser | All supported grammar and semantic rules documented | Manual audit per release |
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
| Every example with endpoints passes `bp test` | 100% | Generated harness is smoke-checked in CI; full Vitest execution remains a release/manual gate |
| `bp build` is idempotent | `bp diff --exit-code` returns 0 immediately after build | CI step: idempotency check (added in this commit) |
| Golden snapshots cover hello-world, todo-api, and one feature-complete example | true | `internal/codegen/js/golden_test.go` |
| No `: any` in route handler signatures or in middleware-injected context types | exception list documented | Manual audit per release |
| New target-specific model/query semantics either compile or reject before files are returned | Node relationship/computed slice compiles; Python/Effect reject | parser/checker/resolve + target generator tests |

### Pillar 3 — Developer experience

A first-time user types `bp init`, then `bp run`, and is in a working server.
Errors point at the source location, suggest a fix, and link to the docs.

| Gate | Threshold | Verified by |
|------|-----------|-------------|
| `bp check` errors include file:line:col + a `Hint:` | 100% of error kinds | Checker diagnostic tests plus a release audit of every `addError` call site |
| Errors carry structured codes (L###/P###/C###/...) | top 80% of common errors covered | `internal/diag` + `docs/error-codes.md`; L001, P001, C001–C015 covered today |
| `bp fmt` is idempotent (`fmt(fmt(x)) == fmt(x)`) | 100% of `testdata/valid/*` | CI step `bp fmt --check` (extend to all examples) |
| `bp lint` produces 0 warnings on every shipped example | 100% | CI step "Lint examples" |
| LSP provides diagnostics, definition, hover, completion, workspace symbols, and a launchable editor client | shipped over stdio; VS Code client starts/restarts configurable `bp lsp` | `internal/lsp` completion/workspace/handler tests + `editors/vscode-blueprint` client tests + manual probe |
| `bp import` never implies behavior preservation | every recovered handler is reported and becomes TODO/501 | `internal/importer` + `cmd/bp` import tests |
| `bp doctor` checks every external dependency the toolchain assumes | bun, node, postgres, drizzle-kit | Manual probe per release |

### Pillar 4 — Testing & migrations

Tests run with no external services. Migrations against a real Postgres are
documented and exercised on the canonical examples in CI.

| Gate | Threshold | Verified by |
|------|-----------|-------------|
| `bp test` is self-contained (in-memory PGlite) | 100% — no `DATABASE_URL` needed | Generated contract harness; authored fixture/assets still need full-run coverage |
| Auto-generated contract tests cover every endpoint | 100% | `--gen-tests` walks `endpoints` |
| Property mode is deterministic and fail-closed | stable seeds/runs; unsupported routes produce no files | `internal/codegen/js/propertytest_test.go` + CLI tests |
| `bp migrate generate` produces a valid migration for every example | 100% | CI step: run `bunx drizzle-kit generate` on each example |
| Authored `test {}` blocks compose with the in-memory harness | 100% | Full Vitest run of the canonical authored suite |

### Pillar 5 — Deploy

`bp deploy` takes a checked `.bp` file to a running container or to Fly.io with
one command, with predictable failure modes when prerequisites are missing.
This pillar's `--target` is a *deploy* target (`docker`, `fly`) — a different
axis from the codegen `--target` (`node`/`python`/`effect`) covered under
[Pillar 2](#pillar-2--codegen-correctness) and
[Multi-target codegen progress](#multi-target-codegen-progress) below.
`bp deploy` always builds the node codegen target internally today, so this
pillar's gates are node-only regardless of how far python/effect progress.

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
| 2 | Examples pass `bp test` | 🟡 generated Node/Python harnesses are smoke-checked in CI; runners are not executed there | 2026-07-14 |
| 2 | `bp build` idempotent | ✅ green (CI gate) | 2026-05-29 |
| 2 | No `: any` in handler boundaries | ✅ Hono `Variables` typed from middleware-injected model (`typeof schema.M.$inferSelect` with `NonNullable` mapped fields); merged fn scaffolds typed from `fn.Inputs`/`Outputs` instead of `...args: any[]` | 2026-05-29 |
| 2 | Relationship/computed target behavior | ✅ Node emits one-level LEFT JOINs and pure computed materializers; Python/Effect reject unsupported ASTs | 2026-07-14 |
| 3 | Errors have `Hint:` on every kind | ✅ green; every checker diagnostic path supplies a hint | 2026-07-14 |
| 3 | Structured error codes | ✅ green (L001, P001, C001–C015 documented + emitted; `bp explain` shipped) | 2026-05-29 |
| 3 | `bp fmt --check` on all examples | ✅ green — column alignment shipped (v0.14.0); all 5 examples pass `bp fmt --check`; CI gate extended to all `examples/*.bp` | 2026-07-10 |
| 3 | LSP feature depth | ✅ diagnostics, definition, hover, context-aware completion, local-workspace symbols, workspace-folder updates, and packaged configurable VS Code stdio client | 2026-07-14 |
| 3 | Honest TypeScript import scaffold | ✅ static Drizzle/Hono plus transport-compatible `zValidator` facts only; nullable/type-changing Zod fields skip, Drizzle SQL/options loss warns, and all handlers are reported TODO/501 stubs | 2026-07-14 |
| 3 | `bp doctor` checks every external dep | ✅ probes drizzle-kit, tsc, python3, uv, alembic, pytest in addition to bun/node/postgres/redis/git; version-parser fixed (Redis no longer reports as `redis-cli`, Docker no longer keeps trailing comma) | 2026-05-29 |
| 3 | `bp lint` produces 0 warnings on every shipped example | ✅ green; two new rules ship in v0.10 (`where-predicate-self-equal`, `unused-input`) | 2026-05-29 |
| 4 | Self-contained `bp test` | 🟡 generated contract tests use in-memory PGlite; unsupported authored-test surfaces now fail preflight before build/install/Vitest rather than running a predictably incomplete suite | 2026-07-14 |
| 4 | Deterministic property generation | ✅ opt-in Node fast-check suite uses stable seeds and 32 valid-request runs; DB state resets per run, and impossible path/email/URL domains, ref-backed writes, recursion, or unsupported/non-hermetic routes reject with no files | 2026-07-14 |
| 4 | Authored tests compose with the harness | 🟡 the supported literal-path JSON/auth/assertion subset runs; cleanup, multipart/file fixtures, custom request entries, timing assertions, shared setup, and missing native assets are rejected with actionable diagnostics | 2026-07-14 |
| 4 | `bp migrate generate` on every example | ✅ CI runs `drizzle-kit generate` against all 5 examples on every push; `bp migrate --target python` shells to `uv run alembic ...` | 2026-05-29 |
| 5 | `bp deploy --target docker` smoke | 🟡 `--target` parsing + post-build `docker run` + `/health` probe + teardown wired in `cmdDeploy`; `--no-run` opt-out for CI image builds. Smoke happy path verified manually; not yet exercised in CI (would need Docker on the runner) | 2026-05-29 |
| 5 | `bp deploy --target fly` smoke | 🔴 explicit "not yet implemented; see the roadmap for status" error from `--target fly` parsing; spec-wide Fly support still pending | 2026-05-29 |

Legend: ✅ meets gate · 🟡 partial · 🔴 not started · ⏳ implemented, gate not yet enforced in CI.

### Multi-target codegen progress

The python target is not a 1.0 gate — 1.0 is defined against the node target
only (see "What's explicitly out of scope" below) — but it is no longer
theoretical: it is advanced, not planned. Supported canonical examples build
end-to-end under `--target python`; examples containing unsupported authored
tests now fail closed instead of silently losing them. Progress is tracked
phase-by-phase here so the work does not drift:

| Phase | What | Status | As of |
|-------|------|--------|-------|
| 1 | Static endpoints → FastAPI app, uv project | ✅ shipped | 2026-05-29 |
| 2 | `model` → Pydantic + SQLAlchemy + Alembic | ✅ shipped; Python follows the shared Node/Python activation rule (models imply Postgres, database-only emits empty `Base`) | 2026-07-14 |
| 3 | Endpoint bodies with `\|>` data ops → sync SQLAlchemy (`try`/`recover`/`map` deferred to 3c) | ✅ shipped | 2026-05-29 |
| 3b | `where(col == val, ...)`, `first`, `when` block form | ✅ shipped | 2026-05-29 |
| 3c | `order(...)`, FK access, `when` inline, `try`/`recover`, `map`, `log` | ✅ shipped | 2026-05-29 |
| 3d/5 | Native `fn` decls + scaffold, `middleware` → FastAPI Depends, `sum()`, `now` | ✅ shipped; inline `fn logic` now fails closed | 2026-07-14 |
| 5 | STREAM (SSE), WS, `cache redis` | 🟡 `cache redis` ships; STREAM/WS declarations now fail closed because lifecycle/event bodies are not translated yet | 2026-07-14 |
| 3d | Non-`==` `where` (`or`/`and`/`in`/text-search/duration), bare-expression steps, partial-commit rollback | ✅ shipped — `or`/`and`/`in`/text-search/duration RHS shipped v0.13.0; bare BlockExpr steps shipped v0.14.0; structured `log` still `print(f"...")` | 2026-07-10 |
| 4 | Middleware, fn/pipe (`impl python`), `bp test` with testcontainers | 🟡 `bp test --target python` now builds the pytest/testcontainers harness and runs `uv run pytest`; middleware supports `fetch`/`log`/declared-fn/`inject`/`guard` and rejects richer steps; authored Blueprint tests, inline fn logic, `pipe`, and full `impl python` parity remain open | 2026-07-14 |

**The effect target is separate and much earlier.** `--target effect`
(`internal/codegen/effect/`) exists and is wired into `bp build`/`bp diff`,
but it is an early scaffold, not tracked phase-by-phase above: it emits a
pinned, runnable `GET /health` service plus typed Effect `Config` for secrets
and supported env defaults. Endpoint/model/test emission is not implemented;
those flags and constructs fail closed. Treat it as experimental/opt-in, not as a target with
a 1.0-adjacent timeline.

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

- **1.0 gates apply to the node target only.** IR slices 2–4 (the
  target-agnostic `resolve` facts multi-target codegen leans on) have already
  shipped, and both a python target (`--target python`, advanced — see
  "Multi-target codegen progress" above) and an early effect scaffold
  (`--target effect`) exist today. Neither is gated on 1.0 or held to this
  document's pillar thresholds: python ships as a separate beta as it closes
  out its remaining phases, and effect is pre-beta. A Go target has not been
  started.
- **Hosted service.** Blueprint is a compiler and CLI; we do not run anyone
  else's code. `bp deploy` shells out to user-owned infrastructure.
- **Custom DSL extensions at runtime.** The plugin system in the roadmap
  is post-1.0.
- **`bp generate` (LLM slot resolution) as a default.** It stays opt-in,
  requires `ANTHROPIC_API_KEY`, and is documented as preview. Review the
  generated code before shipping.
