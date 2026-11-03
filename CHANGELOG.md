# Changelog

All notable changes to `archyl` are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[semantic versioning](https://semver.org).

## [Unreleased]

## [1.0.0]

First public release.

### Session

- `archyl` on its own opens a session: ask about the architecture in plain
  language and the assistant runs `impact`, `check`, `brief` and the rest,
  showing each one and asking before anything that writes. Native tool calling
  on OpenAI and Anthropic, a text protocol on other providers.
- `/` commands, `@file` attachments (the file and its architecture brief),
  `#element` mentions, multi-line input, `archyl -c` and `/resume`.

### Explore

- `map`, `search`, `show`, `where`, `impact`, `ask`, `adr`, `status`, `open`,
  `context`, `activity`, `discover` with a full-screen review of what it found.
- A local cache of the model per profile and project: completions, `map`,
  `show` and `open` answer at once.

### Coding agents

- `setup` for Claude Code, Cursor, VS Code and Codex; `plan`, `brief`,
  `remember`, `recall`, `wrap`, `guard`, `session`, `agents`, `runs`, `mcp`,
  `statusline`.

### Checks and CI

- `check` (GitHub annotations, GitLab Code Quality, SARIF), `review
  [--comment]`, `cr`, `radar [check]`, `hooks install`, `drift`,
  `dsl pull|diff|push`, `release`, and a GitHub Action.

### Account

- Browser sign-in (OAuth 2.1 + PKCE) stored in the OS keychain and refreshed
  safely across concurrent processes; profiles for several instances or
  organizations; `doctor`, `inbox`, `whoami`, `prompt`, `cache clear`.

### Requirements

- Archyl with the CLI endpoints (archyl.com, or a self-hosted instance from
  the release that ships them).

### Added

- `archyl projects` lists the projects you can reach.
- `archyl rules` lists the rules that apply to the project.
- `install.ps1` installs on Windows, and `install.sh` works in Git Bash.
- `archyl init --new --no-discover` works on a repository with no remote.
- `archyl diagram [element]`: draws a C4 view as an inline image in Kitty, Ghostty,
  WezTerm and iTerm2, as box-drawing text elsewhere, or as PNG, SVG or Mermaid
  with `--format` or `-o` (the extension picks the format). `--json` prints the
  layout. `ARCHYL_IMAGES=0` always draws text.
- `archyl diagram <element>` centres the diagram on the element, of any
  level (system, container, component or code), with everything its
  relationships reach both ways. `--depth` walks up to 3 hops; deeper
  elements are drawn as their ancestor at the element's level, and neighbours
  from elsewhere name their parent. `--inside` draws a system's containers or
  a container's components instead. `f` centres on the selection in
  `archyl map`, and the session's assistant draws both.
- `archyl tour [element]`: a guided walk through the codebase. The big
  picture first, then the parts worth meeting, in the order work flows: the
  systems when you own several, else the containers, from where outside
  systems send work in. Shared infrastructure (queues, caches, databases,
  storage) is listed in the big picture, never a stop. Archyl's AI writes the
  story from the model: how the system works, its main flows, each stop's
  role and a piece of work to follow through the code. Under it, each stop
  lists its key parts, what it uses and what uses it, who owns it, the files
  to read first, the decisions, what was remembered about it and the rules
  that guard it, with a small diagram. Full screen in a terminal; piped,
  Markdown for agents; `--markdown` adds a Mermaid diagram per stop for an
  ONBOARDING.md; `--no-ai` keeps to the model's facts; `--json`; `--stops`.
- `archyl history [from..to] [element]`: how the architecture changed
  between two points. A point is `now`, a version (`12`, `rev12`), a release
  as `archyl release` recorded it (the latest version at or before it), a
  date (`2026-09-01`) or a duration (`30d`, `2w`); either side of the range
  may be left out. The diff is computed in the CLI, so `now` works: elements
  match by ID, then by their path of names, and are listed as added, removed
  or modified (renamed, moved, retyped, redescribed); an element removed with
  its children counts once, and relationships removed with their ends are
  folded into it. The diagram merges both sides and marks each box and arrow
  (added in green with `+`, changed in amber with `~`, removed dashed and dim
  with `−`), in text, PNG, SVG and Mermaid. Piped, it prints Markdown for
  release notes; `--markdown` adds the diagram, `--format` and `-o` write the
  diagram alone, `--json` the whole report. `--play` replays every version of
  the range full screen, like the web app's time-lapse.
- `archyl incident <element>`: one fast, read-only screen for an element in
  trouble. Its owners (inherited from the closest owned ancestor), the blast
  radius grouped by distance with each dependent's owners, what it depends
  on, the latest model changes and releases, linked documentation, decisions
  and pitfalls, and everyone to notify. No AI; the calls run together under a
  short timeout and a failed source shows as unavailable. Markdown when piped,
  `--json`, `--slack` for a ready-to-paste message, `--copy`, `--depth`.
- `archyl decide ["what was decided"]`: drafts an Architecture Decision Record
  from what you just changed. It reads the branch diff, where the change sits
  in the architecture and the decisions already recorded; Archyl's AI writes
  context, decision, consequences and alternatives from those facts only. You
  review it or edit it in `$EDITOR`, then it is recorded as proposed and linked
  to the elements it is about. `--no-ai` starts from a template; `--print` and
  `--json` print the draft without creating anything unless `--yes`.
- `archyl codeowners`: generates the CODEOWNERS block from the owners
  recorded in Archyl and the folder each element lives in. Teams are written
  `@<org>/<team-slug>`, people as their email, with overrides in
  `.archyl.json`; lines outside the managed block are kept. `--write` updates
  the file after showing the diff, `--check` fails CI when it is out of date.
- `archyl map`: `d` shows the current level as a diagram. Tab and shift+tab
  select, arrows and h j k l scroll, enter drills down, backspace or `u` goes
  up, `o` opens in the web app, `y` copies the ID, `d` or esc goes back.
- `archyl impact --diagram`: the affected part of the architecture, with the
  touched elements highlighted.
- `archyl init --new`: creates a project for the repository, connects it from
  its git remote, runs an AI discovery you watch live, opens the triage, draws
  the result and offers `archyl setup`. Plain `init` offers it when no project
  matches. `--name`, `--team`, `--provider` (self-hosted remotes), `--branch`,
  `--no-discover`, `--no-setup`, `--yes`.
- `archyl why <file[:line]>`: git blame for the architecture. The file's
  elements, the decisions that apply, owners, remembered notes, the rules aimed
  at it and recent model changes; with a line, the commit that last changed it
  tied to the decisions made around it.
- `archyl review --diagram` prints the impact as a Mermaid flowchart, and
  `review --comment` now includes that diagram in the pull request comment.
- `archyl fix`: runs the conformance check, lets you pick violations and hands
  them to Claude Code or Codex (`--agent`) inside a work session, with the
  brief and the decisions that apply, then checks again. Never commits.
  `--severity`, `--yes`, `--print` for any other agent, `--json`, and
  `-- <command>` to run your own (the task is in `$ARCHYL_FIX_TASK_FILE`).
- `archyl lsp`: a Language Server over stdio. Conformance violations as
  diagnostics on open and save, the file's element on hover, "Ask Archyl why"
  and "Open in Archyl" code actions. Editor setup in `docs/lsp.md`.
- `archyl upgrade`: upgrades through Homebrew or `go install` when that is how
  archyl was installed, otherwise replaces itself with the release archive
  after checking `checksums.txt` (and its cosign signature when `cosign` is
  installed). `--check` only reports. The new-release notice suggests it.
- `archyl completion install`, also offered by `archyl setup`: writes the
  completion script where bash, zsh, fish or PowerShell loads it, and only
  edits a startup file when you agree.

- `archyl setup` offers to install shell completion (`--no-completion` skips
  it).
- `archyl dsl push` and `dsl diff` send every file the `include` directives of
  `archyl.yaml` reach, so a monorepo can keep one file per service. Includes
  resolve against the file declaring them and must stay in the repository; a
  missing one stops the command before anything is sent. Needs an Archyl
  server from the same release.

### Security

- A stored sign-in is only sent to the host it was made on. With `ARCHYL_HOST`
  pointing elsewhere, the CLI stops and says so; `ARCHYL_API_KEY` still works.
- Requests carrying a credential follow no redirect to another origin.
- `check`, `review`, `radar check` and `dsl` read regular files inside the
  repository only, and leave out files that look like secrets. `guard` and
  `lsp` skip those too. `check` lists what it did not read.
- Output no longer carries escape sequences from the model, files or AI answers,
  apart from colors.
- `guard` warnings leave the permission prompt in place instead of answering
  `allow`.
- `check` sends large changes in batches instead of cutting them at 500 files,
  reads paths with unusual characters, fails on an unknown severity and refuses
  an answer with no result.
- The session's write prompt takes `y` only, and shows the whole command or how
  many characters it cut.
- `upgrade` only accepts signatures made by the release workflow on a tag, and
  says when no signature was checked.
- `review --comment` escapes names and findings, and only edits its own comment.
- `codeowners` refuses handles that are not handles, and keeps names on one line.
- Only web addresses are opened in a browser.
- Briefing files have random names; code frames only read files of the repository.
- Writes refuse symbolic links in the repository.
- The assistant's text protocol stops after the action limit.
- `login` warns about plain-http hosts, and a stray callback no longer ends it.
- `init --new` creates projects and starts discoveries from the CLI. Run it
  against an Archyl server from the same release, which enforces the
  credential's organization when a project, whiteboard, share link, overlay
  or DSL import is created, and checks access to the repository before a
  discovery counts against the quota. Self-hosted instances should upgrade
  first.

### Fixed

- The session no longer leaves blank lines under the prompt after a long
  streamed answer.
- `archyl prompt` and the status line no longer start git to find the
  repository: about 7 ms faster on every shell prompt.

[Unreleased]: https://github.com/archyl-com/cli/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/archyl-com/cli/releases/tag/v1.0.0
