# Agent Workflow

The `.bp` file is the source of truth. The generated project is output: `bp build` rewrites it, so changes belong in the `.bp` file.

## The loop

```
1. Edit the .bp source.
2. bp fmt --write <file.bp>                      # canonical layout
3. bp check <file.bp>                            # syntax + semantics; fix every error
4. bp lint <file.bp>                             # warnings: unused inputs/secrets, missing intents, unguarded fetch
5. bp build <file.bp> --out <dir>                # generate the project
6. bp diff <file.bp> --out <dir> --exit-code     # 0 right after a build; a gate, not a fix
7. bp test <file.bp>                             # generated contract tests (node, in-memory Postgres)
```

Use `bp check` (with several files or a directory when useful) after every edit. Run steps 4 to 7 before you report a change as done.

For a multi-file project, pass the directory: `bp check services/`, `bp lint --json services/`, `bp fmt --check services/`.

## Reading errors

Each error prints `file:line:col`, a caret, and a `Hint:`. Look at the line it names before changing anything. `bp explain <code>` gives the full catalog entry for a code such as C052.

## Generated code

- Never hand-edit files in the generated project except the ones marked `UserOwned` in `.blueprint/manifest.json` (for example `src/impl/functions/*`). The build scaffolds those once and never overwrites them.
- Other generated files are rewritten on each build, and files that drop out of the build are deleted.

## Target choice

Default to `node`. Use `--target python` only for the HTTP/data subset that `bp build --target python` accepts. Check that before you promise Python output.

## Examples as reference

`examples/*.bp` are checked by CI. Grep them for a construct you need:

```bash
grep -l 'try {'         examples/*.bp   # try/recover
grep -l 'paginate'      examples/*.bp   # pagination
grep -l 'inject .* as'  examples/*.bp   # middleware injection
grep -l '^STREAM'       examples/*.bp   # STREAM endpoints
grep -l '^WS'           examples/*.bp   # WebSocket endpoints
```

## Tests

- `bp test <file.bp>` builds with contract tests and runs Vitest on Node, with PGlite. No Docker needed.
- `bp test <file.bp> --gen-property-tests` adds deterministic fast-check suites for supported REST routes. Unsupported routes fail the build; they are never skipped.
- `bp test <file.bp> --target python` runs pytest with a Docker Postgres.
- Authored Node tests (`test name { ... }`) support a narrower set than the grammar. See `docs/testing-guide.md`.

## Migrating from TypeScript

`bp import <path> --from ts --out scaffold.bp` recovers the static structure of Drizzle and Zod code. Handlers are not imported: each becomes a TODO/501 route that must be rewritten and tested by hand.

## See also

- `bp context cli` — every command and exit code
- `bp context language` — valid `.bp`
- `bp context errors` — diagnostic format and codes
- `bp context examples` — the example specs
