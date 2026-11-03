# Contributing

Thanks for helping. Issues and pull requests are welcome. For anything larger
than a fix, open an issue first so we can agree on the shape before you write
the code. Security problems go through [SECURITY.md](SECURITY.md), never a
public issue. Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Setup

You need Go 1.26 or later and git.

```sh
git clone https://github.com/archyl-com/cli && cd cli
go run ./cmd/archyl --help
go build -o archyl ./cmd/archyl
```

To run it against a local Archyl instead of archyl.com:

```sh
./archyl login --host http://localhost:8080
# or, without a browser
ARCHYL_HOST=http://localhost:8080 ARCHYL_API_KEY=arch_… ARCHYL_PROJECT_ID=… ./archyl status
```

## Before you open a pull request

```sh
go vet ./...
go test -race ./...
golangci-lint run
```

`go test ./...` includes `cmd/archyl`'s smoke test, which builds the binary and
drives it against a fake API; `go test -short ./...` skips it.

- Tests are table-driven and use testify. Isolate them from your machine:
  `t.Setenv` for `HOME`, `ARCHYL_CONFIG_DIR` and `ARCHYL_NO_KEYRING`, and
  `GIT_CONFIG_GLOBAL=/dev/null` for git fixtures (a global signing config
  otherwise hangs commits).
- Full-screen views and the session are Bubble Tea models. Their tests send
  `tea.KeyPressMsg` values and assert on `ansi.Strip(View().Content)`.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`…): the release notes are built from them.

## Conventions

- **Two output rules.** Results go to stdout, status and spinners to stderr.
  Anything a script may want has a `--json` form.
- **Agents read the output too.** Commands an agent runs (`plan`, `brief`,
  `recall`, `impact`, `check`) print compact markdown when stdout is not a
  terminal.
- **Flat UI.** No rounded borders; panes are split by a hairline on the left;
  the selection is the accent color with `›`. Colors come from the theme in
  `internal/ui`, never raw ANSI.
- **Writes ask first.** A command that changes the model asks in a terminal
  and takes `--yes`; the session's assistant asks before every write.
- **Public API only.** The CLI talks to the Archyl REST API (`internal/api`).
  If a command needs an endpoint that does not exist, open an issue first.
- **Errors say what to do.** Wrap with `%w`, and end user-facing errors with
  the command that fixes them when there is one.

## Layout

| Package | Role |
| --- | --- |
| `cmd/archyl` | entry point and the smoke test |
| `internal/commands` | the command tree |
| `internal/shell` | the interactive session: input, suggestions, the assistant and its tool calls, resume |
| `internal/tui` | full-screen views: map, search, runs, agents, discovery triage, streamed answers |
| `internal/ui` | theme, printer, logo and components |
| `internal/api` | typed REST client, including the chat stream |
| `internal/auth`, `internal/session` | OAuth 2.1 + PKCE sign-in, refresh across processes, environment overrides |
| `internal/config`, `internal/credentials` | profiles on disk, secrets in the keychain or a 0600 file |
| `internal/cache` | on-disk cache: the C4 model per profile and project, the prompt segment |
| `internal/repo` | git and `.archyl.json` |
| `internal/graph` | the C4 model indexed for navigation |
| `internal/agents`, `internal/guard`, `internal/wrap`, `internal/mcpproxy` | coding-agent setup, the PreToolUse hook, work sessions, the stdio MCP proxy |
| `internal/runstate`, `internal/activity` | managed agent runs and the activity feed |
| `internal/ci`, `internal/report`, `internal/prcomment` | CI detection, report formats (annotations, Code Quality, SARIF), pull request comments |
| `internal/radar`, `internal/weblink`, `internal/update`, `internal/buildinfo` | radar checks, web app links, release notices, version |

## Releases

Maintainers tag `vX.Y.Z` on `main`. GoReleaser builds the binaries, signs the
checksums with cosign, attaches an SBOM, publishes the GitHub release and
updates the Homebrew formula. The `v1` tag is then moved to the new release so
`uses: archyl-com/cli@v1` picks it up. Add the release to
[CHANGELOG.md](CHANGELOG.md) before tagging.
