# Roadmap

Blueprint has a usable compiler, a mature Node target, an advanced Python
target, and a deliberately experimental Effect health/config scaffold. The roadmap is
ordered around trust: the compiler should reject programs it cannot preserve
before it adds more syntax or targets.

For release-level verification gates, see [Production Readiness](/production-readiness).
For implementation-sized work, see the repository
[BACKLOG.md](https://github.com/abdul-hamid-achik/blueprint/blob/main/BACKLOG.md).

## What works today

| Surface | Status | Notes |
|---|---|---|
| Language frontend | Stable core | Lexer, parser, AST, formatter, linter, diagnostics, and resolver facts |
| Node target | Mature reference target | Hono, Drizzle, Zod, frontend contracts, Vitest/PGlite plus opt-in fast-check properties, migrations, one-level relationships, computed fields, workers, schedules, STREAM, and WebSocket transport |
| Python target | Advanced beta | FastAPI, SQLAlchemy, Pydantic, Alembic, pytest/testcontainers for the supported HTTP/data subset; unsupported declarations fail closed |
| Effect target | Experimental runnable scaffold | Pinned project, typed secret/env `Config`, and `GET /health`; model and endpoint emission are not implemented |
| Developer tools | Available | `check`, `build`, `diff`, `fmt`, `lint`, `docs`, `test`, `migrate`, conservative TypeScript `import`, `doctor`, `stats`, `context`, `llms`, and LSP with a VS Code client |
| Deployment | Docker | Image build and local health smoke; hosted deploy targets remain future work |

Node is the default and the only target used for the 1.0 release gates. Python
can be selected explicitly with `--target python`; Effect is for generator
experiments, not production services.

Node and Python now share deterministic database activation: any model implies
the complete Postgres dependency/env/schema/migration layer, while an explicit
database with zero models emits an importable empty schema/Base.

## Now: correctness before breadth

### Strengthen semantic checking

`bp check` now rejects unbound and use-before-bind arrow references, unknown
data-operation models, invalid conditional scope leaks, duplicate declarations,
callable arity mismatches, direct model-field typos, duplicate enum variants,
and duplicate or malformed blueprint settings. It also checks conservative
primitive/enum/model/list/map/optional/null assignability across declared calls,
input reassignments, and known model writes, including nullable fetch results
and truthy-guard narrowing. The remaining semantic depth is nested JSON/FK leaf
typing, composite function-output recovery, and broader expression/operator
assignability.

### Make every target fail closed

A successful build must never silently discard source behavior. Effect audits
blueprint settings, middleware uses, declaration contents, and top-level AST
kinds before returning files; Python has an exhaustive declaration and
expression gate: authored tests, inline function logic, STREAM/WS bodies,
unsupported input types, undeclared env fields, attribute access on JSON/map
inputs or JSON-returning function results, unknown value calls, unsafe native
implementation config, mismatched defaults/constraints, Python-keyword
generated names, dynamic/bound
`where(q)` filter accumulators, and middleware shapes outside the supported
`fetch`/`log`/declared-fn/`inject`/`guard` subset fail before files are
returned. Bare `where(q)` remains text search for string/text endpoint inputs.
Future target work can add vertical slices without weakening that boundary.

### Harden external integrations

Node external-service auth and retry now have compile-time and mocked-fetch
runtime coverage. Supported auth (`bearer`, `jwt`, `basic`, `api_key`) consumes
one declared secret/env credential and emits the expected request header.
`retry N` is N additional immediate attempts for network/timeout failures and
HTTP 408/429/5xx, each with a fresh timeout; other 4xx responses are not
retried. Provider-specific signing, refresh flows, and backoff remain work for
a user-owned function or a future richer policy surface.

The same rule applies to `subscribe ... from(service)`: Node rejects that form
until a transport adapter can promise delivery from another service. A
source-less `subscribe "event" { ... }` remains available for the in-process
event bus.

### Keep source generation reviewable

Quoted `@>` slots now fail every target before files are emitted and point to
`bp generate --write`. The remaining LLM work is provider flexibility and a
review workflow that makes source changes easier to inspect before applying.

## Next: complete the daily workflow

### Python parity

- Translate or reject every Node-supported declaration without placeholders.
- Run authored Blueprint test blocks under pytest.
- Complete STREAM/WS event delivery, workers, schedules, storage, and
  structured logging.
- Continue compile, syntax, idempotency, and runtime gates across all canonical
  examples.

### Editor experience

The language server provides diagnostics, hover, go-to-definition,
context-aware completion, and local-workspace symbol search. The packaged VS
Code client starts `bp lsp` automatically and supports configurable server
paths/restarts. Next steps are rename, references, code actions, incremental
sync/indexing, and broader cross-file semantic completion.

### Deployment

- Exercise the Docker smoke path in CI.
- Add one verified hosted deployment adapter before documenting more platform
  recipes.

### Documentation as a tested interface

- Validate complete `.bp` examples in documentation during CI.
- Compare documented CLI commands and target support with the binary's
  embedded command registry.
- Keep one public support matrix for parsed, generated, and runtime-enforced
  features.
- Split the long language reference into task-focused sections without
  breaking stable anchors.

## Later: ecosystem and language evolution

These are useful directions, but they come after correctness gates:

- a package registry for reusable Blueprint middleware, types, and pipes;
- a runnable Vite application target that consumes the generated frontend SDK;
- relationship aliases, self/repeated-target joins, nested eager loading, and
  Python relationship/computed-field parity beyond the shipped one-level Node
  slice;
- batch data operations;
- Go or other generators built on the shared `Generator` and `resolve` contracts;
- a browser playground with parse/check feedback and generated-code preview;
- adversarial invalid-input properties, credential-aware arbitraries, and
  hermetic adapters beyond the shipped valid-request Node property suite;
- additional verified deployment providers.

Proposed features are not accepted syntax until they appear in the
[Language Reference](/language-reference) with an implementation status and a
passing fixture.

## Design constraints that remain fixed

Roadmap work preserves Blueprint's core rules:

1. Arrows keep data flow visible: inputs, steps, outputs.
2. `@` intent remains syntax, not a comment convention.
3. Blocks stay flat; `try/recover` is the deliberate exception.
4. `guard`, `when`, and `map` replace general `if`/loops.
5. Generated files and user-owned implementation files remain distinguishable.
6. Rebuilding stays deterministic and manifest-aware.

## Contributing

Start with [Contributing on GitHub](https://github.com/abdul-hamid-achik/blueprint/blob/main/CONTRIBUTING.md),
then choose a scoped item from the backlog. Changes to language behavior should
include parser/checker coverage, generator coverage for every affected target,
an end-to-end fixture, and documentation in the same change.
