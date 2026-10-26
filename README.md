# Tau

(This project is meant to be an exercise in isolating sensitive information from an agent at the harness level. It is a work-in-progress; I don't (yet) claim a full hardened security model in this project.)

Tau runs the [Pi](https://pi.dev) coding agent in a Docker container. It is a Go
binary with a CLI (`tau`) and a terminal UI (`tau -i`). Pi does the agent work. Tau
builds the container, mounts your files into it, and keeps credentials out of the
agent's reach.

Requirements: Docker with BuildKit, Go to build the binary, rsync for workspaces.

## Why it works this way

- **A container.** The agent can break its own environment and you recreate it. Only
  mounted directories persist.
- **Credentials live in proxies.** If the agent holds a token, the token ends up in
  its context. A credentialed CLI (`aws`, `gh`, `linear`, `slack`) is a *proxy*: a
  server running as a separate OS user holds the credential. The agent types the
  command as usual, a shim forwards it, and the agent gets the output. Use scoped
  credentials. Tau does not stop an agent that runs code as the proxy user on purpose.
- **Profiles.** A profile is an OS user in the same container with its own Pi config,
  skills, environment, sessions and proxy choices. Use one for prod credentials and
  another for dev.
- **One config file.** `~/.tau/config.yaml` lists packages, skills, workspaces and
  proxies. `tau build` turns it into the image. `config.runtime.yaml` is meant to hold
  packages added after the build, and `tau packages diff` compares it to `config.yaml`.
  Nothing writes it yet.
- **Pi stays Pi.** Tau does not wrap its behaviour. It ships a few extensions and
  skills (`pi-config/`) and copies them into `~/.tau/pi/` on start.

## Use

```bash
make                  # builds ./tau
cp -r example/. ~/.tau/   # a starting config; fill in ~/.tau/secrets/*.env
tau build             # build the image from ~/.tau/config.yaml
tau start             # create the container
tau session new       # start Pi in tmux; detach with Ctrl-b d
tau session list
tau session attach NAME
tau -i                # terminal UI
```

Run `tau -h` for the other commands:

| Command | Does |
|---|---|
| `packages` | list, diff and resolve installed packages |
| `skills` | choose which host skills the agent loads |
| `workspace` | add a project as a copy, or `--live` to mount the real directory |
| `profile` | create profiles and set their workspace mode |
| `proxy` | list, allow, disallow, refresh and reload proxies; set their variables |
| `env` | set the profile's environment variables |

`tau workspace cd NAME` and `tau workspace add` print the host path, so
`cd "$(tau workspace cd NAME)"` changes your shell's directory.

## Files

Everything per user is under `~/.tau/` (`TAU_HOME`). Nothing there is tracked.

| Host | Container | Holds |
|---|---|---|
| `config.yaml` | | packages, skills, workspaces, proxies |
| `pi/` | `~/.pi` | Pi settings, sessions, skills, extensions |
| `workspace/` | `/workspace` | project copies and live mounts |
| `scripts/` | `~/scripts` | the agent's scripts |
| `share/` | `~/share` | files the agent gives you |
| `treehouse/` | `~/.treehouse` | git worktree pools |
| `secrets/` | | proxy secrets, never mounted |
| `profiles/NAME/` | | the same layout for another profile |

## Proxies

Define a proxy once in the default profile's `config.yaml`. Each profile lists the
ones it may use in `proxies.active`.

```yaml
proxies:
  packages:                       # hidden installs the agent cannot read or run
    aws-cli:
      check: test -x "$PREFIX/bin/aws"
      script: |
        curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o /tmp/a.zip
        unzip -q /tmp/a.zip -d /tmp && /tmp/aws/install -i "$PREFIX" -b "$PREFIX/bin"
  defs:
    aws-dev-readonly:
      package: aws-cli
      binary: bin/aws
      agent_command: aws          # what the agent types
      env_from: aws configure export-credentials --profile dev_readonly --format env
      deny: [configure export-credentials]
  active: [aws-dev-readonly]
```

- `env_from` runs on your machine at `tau start` and `tau proxy refresh`. Stored
  variables (`tau proxy env set PROXY KEY VALUE`) are kept in `~/.tau/secrets/`.
- A profile can have one active proxy per `agent_command`.
- Each proxy gets its own user, server and socket. The kernel keeps one proxy's
  memory from another's.
- Adding or changing a proxy needs `tau proxy reload`. Changing `packages` or
  `binary` needs `tau build` and `tau restart`.

## Git

`git` and `gh` are proxies. The `GITHUB_TOKEN`, git identity and credential helper live
in `~/.tau/secrets/git.env` and `gh.env`, and the container has no token of its own.
Both proxies run in the workspace, so they work only there. Scope the token to the repos
the agent should reach and protect the branches. The agent pushes a branch and opens a PR. You merge it. The
`git-guard` extension blocks git in `~/share` and PR merges, which catches accidents
only. Branch protection is the real limit.

## Not ported yet

These still exist only in the old bash script (`git show HEAD:tau`): `shell`,
`gitconfig`, `paste`, `annotate`, `live` and `migrate`. `AGENTS.md` has the detailed
reference.
