# nvim-blueprint

Neovim syntax plugin for the [Blueprint](https://github.com/abdul-hamid-achik/blueprint) language (`.bp` files).

## Features

- Syntax highlighting for all Blueprint constructs — arrows, keywords, types, MIME types, path params, string interpolation
- Smart indentation (2-space, indent after `{`)
- Comment support (`#`) — works with `gcc` / `gc` from vim-commentary
- File type detection for `.bp` files

## Installation

### lazy.nvim

```lua
{
  "abdul-hamid-achik/blueprint",
  config = function()
    vim.opt.rtp:prepend(
      vim.fn.stdpath("data") .. "/lazy/blueprint/editors/nvim-blueprint"
    )
  end,
  ft = "bp",
}
```

Or, if you cloned the repo locally:

```lua
{
  dir = "/path/to/blueprint",
  config = function()
    vim.opt.rtp:prepend("/path/to/blueprint/editors/nvim-blueprint")
  end,
  ft = "bp",
}
```

### vim-plug

```vim
Plug 'abdul-hamid-achik/blueprint', { 'rtp': 'editors/nvim-blueprint' }
```

### Manual

Copy the directories into your Neovim config root:

```bash
NVIM_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/nvim"
PLUGIN_DIR="/path/to/blueprint/editors/nvim-blueprint"

cp -r "$PLUGIN_DIR/ftdetect" "$NVIM_CONFIG/"
cp -r "$PLUGIN_DIR/ftplugin" "$NVIM_CONFIG/"
cp -r "$PLUGIN_DIR/syntax"   "$NVIM_CONFIG/"
cp -r "$PLUGIN_DIR/indent"   "$NVIM_CONFIG/"
```

### Packer

```lua
use {
  'abdul-hamid-achik/blueprint',
  rtp = 'editors/nvim-blueprint',
}
```

## What Gets Highlighted

| Construct | Example | Highlight group |
|-----------|---------|-----------------|
| `<-` input arrow | `<- name string required` | `Special` |
| `\|>` step arrow | `\|> user = fetch user(id)` | `Operator` |
| `->` output arrow | `-> 200 { id: user.id }` | `Keyword` |
| `@` intent | `@ "Create a user"` | `PreProc` |
| `@>` LLM slot | `@> implement this` | `PreProc` |
| HTTP methods | `GET POST PUT PATCH DELETE STREAM WS` | `Function` |
| Declaration keywords | `blueprint model fn pipe middleware` | `Keyword` |
| Control flow | `guard when try recover` | `Conditional` |
| Data ops | `fetch query save update delete` | `Function` |
| Built-in types | `string int bool uuid timestamp` | `Type` |
| Constraints | `required optional primary unique` | `StorageClass` |
| String literals | `"Hello, {name}!"` | `String` |
| Interpolations | `{name}` inside strings | `Identifier` |
| MIME types | `image/png application/pdf` | `Constant` |
| Path params | `:id :user_id` | `Identifier` |
| Numbers with units | `10mb 30days 60/min` | `Number` |
| Comments | `# this is a comment` | `Comment` |

## File Structure

```
nvim-blueprint/
├── ftdetect/bp.vim     — registers .bp as filetype "bp"
├── ftplugin/bp.vim     — 2-space indent, # comment string
├── syntax/bp.vim       — full syntax highlighting
└── indent/bp.vim       — smart indentation rules
```
