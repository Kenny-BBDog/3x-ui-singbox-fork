# Specs

A spec is the written record of **what a change is for and how we will know it
worked**, made before the code. This fork diverges from upstream on purpose
(sing-box runtime, node sync, per-client mount, and soon traffic multipliers),
so decisions need to survive longer than one conversation.

## When a spec is required

Required:

- Runtime behavior: anything the panel or a client can observe.
- Data: schema, migrations, traffic accounting, credentials, subscription output.
- Protocols: inbound/outbound rendering, TLS, transport, node sync.
- Deploy and operations: the deploy path, health checks, rollback, secrets.
- Anything that touches production hosts.

Not required: typo fixes, formatting, dependency bumps, comment-only changes,
documentation-only edits. Use judgement; when unsure, write a short one.

## Naming and location

```
specs/NNNN-short-slug.md
```

Four-digit, zero-padded, monotonically increasing. Never renumber or reuse a
number. The slug is lowercase and hyphenated. Start from
[`TEMPLATE.md`](TEMPLATE.md).

## Status

Every spec carries a `Status:` line directly under the title:

| Status | Meaning |
| --- | --- |
| `Draft` | Being written. Not yet agreed. Do not implement. |
| `Approved` | Agreed. Implementation may start. |
| `Implementing` | Code exists on a task branch or a merged PR. |
| `Shipped` | In production. The spec records the PR and the deployed commit. |
| `Superseded by NNNN` | Replaced. Keep the file; do not delete it. |
| `Rejected` | Decided against. Keep the reason. |

## Lifecycle

1. Write the spec on a task branch (`chore/spec-*` or the branch that will
   implement it) and open it as a PR. The PR is where it gets reviewed.
2. On agreement, set `Status: Approved`. Implement on a task branch that links
   back to the spec.
3. Set `Status: Implementing` when code lands on a branch.
4. Set `Status: Shipped` and fill in the PR link and the deployed commit when it
   reaches production.
5. If a later spec replaces it, mark the old one `Superseded by NNNN` and link
   both ways.

A spec is not a plan that expires. If reality diverges from it, update the spec
in the same PR that diverges from it — a stale spec is worse than none, because
it is trusted.

## Relation to the other documents

| Document | Answers |
| --- | --- |
| `specs/` | Why this change, what it must do, how it is verified. |
| `BRANCHING.md` | Which branch, how it merges, what the gate is. |
| `deploy/PRODUCTION.md` | How it reaches our servers. |
| `deploy/README.md` | Upstream's cloud-init / marketplace install tooling. |
| `docs/architecture.md` | How the code is structured today (upstream's map). |
| `REVIEW.md` | What a reviewer judges a change by. |

## Index

| Spec | Title | Status |
| --- | --- | --- |
| [0001](0001-deploy-pipeline.md) | Release deploy pipeline and health gate | Implementing |
| [0002](0002-traffic-multiplier.md) | Per-inbound traffic multiplier (residential 2×) | Approved |
