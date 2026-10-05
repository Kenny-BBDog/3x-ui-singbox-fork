# Branching And Release Flow

This repository is a private fork of [`MHSanaei/3x-ui`](https://github.com/MHSanaei/3x-ui).
It is the **admin panel** for our VPN service, and it diverges from upstream on
purpose (sing-box protocols, node sync, per-client mount). Treat `master` here as
production for our fleet, not as a mirror of upstream's `main`.

## Remotes

| Remote | Points at | Purpose |
| --- | --- | --- |
| `origin` | `Kenny-BBDog/3x-ui-singbox-fork` | Our fork. All our work lands here. |
| `upstream` | `MHSanaei/3x-ui` | The original project. Fetch to review or cherry-pick upstream fixes (security patches especially). |

Never push to `upstream`.

Note: upstream's default branch is `main`; ours is `master`. Workflow triggers in
`.github/workflows/` therefore name `master`.

## Branches

| Branch | Role |
| --- | --- |
| `master` | Production. What the DMIT and LA panels are built from. Only accepts merges from a reviewed task branch or a `hotfix/*`. |
| `feature/*` | New capability. One task per branch, short-lived. |
| `fix/*` | Non-urgent defect fix. |
| `hotfix/*` | Production is broken right now. Branch from `master`, smallest possible change. |
| `chore/*` | Tooling, CI, docs, dependency work. No product behavior change. |

Start a task branch from an up-to-date `master`, and keep it focused on one
change. Delete it after merge.

## Flow

1. `git switch master && git pull` — start from current production.
2. `git switch -c <type>/<short-description>` — for example `feature/traffic-multiplier`.
3. Make the change, commit with a message explaining **why**.
4. `git push -u origin <branch>` and open a pull request against `master`.
5. CI must be green before merge. See the gate below.
6. Merge, then delete the branch. Deploy is a separate, manually triggered step.
7. For `hotfix/*`, branch from `master`, keep the diff minimal, and open the PR
   with the incident noted in the description.

## Merge Gate

**This is a Free-plan private repository, so GitHub branch protection and rulesets
are not available** (the API returns 403 "Upgrade to GitHub Pro"). The gate is
therefore by convention plus CI, not server-enforced:

- CI (`ci.yml`) runs on every pull request and on every push to `master`. A red
  CI means do not merge.
- Do not merge a red or still-running CI, and do not force-push `master`.
- Open a PR for every change to `master`, even when working alone — the PR is the
  record of what shipped and why, and it is where CI reports.

If this project later needs an enforced gate (required checks, blocked
force-push), that requires GitHub Pro on the repository, or making it public.

## Workflows

| Workflow | Trigger | What it does |
| --- | --- | --- |
| `ci.yml` | PR, push to `master` | Go tests, Postgres durability/migration tests, codegen freshness, govulncheck, `-race`+shuffle, fuzz smoke, golangci, frontend lint/typecheck/test/build/audit. |
| `release.yml` | push to `master`, `v*.*.*` tags, manual | Builds multi-platform panel packages. Also publishes the rolling `dev-latest` pre-release that the panel's Dev update channel installs. |
| `codeql.yml` | push to `master`, weekly | CodeQL static analysis. |
| `docker.yml` | `v*.*.*` tags, manual | Docker image build. |
| `mutation.yml` | nightly, manual | Gremlins mutation testing. Informational; does not fail the build. |
| `cleanup_caches.yml` | nightly, manual | Deletes stale Actions caches. |

Upstream workflow files that do not apply to this fork were removed rather than
left dead: the upstream docs-site CI/deploy (they target `docs.sanaei.dev`, the
upstream author's domain) and the Claude-based PR review / issue analyst jobs
(they need a `CLAUDE_CODE_OAUTH_TOKEN` secret this repository does not have).

## Deploy

Deployment is **manually triggered** by design: the panel is a parent process of
xray and sing-box, so restarting it drops every live client connection for a few
seconds. Deploys therefore happen in the low-traffic window (after ~01:00), never
automatically.

Build artifacts come from `release.yml`, which publishes a static musl binary to
the rolling `dev-latest` pre-release on every `master` merge.

Deploy with [`deploy/deploy.sh`](deploy/deploy.sh):

```bash
./deploy/deploy.sh --health-only   # check both hosts, change nothing
./deploy/deploy.sh                 # deploy dev-latest to both hosts
```

It verifies the release checksum, replaces only `/usr/local/x-ui/x-ui` (the
hand-built `sing-box` and per-host config are left alone), runs a health gate, and
rolls back automatically if the gate fails. Full detail in
[`deploy/PRODUCTION.md`](deploy/PRODUCTION.md) and
[`specs/0001-deploy-pipeline.md`](specs/0001-deploy-pipeline.md).

## Specs

Changes that touch runtime behavior, data, protocols or operations get a spec in
[`specs/`](specs/) before implementation. See [`specs/README.md`](specs/README.md)
for when one is required and the lifecycle.
