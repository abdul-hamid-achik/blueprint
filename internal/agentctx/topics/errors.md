# Errors and Diagnostics

`bp check`, `bp build`, `bp lint`, and `bp fmt --check` print diagnostics in one shape:

```
error[C004]: examples/todo-api.bp:14:1
   |
14 | model todo {
   | ^^^^^
   = duplicate model name "todo"

   Hint: rename one of them, or remove the duplicate definition.
```

- Header: `error[<code>]: file:line:col`, then the source line with a caret.
- Message, then a `Hint:` with the fix.
- `bp lint` prints `file:line:col [warning] rule: message` with a hint. Lint findings do not change `bp check` results.

Color is automatic for a TTY. `NO_COLOR=1` or `--no-color` forces plain output.

## Code ranges

| Prefix | Pass | Notes |
|---|---|---|
| `C001`-`C052` | Checker | Every checker error has a code. C048 is an enum or name-case rule. C052 is a constraint value that does not fit its type. C035 is an unbound identifier, including `close` outside a WS handler. C005 is a duplicate route. C011 is a step after an output. |
| `L###` | Lexer | Some tokens. |
| `P###` | Parser | For example P001: the file does not start with a `blueprint` block. Some parser errors have no code yet and render without a bracket. |
| `R###`, `G###` | Resolver, codegen | Reserved. A codegen failure prints as a codegen error, with exit 4. |

## Looking up a code

```bash
bp explain C052
bp explain C005
```

`bp explain` prints the entry from the embedded catalog. For an unknown code it exits 1. The catalog lives in `internal/diag/error-codes.md` and mirrors `docs/error-codes.md`.

## Workflow

1. Open the file at the reported line and column before changing anything.
2. Run `bp explain <code>` if the hint is not enough.
3. Fix the `.bp` source and re-run `bp check`. Never edit generated output to silence an error.

## See also

- `bp context workflow` — where diagnostics fit in the loop
- `bp context language` — the constructs and constraint rules the checker enforces
