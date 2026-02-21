# Blueprint Language Support for VS Code

Syntax highlighting and language support for the [Blueprint](https://github.com/abdul-hamid-achik/blueprint) language (`.bp` files).

## Features

- Full syntax highlighting for all Blueprint constructs
- Bracket matching and auto-closing pairs (`{}`, `[]`, `()`, `""`)
- Comment toggling with `Ctrl+/` / `Cmd+/`
- Smart indentation — auto-indents after `{`
- String interpolation `{expr}` highlighted inside strings

## Installation

### Option 1: Install from VSIX (recommended)

Build and install the extension package:

```bash
cd editors/vscode-blueprint
npm install -g @vscode/vsce
vsce package
code --install-extension blueprint-language-0.1.0.vsix
```

### Option 2: Symlink (development)

```bash
ln -s "$(pwd)/editors/vscode-blueprint" \
  "$HOME/.vscode/extensions/blueprint-language"
```

Reload VS Code (`Ctrl+Shift+P` → **Developer: Reload Window**).

### Option 3: Copy to extensions folder

```bash
cp -r editors/vscode-blueprint \
  "$HOME/.vscode/extensions/blueprint-language"
```

Restart VS Code.

## What Gets Highlighted

| Construct | Example | Scope |
|-----------|---------|-------|
| `@>` LLM slot | `@> implement pricing` | `keyword.operator.llm-slot` |
| `@` intent | `@ "Create a user"` | `keyword.operator.intent` |
| `<-` input | `<- name string required` | `keyword.operator.input` |
| `\|>` step | `\|> user = fetch user(id)` | `keyword.operator.step` |
| `->` output | `-> 200 { id: user.id }` | `keyword.operator.output` |
| HTTP methods | `GET POST PUT PATCH DELETE` | `support.function.http-method` |
| Declarations | `blueprint model fn pipe` | `keyword.declaration` |
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
├── package.json                  — extension manifest
├── language-configuration.json  — brackets, comments, indent rules
├── syntaxes/
│   └── bp.tmLanguage.json        — TextMate grammar
└── README.md
```
