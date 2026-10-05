# Deploying our production panel

This file covers **our** two hosts (DMIT master + LA node). For the upstream
cloud-init / marketplace tooling, see [`README.md`](README.md) in this directory.

`deploy.sh` ships the panel binary to both hosts. Run it from the operator's
machine with Git Bash, not from a host.

```bash
./deploy/deploy.sh --health-only     # check both hosts, change nothing
./deploy/deploy.sh --dry-run         # download, verify, stage; no swap
./deploy/deploy.sh                   # deploy dev-latest to both hosts
./deploy/deploy.sh --tag v3.8.6      # a specific release
./deploy/deploy.sh --hosts dmit      # one host
```

Implements [`specs/0001-deploy-pipeline.md`](../specs/0001-deploy-pipeline.md).

## Where the binary comes from

Nothing is built on the hosts, and nothing is built locally. CI builds it:
`.github/workflows/release.yml` compiles a **static musl** binary per platform
with the Bootlin cross-toolchain and publishes it to the rolling `dev-latest`
pre-release, force-moved to each `master` commit.

Static musl linking is what makes one artifact valid on both hosts — DMIT is
Debian 12 (glibc 2.36) and LA is Ubuntu 24.04 (glibc 2.39). A dynamically linked
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

## Hosts

| Host | Address | Panel | Reached by |
| --- | --- | --- | --- |
| DMIT (master) | `179.255.106.182` | `https://vpn.flintic.uk/vpn-admin/` (2053 behind nginx) | SSH key at `~/.ssh/vps_clean.pem` |
| LA (node) | `156.225.88.212` | `http://127.0.0.1:25073/Cul7KGMTQ8sEpXsfsN/` | Through DMIT as a jump host |

LA is reached through DMIT because DMIT holds the key LA trusts; this matches how
the estate is already administered. No GitHub secret holds any server key — that
is why deploy is a local script rather than a workflow.

## When to run it

Restarting `x-ui` restarts `xray` and `sing-box` (it is their parent process), so
every client connection drops for a few seconds. **Deploy in the low-traffic
window, after ~01:00 local.** Check the load first:

```bash
ssh -i ~/.ssh/vps_clean.pem root@179.255.106.182 \
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

## Prerequisites

- Git Bash (the script is bash; it is not a PowerShell script).
- `gh` CLI, authenticated — it fetches the release artifact.
- `ssh`, `scp`, `tar`, `curl`, `sha256sum` (Git Bash provides all of these).
- The SSH key for DMIT. Override with `--key` or `$SSH_KEY`.
