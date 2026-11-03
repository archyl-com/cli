# archyl lsp

`archyl lsp` is a Language Server over stdio. Any editor with an LSP client can
run it next to the language's own server.

| Feature | What you get |
| --- | --- |
| Diagnostics | The project's conformance rules, run on a file when you open or save it. Critical and high findings are errors, medium are warnings, low are information. Each one carries the rule name and the suggestion. |
| Hover | The element the file belongs to: its path in the C4 model, description, owners, how many elements depend on it, the decisions (ADRs) that apply, and a link to it in Archyl. Cached per file for 10 minutes, refreshed on save. |
| Code actions | **Ask Archyl why** asks the Archyl assistant why the rule exists (on a diagnostic) or why the file is built the way it is, and opens the answer as a markdown document. **Open in Archyl** opens the element in the browser. |

It uses the CLI's session (`archyl login`, or `ARCHYL_API_KEY`) and the
repository's `.archyl.json` (`archyl init`), resolved from the workspace root
the editor sends. `ARCHYL_PROJECT_ID` overrides the project.

The server never takes the editor down. When Archyl cannot be reached, or the
repository is not linked, it writes one warning to the editor's LSP log and
shows no diagnostics; it logs again only after it has worked in between.

## Protocol

The server implements this subset of LSP 3.17:

- `initialize`, `initialized`, `shutdown`, `exit`
- `textDocument/didOpen`, `didSave` (with `includeText`), `didClose`. Edits are
  not synced: files are checked as saved.
- `textDocument/publishDiagnostics` (source `archyl`), debounced by 300 ms per file
- `textDocument/hover` (markdown)
- `textDocument/codeAction`, returning commands
- `workspace/executeCommand`: `archyl.why` and `archyl.open`
- `window/showDocument` to open answers and links, falling back to
  `window/showMessage` when the client does not support it, and
  `window/logMessage` for warnings

Other requests get a `MethodNotFound` error; other notifications are ignored.

## VS Code

VS Code has no built-in generic client. Install
[Generic LSP Client](https://marketplace.visualstudio.com/items?itemName=llllvvuu.llllvvuu-glspc)
(`llllvvuu.llllvvuu-glspc`), then in the workspace's `.vscode/settings.json`:

```json
{
  "glspc.serverCommand": "archyl",
  "glspc.serverCommandArguments": ["lsp"],
  "glspc.languageId": "go"
}
```

That extension attaches one server to one language ID: set `glspc.languageId`
to the main language of the repository (`typescript`, `python`, `java`…). If
VS Code was not started from a terminal and cannot find `archyl`, give the full
path in `glspc.serverCommand`, or extend `PATH` with
`"glspc.environmentVariables": { "PATH": "/opt/homebrew/bin:$PATH" }`.

## Neovim

Neovim 0.11 and later:

```lua
vim.lsp.config("archyl", {
  cmd = { "archyl", "lsp" },
  root_markers = { ".archyl.json", ".git" },
  filetypes = { "go", "typescript", "typescriptreact", "javascript", "python", "java", "kotlin", "rust" },
})
vim.lsp.enable("archyl")
```

Earlier versions, from an autocommand:

```lua
vim.api.nvim_create_autocmd("FileType", {
  pattern = { "go", "typescript", "python", "java", "rust" },
  callback = function(args)
    vim.lsp.start({
      name = "archyl",
      cmd = { "archyl", "lsp" },
      root_dir = vim.fs.root(args.buf, { ".archyl.json", ".git" }),
    })
  end,
})
```

With [nvim-lspconfig](https://github.com/neovim/nvim-lspconfig) before 0.11,
register it as a custom server:

```lua
local configs = require("lspconfig.configs")
if not configs.archyl then
  configs.archyl = {
    default_config = {
      cmd = { "archyl", "lsp" },
      filetypes = { "go", "typescript", "python", "java", "rust" },
      root_dir = require("lspconfig.util").root_pattern(".archyl.json", ".git"),
    },
  }
end
require("lspconfig").archyl.setup({})
```

Hover with `K`, code actions with `gra` (0.11) or `vim.lsp.buf.code_action()`.
Neovim supports `window/showDocument`, so answers open in a buffer and links in
the browser.

## Helix

In `languages.toml` (`~/.config/helix/` or the repository's `.helix/`), declare
the server and add it after the language's own:

```toml
[language-server.archyl]
command = "archyl"
args = ["lsp"]

[[language]]
name = "go"
language-servers = ["gopls", "archyl"]

[[language]]
name = "typescript"
language-servers = ["typescript-language-server", "archyl"]
```

Hover with `space k`, code actions with `space a`. A Helix build without
`window/showDocument` support receives answers and links as messages.

## Zed

Zed runs a language server only through an extension; settings alone can change
the binary of a known server but not add a new one. A dev extension takes three
files:

`extension.toml`

```toml
id = "archyl"
name = "Archyl"
version = "0.1.0"
schema_version = 1
authors = ["You <you@example.com>"]
description = "Archyl architecture diagnostics and hovers"

[language_servers.archyl]
name = "Archyl"
languages = ["Go", "TypeScript", "TSX", "JavaScript", "Python", "Rust", "Java"]
```

`Cargo.toml`

```toml
[package]
name = "zed-archyl"
version = "0.1.0"
edition = "2021"

[lib]
crate-type = ["cdylib"]

[dependencies]
zed_extension_api = "*" # pin the current release
```

`src/lib.rs`

```rust
use zed_extension_api::{self as zed, LanguageServerId, Result};

struct Archyl;

impl zed::Extension for Archyl {
    fn new() -> Self {
        Archyl
    }

    fn language_server_command(&mut self, _id: &LanguageServerId, worktree: &zed::Worktree) -> Result<zed::Command> {
        let command = worktree.which("archyl").ok_or("archyl is not on PATH")?;
        Ok(zed::Command { command, args: vec!["lsp".into()], env: Default::default() })
    }
}

zed::register_extension!(Archyl);
```

Install it with **zed: install dev extension**, pointing at the folder. Zed
starts the server for the listed languages next to their own.

## Troubleshooting

- Nothing shows up: run `archyl doctor` in the repository. The editor's LSP log
  (`:lua vim.cmd.edit(vim.lsp.get_log_path())` in Neovim, `:log-open` in Helix, the "Generic LSP Client" output
  channel in VS Code) holds the server's one warning.
- Diagnostics lag: they follow saves, not keystrokes, and each check is a call
  to Archyl.
- A file outside the repository, or one Archyl has no element for, gets no
  hover.
