# Deploying our production panel

This file covers **our** two hosts (a primary panel and a secondary node). For the upstream
cloud-init / marketplace tooling, see [`README.md`](README.md) in this directory.

`deploy.sh` ships the panel binary to both hosts. Run it from the operator's
machine with Git Bash, not from a host.

```bash
./deploy/deploy.sh --health-only     # check both hosts, change nothing
./deploy/deploy.sh --dry-run         # download, verify, stage; no swap
./deploy/deploy.sh                   # deploy dev-latest to both hosts
./deploy/deploy.sh --tag v3.8.6      # a specific release
./deploy/deploy.sh --hosts primary   # one host
```

Implements [`specs/0001-deploy-pipeline.md`](../specs/0001-deploy-pipeline.md).

## Where the binary comes from

Nothing is built on the hosts, and nothing is built locally. CI builds it:
`.github/workflows/release.yml` compiles a **static musl** binary per platform
with the Bootlin cross-toolchain and publishes it to the rolling `dev-latest`
pre-release, force-moved to each `master` commit.

Static musl linking is what makes one artifact valid on both hosts — they run
different distributions with different glibc versions (Debian 12 is 2.36,
Ubuntu 24.04 is 2.39). A dynamically linked
build produced against a newer glibc will not start on the older host. This is
why the artifact is taken from CI rather than compiled on a host.

## What the script replaces, and what it does not

It replaces **only** `/usr/local/x-ui/x-ui`.

The release tarball is a full package (`bin/xray`, geo databases, service files).
Those are deliberately not unpacked: both hosts run a **hand-built `sing-box`**
that provides the `v2ray_api` stats endpoint the panel meters traffic through.
Overlaying the tarball would replace that binary and stop traffic accounting
(and therefore billing).

## Safety

| Step | Why |
| --- | --- |
| Release checksum verified before anything is uploaded | A truncated or tampered download must never reach `/usr/local` |
| Uploaded to `/root/x-ui.deploy-<UTC>`, never over the live file | The live binary is only replaced by a `mv` after the new one is verified on the host |
| `cp -a` to `/root/x-ui.bin.pre-deploy-<UTC>` | Rollback artifact, on the host |
| Health gate after restart | See below |
| Automatic rollback on gate failure | Restores the backup, restarts, re-checks, exits non-zero |

## The health gate

Three checks, none needing a credential (the sign-in page is unauthenticated):

1. `systemctl is-active x-ui` is `active`.
2. The panel root returns `200` with an `<html>` body.
3. **Every asset the embedded HTML references is served with a JS/CSS MIME type.**

Check 3 is the one that matters, and two findings shaped it.

**Scraping the served page is not enough.** The panel's login page is not the app
shell: `/panel/*` 307-redirects without a session. Only the binary knows which
assets the app shell will request, so the list is read from the binary on the
host with `strings`.

**Status alone is not enough.** A panel that answers unknown paths with
`index.html` returns `200 text/html` for a missing `.js`. A status-only check
passes, and the browser then fails to parse an HTML document as an ES module —
the blank page again, with a green gate. Requiring the MIME type closes that gap.

This is the failure the gate exists for: on 2026-10-05 the panel served its login
page and then rendered blank, with the console showing a 404 on a referenced
asset.

## Configuration

Host addresses, panel paths and key paths are **not in this repository** — it is
public, and those values describe live infrastructure. They live in
`deploy/hosts.env`, which is gitignored:

```bash
cp deploy/hosts.env.example deploy/hosts.env
$EDITOR deploy/hosts.env
```

`hosts.env` defines the two targets:

| Variable group | Meaning |
| --- | --- |
| `PRIMARY_HOST`, `PRIMARY_SSH_KEY`, `PRIMARY_BASE`, `PRIMARY_ORIGIN`, `PRIMARY_ASSET_DIR` | The primary panel (the one that serves the customer-facing subscription entry point) |
| `SECONDARY_HOST`, `SECONDARY_JUMP_KEY`, `SECONDARY_BASE`, `SECONDARY_ORIGIN`, `SECONDARY_ASSET_DIR` | The secondary panel, reached **through the primary as a jump host** because the primary holds the key the secondary trusts |
| `DEPLOY_ORDER` | Which order `--hosts both` uses; primary first, so a failure there is found before the secondary is touched |

`PRIMARY_CURL_EXTRA` (optional) carries any extra `curl` arguments needed to
reach the primary's panel URL from the host itself, for example a `--resolve`
when the panel is served behind a reverse proxy.

The two hosts differ in how their panels are exposed, which is why the health
gate takes both a base URL and an origin per host rather than sharing one.

No GitHub secret holds any server key — that is why deploy is a local script
rather than a workflow.

## When to run it

Restarting `x-ui` restarts `xray` and `sing-box` (it is their parent process), so
every client connection drops for a few seconds. **Deploy in the low-traffic
window.** Check the current load first, substituting your own values from
`hosts.env`:

```bash
# shellcheck disable=SC1091
source deploy/hosts.env
ssh -i "$PRIMARY_SSH_KEY" "root@$PRIMARY_HOST" \
  "ss -tn state established '( sport = :8445 )' | wc -l"
```

## Rollback

Automatic on a health-gate failure. To roll back by hand, re-deploy the previous
release if it still exists, or on the host:

```bash
systemctl stop x-ui
cp -a /root/x-ui.bin.pre-deploy-<UTC> /usr/local/x-ui/x-ui
systemctl start x-ui
```

## Back up the database too

The script backs up the **binary**, not the database. The panel runs its schema
migrations on first start, so before deploying a build that changes the schema,
copy the database on each host as well:

```bash
cp -a /etc/x-ui/x-ui.db /root/x-ui.db.pre-deploy-<UTC>
```

To inspect a live database afterwards, copy **`x-ui.db` together with `-wal` and
`-shm`**: the write-ahead log can be several megabytes, and a copy of `x-ui.db`
alone can show a migration that has not been checkpointed yet as absent. Neither
host has `sqlite3` installed; both have `python3`, whose `sqlite3` module reads
the trio correctly.

## Prerequisites

- Git Bash (the script is bash; it is not a PowerShell script).
- `gh` CLI, authenticated — it fetches the release artifact.
- `ssh`, `scp`, `tar`, `curl`, `sha256sum` (Git Bash provides all of these).
- `deploy/hosts.env`, filled in from the template.
- The SSH key named by `PRIMARY_SSH_KEY`. Override per run with `--key` or `$SSH_KEY`.

