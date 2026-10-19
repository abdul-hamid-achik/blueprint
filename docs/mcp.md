---
title: MCP Server
description: Run bp mcp to expose the Blueprint compiler's check, lint, format, explain, context, build, and diff commands to MCP-capable agents over stdio.
---

# MCP Server

`bp mcp` runs a stdio [Model Context Protocol](https://modelcontextprotocol.io)
server. Agents that speak MCP get the compiler's `check`, `lint`, `format`,
`explain`, `context`, `build`, and `diff` as tools, and the embedded language
reference as resources, instead of shelling out to `bp` and parsing text. The
tools return the same JSON as `bp check --json` and `bp lint --json`.

## Setup

### Claude Code

```bash
claude mcp add blueprint -- bp mcp
```

### Other clients

For clients that read an `mcpServers` map, add:

```json
{
  "mcpServers": {
    "blueprint": {
      "command": "bp",
      "args": ["mcp"]
    }
  }
}
```

`bp` must be on `PATH`, or replace `"bp"` with an absolute path to the binary.
The server resolves relative paths against its working directory, which is the
directory the client launches it in. Start the client from the project root.

The server writes one line to stderr when it starts. Stdout carries only
protocol messages.

## Tools

| Tool | Inputs | Returns | Writes files? |
| --- | --- | --- | --- |
| `check` | `path` or `source`, optional `filename` | The `bp check --json` document (`success`, `errors`, `error_count`) | No |
| `lint` | `path` or `source`, optional `filename` | The `bp lint --json` document (`success`, `issues`) | No |
| `format` | `path` or `source`, optional `filename` | The formatted text, plus `{file, changed, formatted}` as structured content | No |
| `explain` | `code`, for example `C052` (case does not matter) | The documentation for a diagnostic code | No |
| `context` | optional `topic` (a `bp context` topic, or `all`) | The agent reference text for that topic | No |
| `build` | `path`, optional `out` (default `generated`), optional `target` (`node`, `python`, `effect`; default `node`) | Build result: exit code, output directory, stdout, stderr | Yes, into `out` |
| `diff` | `path`, optional `out` and `target` (same defaults) | Whether `out` would change (`changed`), plus exit code and output | No |

Notes:

- `check`, `lint`, and `format` take either `path` (a `.bp` file) or inline
  `source`. With inline `source`, `filename` (default `input.bp`) names the file
  in diagnostics and resolves includes against the working directory.
- `format` never writes. Apply the result yourself, or run `bp fmt --write`.
- `build` never passes `--force`, so the manifest safety check applies. It
  refuses a non-empty output directory that `bp` did not create.
- Results use absolute paths.

## Resources

| URI | Content |
| --- | --- |
| `blueprint://context` | Full agent-facing reference (same as `bp context`) |
| `blueprint://context/<topic>` | One topic for each `bp context` topic: `cli`, `codegen`, `errors`, `examples`, `language`, `overview`, `targets`, `workflow` |
| `blueprint://llms` | Complete agent and LLM guide (same as `bp llms`), `text/markdown` |

## Error behavior

- Diagnostics are a normal result. A `.bp` file with errors makes `check`
  return `success: false` with `isError: false`, so the agent reads the errors
  as data.
- A failed tool call sets `isError: true` with a text explanation. Examples:
  an unreadable path, a bad `target`, `explain` with an unknown code, or a
  `build` or `diff` that exits nonzero. For `build` and `diff`, the structured
  content carries `exit_code`, `stdout`, and `stderr`. `build` exit codes are
  1 (spec errors), 2 (usage, environment, or unsafe output directory), and 4
  (the target cannot generate this spec).
- Protocol errors are JSON-RPC errors. They cover malformed requests and
  unknown tool or resource names. Arguments that are not an object are also
  protocol errors. Bad argument values, including unknown fields, are tool
  errors.
- A handler panic becomes a tool error, so one bad call does not stop the
  server.

## Typical agent loop

1. Edit the `.bp` file.
2. Call `check` and fix every error before continuing.
3. Call `format`, then apply the formatted text.
4. Call `lint` and address the warnings that matter.
5. Call `build` to generate the project, then `diff` to confirm the output is
   up to date.

## Protocol notes

- Transport: stdio only, newline-delimited JSON-RPC 2.0. There is no HTTP mode.
- Protocol revisions: `2026-07-28` (stateless, with the protocol version carried
  in each request's `_meta`, and `server/discover`), plus the initialize-based
  `2025-11-25`, `2025-06-18`, `2025-03-26`, and `2024-11-05`.
- No network access is needed. The server reads local files and runs the
  compiler. Nothing is sent to any service.
