# Demo recordings

The GIFs in the main README are recorded with [VHS](https://github.com/charmbracelet/vhs)
from the tapes in this directory:

| Tape | GIF | Shows |
| --- | --- | --- |
| `session.tape` | `session.gif` | A bare `archyl` session: a question in plain language, the assistant running `archyl impact`, then a `#element` mention |
| `map.tape` | `map.gif` | `archyl map`: open the system, `d` for its diagram, tab between boxes, enter to drill into a container, `d` back to the tree |
| `diagram.tape` | `diagram.gif` | `archyl diagram` (system context), `archyl diagram Backend --inside`, then `archyl impact --diagram` |
| `why.tape` | `why.gif` | `archyl why <file>:<line>`: elements, owners, decisions, what was remembered, rules |
| `check.tape` | `check.gif` | `archyl impact`, then `archyl check` |

`diagram.png` is not a tape: it is `archyl diagram Backend --inside -o diagram.png` run
with `ARCHYL_THEME=dark`.

## Re-recording

You need `vhs` (it pulls in `ttyd` and `ffmpeg`), an `archyl` binary on your
`PATH`, and an Archyl instance with a project to point at. Use a local or demo
instance: the recordings show element names, branch names and owners.

The tapes read two variables and never print them:

- `ARCHYL_DEMO_ENV`: a shell file the tape sources before recording. It sets
  the connection and keeps the output stable:

  ```sh
  export ARCHYL_HOST=http://localhost:8080
  export ARCHYL_API_KEY=arch_...            # a temporary key, delete it afterwards
  export ARCHYL_PROJECT_ID=<project id>
  export ARCHYL_CONFIG_DIR=/tmp/archyl-demo-config  # keeps your own profile out of it
  export ARCHYL_NO_UPDATE_NOTIFIER=1
  export ARCHYL_THEME=dark
  export ARCHYL_IMAGES=0                    # VHS is xterm.js: no image protocol, draw diagrams as text
  ```

  Build the binary with a version stamped, or the session's welcome says
  `dev`: `go build -ldflags "-X github.com/archyl-com/cli/internal/buildinfo.Version=1.0.0" ./cmd/archyl`.

  When the API key belongs to an organization, put a `config.yaml` with that
  `organization_id` in `ARCHYL_CONFIG_DIR` (`archyl whoami` should succeed).

- `ARCHYL_DEMO_REPO`: a throwaway git repository on a branch ahead of
  `origin/main`, whose changed files map to elements of the project
  (`archyl where <path>` should name one). `session.tape` and `check.tape` show
  what that branch touches, so it should change a few files owned by teams, and
  one of them should break a conformance rule for `archyl check` to report.
  `why.tape` points at `backend/internal/adapter/http/handlers/harness.go:620`;
  it shows more when an ADR is linked to that file's element and something was
  remembered on it (`archyl remember … --element "HTTP Handlers"`).

The session tape asks the AI assistant a question, so the project's plan must
include AI chat. Its answer differs from run to run: watch the result before
committing it.

```sh
export ARCHYL_DEMO_ENV=/path/to/demo.env ARCHYL_DEMO_REPO=/path/to/demo-repo
vhs session.tape
vhs map.tape
vhs check.tape
vhs diagram.tape
vhs why.tape
```

Keep each GIF under 2 MB (`ls -lh *.gif`). If one grows past that, lower
`Framerate` or shorten the `Sleep`s in its tape.
