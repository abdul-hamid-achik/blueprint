# Errors and Diagnostics

Every error from `bp check`, `bp build`, `bp lint`, and `bp fmt --check` renders through `internal/diag`. The shape is consistent:

```
error[C004]: examples/todo-api.bp:14:1
   |
14 | model todo {
   | ^^^^^
   = duplicate model name "todo"

   Hint: rename one of them, or remove the duplicate definition.
```

- Header: `error[<code>]: file:line:col`
- Source line + caret pointing at the bad token
- Message
- Hint with an actionable suggestion

Color is auto-detected (TTY → color; pipe → plain). `NO_COLOR=1` forces plain.

## Code namespaces

| Prefix | Source pass | Examples |
|---|---|---|
| `L###` | Lexer | `L001` `'|'` not part of `'|>'` (use `'|>'`?) |
| `P###` | Parser | `P001` file does not start with a `blueprint` block |
| `C###` | Checker (semantic) | `C001`–`C015` — duplicate names, unknown types, unknown refs, hint capitalization, etc. |
| `R###` | Resolver / typed IR | reserved |
| `G###` | Codegen | reserved (codegen panics are bugs, not user diagnostics) |

`C###` is by far the largest pool. Run `bp explain <code>` to read the full docs for any code:

```bash
bp explain C001
bp explain L001
bp explain P001
```

`bp explain` reads from `internal/diag/error-codes.md` (embedded at build time). The mirrored `docs/error-codes.md` is the same content for the VitePress docs site; a CI test (`TestErrorCodesDocInSync`) keeps the two from drifting.

## Why some errors don't have a code yet

Codes are added incrementally as we identify high-traffic sites. Uncoded errors render the same way but without the `[Cxxx]` bracket. The current namespace covers the audit's "top 80% of common errors" threshold; adding a new code is a small PR (one constant + one `addErrorCode` migration + one doc section).

## LSP rendering

The Blueprint LSP server (`bp lsp`) emits each diagnostic as an LSP `Diagnostic` with `code` set to the structured code (when present), `source: "blueprint"`, and a `Range` covering the bad token. Editors will surface squiggles and (for clients that respect `codeDescription`) link the code to the error-codes docs page.

## See also

- `bp context cli` — running `bp check` / `bp lint`
- `bp context language` — what `.bp` constructs the checker validates
