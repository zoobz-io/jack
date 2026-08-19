# jack

[![CI Status](https://github.com/zoobzio/jack/workflows/CI/badge.svg)](https://github.com/zoobzio/jack/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/zoobzio/jack/graph/badge.svg?branch=main)](https://codecov.io/gh/zoobzio/jack)
[![Go Report Card](https://goreportcard.com/badge/github.com/zoobzio/jack)](https://goreportcard.com/report/github.com/zoobzio/jack)
[![CodeQL](https://github.com/zoobzio/jack/workflows/CodeQL/badge.svg)](https://github.com/zoobzio/jack/security/code-scanning)
[![Go Reference](https://pkg.go.dev/badge/github.com/zoobzio/jack.svg)](https://pkg.go.dev/github.com/zoobzio/jack)
[![License](https://img.shields.io/github/license/zoobzio/jack)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/zoobzio/jack)](go.mod)
[![Release](https://img.shields.io/github/v/release/zoobzio/jack)](https://github.com/zoobzio/jack/releases)

**An operator console for running multiple Claude Code agents, each in its own container, each with its own identity.**

jack gives every agent an isolated Docker container, a dedicated tmux session, its own git/GitHub identity, and its own Claude Code configuration. You clone a repo *for an agent*, then drop *into* that agent's session — a real `claude` running against a real checkout, sandboxed away from your host and from every other agent.

That's the whole job. jack does not manage secrets, message-passing, or the GitHub API — the agent handles those from inside its container. jack builds the box, wires up the identity, and gets you a session.

---

## Concepts

| Term | Meaning |
|------|---------|
| **Agent** | A named identity (`alex`, `scout`, …), defined by a **profile** in your config. It carries a git identity, a Claude "soul" (`CLAUDE.md` + slash commands), and optional certificate identity. An agent name may not contain `-`. |
| **Profile** | The config block for an agent: its git name/email, GitHub user, default model, permission mode, and any supporting repos. |
| **Session** | A tmux session (`<agent>-<repo>`) attached to a `claude` process running inside the agent's container. |
| **Container** | One Docker container per agent-repo pair (`jack-<agent>-<repo>`), built from jack's base image. |
| **Registry** | jack's record of which repos have been cloned for which agents (`~/.jack/registry.yaml`). |

The unit of work is an **agent + repo**. Everything jack names and manages derives from that pair.

---

## How it works

```
  host                                  docker container  (jack-alex-myapp)
  ────                                  ─────────────────────────────────────
  jack in --agent alex --project myapp
        │
        ├─ docker run  ───────────────▶  node:22-slim + git + step-cli
        │                                + @anthropic-ai/claude-code
        │                                  /root/workspace/myapp   ◀── your clone (rw)
        │                                  /root/workspace/.claude  ◀── agent config (ro)
        │                                  /root/.claude(.json)     ◀── agent's Claude state (rw)
        │                                  /root/.jack/bin          ◀── tools volume (persists)
        │
        ├─ docker exec (cert bootstrap, setup scripts)
        │
        └─ tmux new-session ──────────▶  docker exec -it claude [--permission flags]
                 │
             you attach here
```

1. **`clone`** builds the base image, clones the repo into `~/.jack/<agent>/<repo>`, sets the agent's git identity in the checkout, copies the agent's config into place, and records the pair in the registry.
2. **`in`** seeds the agent's private Claude state from your host login on first use (credentials shared via hard link; history and memory stay per-agent), starts the container (if needed), bootstraps a certificate when a CA is configured, runs any setup scripts, then launches `claude` inside a tmux session and attaches you to it.
3. **`out`** / **`kill`** tear the session (and optionally everything else) back down.

---

## Prerequisites

jack shells out to these tools on the **host**:

- [Docker](https://docs.docker.com/get-docker/) — builds and runs the agent containers
- [tmux](https://github.com/tmux/tmux) — hosts the interactive sessions
- [git](https://git-scm.com/) — clones repos

Optional:

- A [smallstep](https://smallstep.com/) certificate authority — for mTLS agent identity (`step` runs *inside* the container; you only need a reachable CA)

To build from source you need **Go 1.24+**.

---

## Installation

```sh
# From source, latest tagged release:
go install github.com/zoobzio/jack/cmd/jack@latest

# Or clone and build:
git clone https://github.com/zoobzio/jack
cd jack
make install          # go install ./cmd/jack
```

Prebuilt binaries for Linux and macOS (amd64/arm64) are attached to each [release](https://github.com/zoobzio/jack/releases).

---

## Configuration

jack reads a single config file, `~/.config/jack/config.yaml`, plus an optional tree of per-agent and per-project scripts alongside it. Override the locations with `JACK_CONFIG_DIR` and `JACK_DATA_DIR` (both must be absolute paths).

Run [`jack init`](#set-up-jack) to generate this file and the surrounding tree automatically — the rest of this section describes what it produces.

### `config.yaml`

```yaml
# Optional top-level defaults, used when a profile doesn't set its own.
model: claude-opus-4-8            # ANTHROPIC_MODEL for the agent's claude
permission: acceptEdits           # default | acceptEdits | bypassPermissions

# Optional: mTLS identity for agents (issued inside the container via step-cli).
ca:
  url: https://ca.internal:9000
  fingerprint: <root-ca-fingerprint>
  provisioner: jack

# At least one profile is required. The key is the agent name (no '-').
profiles:
  alex:
    git:
      name: Alexander Thorwaldson
      email: alex@zoobz.io
    github:
      user: zoobzio
    model: claude-opus-4-8         # optional per-agent override
    permission: bypassPermissions  # optional per-agent override
    repos:                         # optional supporting repos, mounted at /repos/<name>
      - https://github.com/zoobzio/pipz

  scout:
    git:
      name: Scout
      email: scout@zoobz.io
    github:
      user: zoobzio-scout
```

**Permission modes** map to `claude` launch flags:

| Mode | Effect | Flag |
|------|--------|------|
| `default` (or unset) | Prompts before edits and commands | *(none)* |
| `acceptEdits` | Auto-accepts edits, still gates commands | `--permission-mode acceptEdits` |
| `bypassPermissions` | Skips all checks — reasonable inside jack's isolated containers | `--dangerously-skip-permissions` |

### Config directory layout

```
~/.config/jack/
├── config.yaml               # profiles + optional ca/model/permission
├── setup.sh                  # optional: global setup, runs on every fresh container
├── agents/
│   └── <agent>/
│       ├── CLAUDE.md         # the agent's "soul"
│       ├── commands/         # slash commands
│       └── setup.sh          # optional: per-agent setup
└── projects/
    └── <repo>/
        └── dev.sh            # optional: per-project toolchain setup
```

The `agents/<agent>/` directory is **copied** into the agent's workspace and bind-mounted read-only one level above the checkout, so Claude Code's directory-inheritance merges the agent's config with any `.claude` in the repo itself.

Setup scripts run in order on each fresh container — **global → agent → project** — and only if the corresponding host file exists. jack deliberately has no opinion about what tools an agent needs; that belongs in `dev.sh`.

### Data directory layout

jack manages this tree itself; you don't edit it by hand:

```
~/.jack/
├── registry.yaml             # which repos are cloned for which agents
└── <agent>/
    ├── .claude/              # agent config, copied from ~/.config/jack/agents/<agent>/
    ├── claude/               # agent's private Claude state (history, memory);
    │                         #   credentials seeded from ~/.claude by hard link
    ├── claude.json           # agent's claude.json, seeded with account keys only
    └── <repo>/               # the clone (mounted rw into the container)
```

The `claude/` state is what makes agents Claude-deep identities rather than just git identities: each agent accumulates its own history and project memory, invisible to your host session and to other agents. Only the login is shared — the credentials file is hard-linked to the host's, so a token refresh on either side stays in sync. If a credential ever drifts (e.g. the link degraded to a copy across filesystems and a rotation broke it), `jack in --reseed` relinks it from the host login without touching the agent's memory.

---

## Usage

```
jack init  [--agent] [--git-name] [--git-email] [--github] [--build]  Scaffold config
jack clone <url> --agent <name>...   Clone a repo into one or more agents' workspaces
jack in    [--agent] [--project] [--reseed]  Enter (attach or create) a session
jack out   [name | --agent --project]  Terminate a session and stop its container
jack kill  [--agent] [--project]     Tear down everything for an agent-repo
jack status                          Show agents, sessions, and containers
```

Commands that address an existing agent-repo (`in`, `kill`) resolve missing `--agent`/`--project` flags from the registry — automatically when there's one option, interactively when there's more than one.

### Set up jack

```sh
jack init                          # prompt for anything not given (agent, git identity, GitHub user)
jack init --agent alex --github zoobzio   # take those from flags, prompt only for the rest
jack init --agent alex --git-name "Alex T" --git-email alex@zoobz.io --github zoobzio  # fully non-interactive
jack init --build                  # also build the base Docker image now
```

`init` is the first thing you run on a new machine. It checks that `docker`, `tmux`, and `git` are installed, then creates the config tree — `~/.config/jack/` with a starter `config.yaml`, an `agents/<name>/CLAUDE.md`, and a `projects/` dir — plus the `~/.jack/` data dir. `init` never overwrites files that already exist, so it is safe to re-run.

Values come from flags first. Anything you don't pass is filled in interactively when you're at a terminal — a short prompt for the agent name, git identity, and GitHub user, each **prefilled** from your global git identity (`git config --global user.name`/`user.email`) so you usually just confirm. When there's no terminal (scripts, CI) it skips the prompts and uses those seeded defaults, so passing every value via flags makes `init` fully non-interactive.

### Clone a repo for an agent

```sh
# Clone for a single agent:
jack clone https://github.com/zoobzio/myapp --agent alex

# Clone for several agents at once (flag is repeatable):
jack clone https://github.com/zoobzio/myapp -a alex -a scout

# Replace an existing clone (kills its session first):
jack clone https://github.com/zoobzio/myapp -a alex --force
```

### Enter a session

```sh
jack in --agent alex --project myapp    # explicit
jack in                                 # pick agent + project interactively
jack in -a alex -p myapp --reseed       # also relink the agent's Claude credentials from the host
```

`in` starts the container if it isn't running — seeding the agent's private Claude state on first use, then bootstrapping the agent's certificate (when a CA is configured) and running setup scripts — launches `claude` in the agent's permission mode, and attaches you. If the session already exists, it just re-attaches.

### Leave or tear down

```sh
jack out myapp-session-name       # kill session + stop container, by session name
jack out -a alex -p myapp         # …or by agent/project

jack kill -a alex -p myapp        # full teardown, with a confirmation prompt
jack kill -a alex -p myapp -f     # skip the prompt
```

`out` stops the container but keeps the clone and tools volume, so you can `jack in` again cheaply. `kill` removes **everything** jack created for the pair — session, container, tools volume, on-disk clone, and registry entry — erasing the agent's memories and any uncommitted local changes. Killing an agent's **last** repo also removes the rest of its directory, including its private Claude state; your host login is untouched.

### Check status

```sh
jack status
```

```
alex
PROJECT  SESSION       STATUS    CONTAINER
myapp    alex-myapp    attached  running
pipz     -             -         stopped

scout
PROJECT  SESSION       STATUS    CONTAINER
myapp    scout-myapp   idle 12m  running
```

---

## Under the hood

### Container layout

The base image is `node:22-slim` plus `git`, `curl`, the smallstep `step` CLI, and `@anthropic-ai/claude-code`. Everything hangs off `/root`:

```
/root/
├── .claude               ← ~/.jack/<agent>/claude       (agent's Claude state, rw)
├── .claude.json          ← ~/.jack/<agent>/claude.json  (rw)
├── .config/jack          ← ~/.config/jack     (read-only, for setup scripts)
├── .jack/
│   ├── bin               ← named tools volume (persists across sessions)
│   └── certs             ← cert.pem / key.pem (issued at bootstrap)
└── workspace/
    ├── .claude           ← agent config       (read-only, inherited by claude)
    └── <repo>            ← the clone           (rw, WORKDIR of the session)
```

Supporting repos listed under a profile's `repos:` are mounted read-write at `/repos/<name>` when present on disk.

### Naming

All names derive from the agent-repo identity, so every part of the system agrees on them:

- Container: `jack-<agent>-<repo>`
- Session: `<agent>-<repo>`
- Tools volume: `jack-<agent>-<repo>-tools`

Because `-` joins these parts, **agent names may not contain `-`** (a repo name may — the session name splits on the *first* hyphen).

### Certificate identity (optional)

When a `ca:` block is configured, `jack in` execs a bootstrap script in the fresh container that runs `step ca bootstrap`, issues a certificate for the agent at `/root/.jack/certs/`, and starts `step ca renew --daemon` to keep it fresh for the life of the container. The CA coordinates are passed in as `JACK_CA_*` environment variables.

---

## Architecture

jack keeps its pure logic separate from the code that touches the outside world, which is what makes it testable without Docker, tmux, or git present.

| Package | Responsibility |
|---------|----------------|
| `cmd/jack` | Entry point — wires the app with real boundaries and registers command handlers. |
| `domain` | Pure value types with self-contained validation: `Agent`, `Repo`, `Identity`, `Session`, and the container layout. |
| `config` | Loads/validates `config.yaml`, resolves host paths (`Env`), tracks clones (`Registry`), and copies agent config into workspaces. |
| `core` | Wires the `App`; defines the `Docker`, `Tmux`, and `Git` boundaries (interfaces over the host CLIs) and builds the container `Spec`. |
| `tools` | Builds the shell commands run inside a container (cert bootstrap, setup scripts) — pure, no I/O. |
| `handler` | The cobra command handlers (`clone`, `in`, `out`, `kill`, `status`) and the interactive resolver. |

The `Docker`/`Tmux`/`Git` interfaces are the only things that shell out; everything else is deterministic and unit-tested with fakes. The Dockerfile is owned by jack and embedded as a constant in `core/docker.go`.

---

## Development

```sh
make build      # build ./bin/jack
make test       # go test -race across all packages
make lint       # golangci-lint (config in .golangci.yml)
make security   # gosec scan
make check      # lint + test + security  (what CI gates on)
make ci         # check + coverage report
make help       # list all targets
```

Install the dev toolchain (`golangci-lint`, `gosec`) and the pre-commit hook with:

```sh
make install-tools
make install-hooks    # runs `make check` before each commit
```

---

## License

[MIT](LICENSE) © zoobz.io
