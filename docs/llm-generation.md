# LLM Generation

`bp generate` turns a quoted natural-language slot into Blueprint arrow
statements. It is an opt-in source-rewrite tool: it updates `.bp` source, then
the normal checker and code generators take over.

It does **not** generate a TypeScript/Python implementation file. Use a native
`impl node` function or a declared external service when the work belongs in
target-language code or another process.

## The workflow

```text
.bp with @> slot
  -> bp generate (Anthropic returns Blueprint statements)
  -> review the proposed source
  -> bp generate --write (replace the slot in the .bp file)
  -> bp check
  -> bp diff / bp build
```

Each slot is sent to the Anthropic Messages API. The request identifies the
containing block and asks for a short sequence of valid Blueprint statements,
such as `|>`, `guard`, `when`, or `->` lines.

## Slot syntax

A slot is a direct arrow statement with a required quoted, single-line prompt:

```bp
@> "validate the input format and reject values longer than 200 characters"
```

Do not prefix it with `|>`, and do not write an unquoted multiline prompt.

Optional hints follow the string using function-like syntax:

```bp
@> "load the user and reject suspended accounts" using(user) max_lines(3)
```

Hints are passed to the model as additional prompt text. They do not change the
checker or code generator by themselves.

### Where slots can appear

`@>` is valid anywhere the parser expects an arrow statement, including:

- HTTP, STREAM, and WebSocket handler bodies
- pipe and middleware bodies
- schedule, worker, and subscription bodies
- test setup and cleanup bodies
- a function's `logic { ... }` body

A function cannot contain `@>` directly after its signature. Functions choose
an implementation form (`impl ...`) or a `logic` body, so put the slot inside
`logic`:

```bp
fn normalize_name {
  <- value string
  -> string

  logic {
    @> "trim whitespace and return the value in lowercase"
  }
}
```

## Complete example

```bp
@ "A small service with two generation slots"
blueprint "generation-demo" {
  version "0.1.0"
  port    3000
  runtime node
}

fn normalize_name {
  <- value string
  -> string

  logic {
    @> "trim whitespace and return the value in lowercase"
  }
}

POST /api/names {
  <- name string required

  @> "reject names longer than 200 characters with status 400"
  |> normalized = normalize_name(name)

  -> 200 { name: normalized }
}
```

The generated response is expected to contain Blueprint statements, for
example a `guard` replacing the endpoint slot and an output statement replacing
the function slot. The exact response is nondeterministic and must be reviewed.

## Preview without changing the file

Set the API key, then run the command without `--write`:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
bp generate generation-demo.bp
```

Blueprint reports how many slots it found, calls Anthropic once per slot, and
prints the candidate updated Blueprint source. The input file is not modified.

Preview mode is for human review. Its stdout includes progress text as well as
the proposed source, so do not treat a simple shell redirect as a clean `.bp`
file.

## Write the replacements to source

```bash
bp generate generation-demo.bp --write
```

`--write` replaces each source line containing an `@>` slot in
`generation-demo.bp`. It does not write to `generated/` and does not create a
file under `src/functions/`.

Preview and write are separate model calls, so the write result may differ from
the earlier preview. Keep the source under version control and inspect the
actual edit.

## Check, review, and build

Treat generated statements like any other untrusted code change:

```bash
# Validate the rewritten Blueprint
bp check generation-demo.bp

# Review the source edit
git diff -- generation-demo.bp

# Preview and then write generated-project changes
bp diff generation-demo.bp --target node --out generated
bp build generation-demo.bp --target node --out generated

# Run generated tests when the service has them
bp test generation-demo.bp --target node --out generated
```

For Python output, use `--target python` with `bp diff`, `bp build`, or
`bp test`. Generation itself is target-agnostic because it edits Blueprint
source rather than target-language files.

## Prompt-writing guidance

The generator asks the model for at most a few concise arrow statements. Prompts
work best when they describe one Blueprint-level operation.

Good prompts state:

- the condition or data operation to express
- the expected binding or output
- an HTTP status and message for guard failures
- relevant names already in scope

```bp
@> "fetch the user by user_id and return 404 with message User not found when absent"
```

Avoid prompts that require importing a library, inventing a new dependency, or
writing a large algorithm:

```bp
@> "use Sharp to resize, watermark, optimize, upload, and index this image"
```

That work belongs behind a declared native function:

```bp
fn process_image {
  <- source file
  -> result json

  impl node {
    module: "./internal/images"
    func:   "processImage"
  }
}
```

## Intent annotations are separate

`@ "..."` documents a block and can appear in generated comments or OpenAPI
descriptions. `@> "..."` is a replaceable generation step. An intent annotation
does not itself call the model, and the current generation prompt is built from
the slot text, hints, and a short containing-block label rather than the entire
source file.

## Current limitations

- `ANTHROPIC_API_KEY` is required; there is no offline generation mode.
- The Anthropic model is selected by the tool; the CLI does not expose model or
  temperature flags.
- A slot occupies one source line. `--write` replaces that line with the model's
  returned statements while preserving its leading indentation.
- `bp check` can validate a source file that still contains a slot, but every
  target's `bp build`/`bp diff` path rejects unresolved `@>` slots with codegen
  exit code 4. Resolve them before generating a project.
- Model output is not guaranteed to parse, pass semantic checks, or preserve
  business intent. Always run `bp check` after writing.
- Re-running generation on an unchanged file only processes `@>` slots that are
  still present. Once a slot is replaced, it is no longer discoverable.

For deterministic native behavior, use `impl`. For target-neutral declarative
behavior you can express directly, prefer writing a `logic` body yourself. Use
`@>` when a reviewed, one-shot Blueprint source transformation is useful.
