# 0001 — Release deploy pipeline and health gate

Status: Approved
Owner: Kenny-BBDog
Created: 2026-10-05
Supersedes: none

## Problem

Deploying the panel is a manual sequence performed over SSH: pull a branch on
the host, `go build` there, replace the binary, restart, and eyeball the result.
Four problems follow.

1. **It is not reproducible.** What is in production is whatever the host's
   working tree happened to contain at that moment, not a reviewed commit.
2. **A broken frontend reached production once already, on 2026-10-05.** The
   panel served its login page and then rendered blank; the browser console
   showed a 404 on `assets/index-wPHBcLd2.js`. Nothing in the deploy caught it,
   because nothing checked that the assets the panel's HTML references can
   actually be served.
3. **It drops live connections with no check afterwards.** `x-ui` is the parent
   process of both `xray` and `sing-box`, so restarting it disconnects every
   client for a few seconds. There is no verification that service came back.
4. **The build environment is a hand-managed dependency.** Building on the host
   needs a Go toolchain and a C compiler there. DMIT runs Debian 12 (glibc 2.36)
   and LA runs Ubuntu 24.04 (glibc 2.39), so a dynamically linked binary built on
   a newer glibc will not start on the older host. Nothing enforces this.

## Goal

- Production binaries come from a reviewed commit, built by CI, verified by
  checksum, and deployed by one local command.
- A deploy that leaves the panel unable to serve its frontend fails loudly and
  rolls back by itself.
- Two hosts run the same binary, from the same commit, in one action.
- The servers gain no new inbound access and hold no GitHub credentials.

## Non-goals

- **Not** continuous deployment. Deploy stays manually triggered, because it
  disconnects users; see the rollout window below.
- **Not** replacing the whole release tarball. The tarball ships `bin/xray`,
  `bin/mtg-linux-amd64`, geo databases and service files. Our hosts run a
  **hand-built `sing-box`** (with `v2ray_api` stats support that enables traffic
  accounting) and per-host `config.json`. Overwriting the package would replace
  the sing-box binary and destroy billing data. Only `x-ui` is replaced.
- **Not** GitHub-hosted deploy. Storing an SSH key that can reach production as a
  repository secret widens the blast radius; the operator's machine already has
  the key.
- **Not** a general-purpose orchestrator. Two known hosts, one binary.

## Design

### Build (already exists)

`release.yml` builds a static musl binary per platform with the Bootlin
cross-toolchain, and publishes a rolling `dev-latest` pre-release that is
force-moved to each `master` commit. Its trigger was pointed at `master`
(spec-adjacent work in PR #9), and it now runs on merge: `dev-latest` currently
records `commit=17bcca48`, built 2026-10-05T13:38Z.

Static musl linking is what makes one artifact valid on both hosts: it removes
the glibc version from the equation entirely.

### Deploy (this spec)

`deploy/deploy.sh`, run from the operator's machine with Git Bash.

Inputs: the release tag (default `dev-latest`), and `--hosts dmit|la|both`
(default `both`).

Per host:

1. Download the artifact and its `.sha256`, **verify the checksum, and abort on
   mismatch**. A partial or tampered download must never reach `/usr/local`.
2. Extract only `x-ui/x-ui` with `tar -xzO` — no package overlay.
3. Upload to a staging path on the host, never directly over the live binary.
4. Record the outgoing binary's checksum, then back it up to
   `/root/x-ui.bin.pre-deploy-<UTC>`. This is the rollback artifact.
5. Stop the service, move the staged file into `/usr/local/x-ui/x-ui` with mode
   0755, start the service.
6. Run the health gate (below). On failure: restore the backup, restart, verify
   the restore, and exit non-zero with both checksums reported.
7. Confirm both managed children came back: `xray-linux-amd64` and `sing-box`.

### Health gate

Three checks, none of which need a credential — the sign-in page is
unauthenticated on both hosts:

1. **Process**: `systemctl is-active x-ui` is `active`.
2. **Login page**: the panel root returns `200` and `text/html`.
3. **Referenced assets are served**: read the asset names out of the **embedded
   HTML inside the deployed binary**, then fetch each one and require `200` with a
   `javascript`/`css` MIME type.

Check 3 is the reason this gate exists, and two findings shaped it.

**Scraping the served page is not enough.** The login page's HTML is not the app
shell: `/panel/*` 307-redirects without a session, so only the binary itself
knows which assets the app shell will request. The asset list therefore comes
from `strings` over the binary on the host, not from an HTTP response.

**Status alone is not enough.** A panel that answers unknown paths with
`index.html` returns `200 text/html` for a missing `.js`. A status-only check
passes, and the browser then fails to parse an HTML document as an ES module —
the blank page again, with a green gate. Requiring the MIME type closes that gap.

Measured against the two binaries present on DMIT:

| Binary | Assets embedded | Check 3 on the referenced set |
| --- | --- | --- |
| `/root/x-ui.bin.pre-distfix-*` (2026-10-05) | 137 | passes today |
| deployed build | 151 | passes |

The pre-distfix binary passes check 3 now, which corrected an initial assumption
in this spec: that binary's embedded `index.html` and assets do agree (its
`assets/index-wPHBcLd2.js` is served, 49925 bytes of real JS). The blank page came
from a browser holding an **older HTML** that requested a chunk name the panel no
longer had — the same symptom class (a 404 on a referenced asset) but originating
in a stale client cache, not an inconsistent build. Check 3 tests the property
that actually matters and is observable from the server: everything the deployed
HTML names must be servable **as the right type**. It cannot see a stale browser
cache, and the spec does not pretend otherwise.

Hosts differ, so the gate is parameterised:

| Host | Base URL (from the host) | Probe transport |
| --- | --- | --- |
| DMIT | `https://vpn.flintic.uk/vpn-admin/` | TLS, `--resolve` to loopback |
| LA | `http://127.0.0.1:25073/Cul7KGMTQ8sEpXsfsN/` | plain HTTP on loopback |

LA's panel is plain HTTP on 25073 rather than TLS behind nginx, so the probe is
per-host rather than shared.

### Known-answer check

A gate that cannot fail is worthless. Check 3's failure branch was exercised
directly rather than by deploying a broken binary (an isolated second instance of
the panel could not be started cleanly — it contends for the subscription port):

- Every referenced asset on the live host returns `200` with a JS/CSS MIME type.
- A fabricated asset name returns `404` with no content type.
- The panel root returns `200 text/html`, i.e. the wrong type for an asset.

The check distinguishes all three, so a missing or mis-typed asset fails it.

### Configuration

Host addresses, ports and base paths live in one place in the script. The SSH
key for DMIT is the operator's existing key; LA is reached **through DMIT as a
jump host** (DMIT holds the key LA trusts), matching how this estate is already
administered.

## Verification

- Checksum verification: corrupt the downloaded tarball, confirm the script
  aborts before touching the host.
- Health gate, positive: run `--health-only` against both hosts; all three checks
  pass (37 referenced assets served with a correct MIME type).
- Health gate, failure branch: confirmed the check separates served assets
  (`200`, JS/CSS type) from a fabricated name (`404`) and from the HTML root
  (`text/html`). Recorded in the PR.
- `--dry-run`: confirm the binary is staged and checksum-verified, and the live
  binary is not touched.
- Service continuity after a real deploy: both `xray-linux-amd64` and `sing-box`
  are running and client traffic counters advance.
- Rollback: confirm `/root/x-ui.bin.pre-deploy-<UTC>` exists and its checksum
  equals the pre-deploy checksum.

## Rollout

Both hosts together, in the low-traffic window (after ~01:00 local), because the
restart disconnects clients for a few seconds. DMIT first, then LA: DMIT is the
master panel, so a failure there is discovered before LA is touched.

Rollback is automatic on health-gate failure. Manual rollback is
`/root/x-ui.bin.pre-deploy-<UTC>` moved back into place and the service restarted.

## Open questions

None.
