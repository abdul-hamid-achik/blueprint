# Blueprint Language Support for VS Code

Editor and language-server support for the [Blueprint](https://github.com/abdul-hamid-achik/blueprint) language (`.bp` files).

## Features

- Full syntax highlighting for all Blueprint constructs
- Parser and semantic diagnostics while you type
- Context-aware completion for declarations, types, constraints, models,
  middleware, steps, local values, environment names, and model fields
- Hover and go-to-definition for Blueprint symbols
- Workspace symbol search across `.bp` files
- Bracket matching and auto-closing pairs (`{}`, `[]`, `()`, `""`)
- Comment toggling with `Ctrl+/` / `Cmd+/`
- Smart indentation — auto-indents after `{`
- String interpolation `{expr}` highlighted inside strings

The extension starts `bp lsp` automatically when a Blueprint file opens.

## Prerequisite

Install the Blueprint CLI and make sure VS Code can find it:

```bash
bp version
```

If `bp` is not on VS Code's `PATH`, set **Blueprint › Server: Path** to the
absolute executable path. **Blueprint: Restart Language Server** applies a new
path or restarts a stopped server.

## Installation

### Option 1: Install from VSIX (recommended)

Build and install the extension package:

```bash
cd editors/vscode-blueprint
npm install
npx @vscode/vsce package
code --install-extension blueprint-language-0.2.0.vsix
```

### Option 2: Symlink (development)

```bash
cd editors/vscode-blueprint
npm install
ln -s "$(pwd)" \
  "$HOME/.vscode/extensions/blueprint-language"
```

Reload VS Code (`Ctrl+Shift+P` → **Developer: Reload Window**).

### Option 3: Copy to extensions folder

```bash
cd editors/vscode-blueprint
npm install
cp -r . \
  "$HOME/.vscode/extensions/blueprint-language"
```

Restart VS Code.

## Settings

```json
{
  "blueprint.server.path": "bp",
  "blueprint.server.args": ["lsp"]
}
```

The defaults above work for the normal Blueprint CLI. `server.args` can be
empty when `server.path` points to a dedicated wrapper that starts the language
server directly. Both settings are machine-overridable, so remote workspaces
can select the executable installed on the remote host.

## Development checks

```bash
npm test
npm run check
```

## What Gets Highlighted

| Construct | Example | Scope |
|-----------|---------|-------|
| `@>` LLM slot | `@> implement pricing` | `keyword.operator.llm-slot` |
| `@` intent | `@ "Create a user"` | `keyword.operator.intent` |
| `<-` input | `<- name string required` | `keyword.operator.input` |
| `\|>` step | `\|> user = fetch user(id)` | `keyword.operator.step` |
| `->` output | `-> 200 { id: user.id }` | `keyword.operator.output` |
| HTTP methods | `GET POST PUT PATCH DELETE` | `support.function.http-method` |
| Declarations | `blueprint model computed fn pipe` | `keyword.declaration` |
| Control flow | `guard when try recover` | `keyword.control` |
| Data ops | `fetch query save update` | `support.function.data-operation` |
| Types | `string int bool uuid` | `storage.type` |
| Constraints | `required optional primary` | `storage.modifier.constraint` |
| String literals | `"Hello, {name}!"` | `string.quoted.double` |
| Interpolations | `{name}` inside strings | `variable.other.interpolated` |
| MIME types | `image/png application/pdf` | `constant.other.mime-type` |
| Path params | `:id :user_id` | `variable.other.path-parameter` |
| Numbers + units | `10mb 30days 60/min` | `constant.numeric` |
| Comments | `# comment` | `comment.line.number-sign` |

## File Structure

```
vscode-blueprint/
├── extension.js                 — stdio language-client lifecycle
├── client-config.js             — validated server settings
├── package.json                 — extension manifest
├── LICENSE                      — MIT package license
├── language-configuration.json  — brackets, comments, indent rules
├── syntaxes/
│   └── bp.tmLanguage.json        — TextMate grammar
├── test/                         — dependency-free client config tests
└── README.md
```
