# Tau — controlled sandbox for Pi Coding Agent

Docker sandbox that runs Pi with short-lived, **readonly-scoped** AWS credentials
and runtime-injected external tokens. Portable: the image *is* the environment.

## Layout

- `Dockerfile` — Alpine base + toolchain + Pi. `aws-cli` v2 from Alpine community repo.
- `build.sh` — build the image. `PI_INSTALL=... ./build.sh` overrides Pi's install command.
- `run.sh` — resolve readonly temp creds on the host, inject into the container.
- `scripts/` — the agent's own scripts (Pi favours scripts over MCP). Baked into image.
- `workspace/` — mounted read-write into the container. Agent output lands here. Gitignored.
- `.env` — scoped external tokens, runtime-injected. Copy from `.env.example`. Gitignored.

## Two boundaries

1. **Credentials** (what the agent can *do*) — enforced cloud-side by IAM / token scope.
   This is the real boundary. Docker doesn't provide it; scoping does.
2. **Host** (what the agent can *touch*) — Docker namespaces. Bounds an LLM, not a
   kernel exploit. Fine for this threat model.

## Setup

1. Make an AWS profile whose **only** reachable permission is the readonly role.
   If it can also assume the write role, the sandbox gives you nothing.
2. `./build.sh`
3. `cp .env.example .env` and fill scoped tokens (optional).
4. Log in if SSO: `aws sso login --profile <profile>`
5. Run:

   ```bash
   PI_AWS_PROFILE=<readonly-profile> ./run.sh
   ```

## Credential flow

`run.sh` runs `aws configure export-credentials` on the host, so SSO / assume-role
resolves to temp creds there. Only those temp creds enter the container — as env
vars, never on disk, never the SSO cache. They expire on their own.

## Instance metadata (EC2 portability)

On EC2 a container can pull the *host's* role creds via `169.254.169.254` and
bypass all scoping. Not routable on macOS/Docker Desktop. When porting to EC2,
block link-local from the container or set the host IMDS hop limit to 1.
