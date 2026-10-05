# 0002 — Per-inbound traffic multiplier (residential 2×)

Status: Approved
Owner: Kenny-BBDog
Created: 2026-10-05
Supersedes: none

## Problem

Customers buy one quota (for example 100 GB) that is spent across **all** nodes,
and the operator sells routes of differing cost: a cheap high-volume 4837 line,
an expensive CN2GIA line, and residential IPs. The quota is one pool; the routes
are not equally expensive. A byte on a residential or CN2GIA route should count
for more than a byte on the cheap line, or the operator loses money on the
customers who use the expensive routes.

The panel counts bytes 1:1 today. There is no multiplier concept anywhere in the
codebase (verified: `multiplier`, `trafficFactor`, `coefficient`, `倍率` appear
nowhere except `cwndMultiplier`, a TCP parameter, and `RateLimiter`, an anti-abuse
notification helper).

The blocker is not the arithmetic, it is **attribution**. A customer is attached
to many inbounds at once — `dmit_ergou` is on **11** sing-box inbounds (1 main + 10
residential) — and the meters cannot currently say which inbound a byte came
through.

### Measured evidence

All four findings below were established by running an isolated sing-box and
reading the live production databases, not by reading docs. The lab: two `anytls`
inbounds with known-size downloads driven through each, counters read over the
v2ray stats gRPC API.

**Finding 1 — the per-user counter is global, not per-inbound.**

```
inbound>>>lab-in-one>>>traffic>>>downlink      4194509
inbound>>>lab-in-two>>>traffic>>>downlink      4194509
user>>>shared>>>traffic>>>downlink             8389018   = the sum of the two
```

There is no combined `inbound>>>…>>>user>>>…` counter. The sing-box binary
contains only two counter-name shapes (`user>>>`, `inbound>>>`), and the upstream
documentation states user stats are "global/shared for that user across all
inbounds". The clash-api `/connections` view carries `metadata.user` but only
while a connection is open — at rest it is empty, so it cannot be used to
attribute traffic after the fact.

This is also why the current code's `InboundId` on a client-traffic row is
meaningless: `PollTraffic` builds `users[email] = meter{inboundId: in.Id}` in a
loop, so the id is whichever inbound happened to be visited last, and
`addClientTraffic` merges purely by email (its own comment: "one shared row per
email regardless of how many inbounds the client is attached to").

**Finding 2 — the per-user label is a metering tag, not a credential, so it can
be made per-inbound.**

```
inbound one, user name "user@lab-one", password P
inbound two, user name "user@lab-two", password P   (same password)

user>>>user@lab-one>>>traffic>>>downlink       1048781
user>>>user@lab-two>>>traffic>>>downlink       1048781
```

Both connections authenticated with the same password and both downloads
succeeded (1 MiB each, no auth error in client or server log), while each counter
captured only its own inbound's traffic. `SBUser` has separate `Password` (the
credential) and `Name` (the label) fields, and only the label feeds the counter
name.

Finding 2 is what makes the problem solvable without changing sing-box.

**Finding 3 — the residential inbounds are not on the panel the operator uses.**

| Host | Role | Local sing-box inbounds | Residential inbounds |
| --- | --- | --- | --- |
| DMIT | master panel | 1 (`id 20`, 洛杉矶·TLS) | only as mirror rows (`node_id = 1`) |
| LA | node | 11 (`id 15`–`25`: 达拉斯 + 10 residential) | the real ones |

The mirror rows on DMIT exist for the UI and for subscription rendering. Traffic
is metered by the sing-box that terminates the connection — LA — and LA's counters
are what LA's own accounting job reads there.

So a multiplier set on the master does not by itself reach the node that meters
the traffic, and the multiplier must be in place **before** the bytes it weights
have flowed, because accounting is not retroactive.

**Finding 4 — cross-host summing already exists; the one-pool requirement is met
today.** This is the finding that changed the shape of this spec.

The master already accumulates node traffic into the customer's single row:

- `NodeClientTraffic` holds a per-node baseline of each email's counters.
- `SetRemoteTraffic` diffs the node's snapshot against that baseline and applies
  the delta to the master's `client_traffics` with an additive expression
  (`ClampedAddExpr("up")`), not a max.

Measured on production:

```
dmit_ergou   up+down
  DMIT master client_traffics    19,145,892,924  (19.1 GB)   <- local + node
  NodeClientTraffic baseline(LA)  8,633,987,004  ( 8.6 GB)   <- the node's share
  19.1 GB - 8.6 GB = 10.5 GB                                 <- DMIT's own line
```

So "one 100 GB pool regardless of node" is **already the behaviour**. The task is
not to build cross-host aggregation; it is to make the multiplier feed into it.

One nearby mechanism uses `max` and must not be confused with the summing path:
`overlayGlobalTraffic` raises a node's displayed counters to the largest value a
master pushed (`if globals[i].Up > r.Up`), and that is display-only, read-path
(`PushGlobalClientTraffics` → `AcceptGlobalTraffic` → `ClientGlobalTraffic`). The
accumulating path is `SetRemoteTraffic` → `addClientTraffic`. Weighting belongs on
the accumulating path.

## Goal

- An inbound carries a traffic multiplier; a residential or CN2GIA inbound can be
  2×, the cheap 4837 lines 1×.
- Quota enforcement uses weighted bytes, so 2× traffic depletes a quota twice as
  fast. A 100 GB customer who only uses a 2× node can move 50 GB.
- The pool stays **single and cross-node**: a customer who splits usage between
  the main line and residential nodes has one weighted total, which the existing
  summing path already delivers.
- The operator can see weighted usage, and the UI says which numbers are weighted
  so a weighted figure is not mistaken for raw bytes.
- Each customer keeps exactly one credential, one subscription, and one quota. The
  multiplier must not multiply the customer's identities in the UI.
- Raw bytes remain recoverable, so a mistaken multiplier can be corrected rather
  than being permanently baked into a total.

## Non-goals

- **Not** changing sing-box itself. Finding 2 makes a fork of the sing-box core
  unnecessary; we fork the panel, not the core.
- **Not** a per-inbound centralized report (which node used how much, in one
  place). Weighting happens where metering happens, so raw per-inbound detail
  stays local to each host. A cross-host per-node report would need a new
  aggregated push and is deliberately out of scope; if it is wanted later it gets
  its own spec.
- **Not** changing the cross-host summing mechanism. Finding 4 shows it works; this
  spec only feeds weighted deltas into it.
- **Not** the customer-facing portal. Monster reads usage through its own layer;
  this spec makes the panel's numbers correct and exposes the multiplier.
- **Not** retroactive re-billing. Traffic recorded before the multiplier existed
  stays 1:1; only new bytes are weighted. See Migration.

## Design

### Model

Add to the inbound:

```
traffic_multiplier  INTEGER NOT NULL DEFAULT 1
```

Rationale, and the resolved decision on rounding: **integers only**. A weight of
`2` means "every byte through this inbound counts twice". A fractional weight
(`1.5`) would make every byte inexact and the display awkward, and there is no
requirement that needs it — route pricing can be expressed with whole-number
weights or by how much quota a plan includes. The column is an integer so the
billing path never carries float drift.

The multiplier belongs to the **inbound**, not the client, because it is a
property of the route (that line costs more), not of the customer. This also means
it applies uniformly to everyone on that inbound and needs no per-client state.

### Attribution: label format

Decided: **`{inbound-tag}|{email}`**, for example

```
user>>>res-30001|dmit_ergou>>>traffic>>>uplink
```

Why this shape:

- It must not contain `>>>`, which is the counter name's own separator.
- It is parsed by splitting on the **first** `|`. Inbound tags are generated by us
  (`res-30001`, `inbound-dmit-anytls`) and never contain `|`, while a client email
  could in principle contain one — so the first separator is the unambiguous
  boundary. Splitting on the last would break if the email contained `|`.
- It is self-describing: a counter name in a log identifies both the client and the
  node, and attribution survives even if the database changes under it.
- It does not collide with an email, because the tag prefix is fixed-length-ish and
  known from the inbound list; and it is deterministic, so a re-render produces the
  same label and does not reset a baseline.

The label is internal. It must not leak into anything a client sees — password,
UUID, subscription output, QR and links are all unchanged. The password stays the
client's own, so a client authenticates identically on every inbound (Finding 2).

The renderer changes:

- each inbound's `users[]` entry gets the label as its `name`, and
- `stats.users` lists every `(inbound, client)` label rather than unique emails.

### Accounting: where the weight is applied

Decided: **at the metering host**, per inbound, before the delta leaves the
process that observed it.

```
Every host runs the same code:
  1. render: one label per (inbound, client)
  2. PollTraffic: read each label's counters -> a per-inbound raw delta
  3. weight it: delta * inbound.traffic_multiplier
  4. aggregate by email -> one row in client_traffics
```

The alternative — send per-`(inbound, client)` raw deltas to the master and weight
centrally — was rejected because it bloats the sync payload: LA would report
11 inbounds × 10 clients = 110 rows per tick instead of 10, and that grows with
both node count and client count. Weighting locally keeps the existing payload
shape and reuses the existing accumulation (Finding 4).

Consequences:

- **Quota enforcement needs no change.** `addClientTraffic` compares `up + down`
  against `total`, and on each host `up`/`down` become the *weighted* values, so
  the existing comparison already enforces a weighted quota.
- **The summing path needs no change.** `SetRemoteTraffic` accumulates the node's
  delta additively into the master row, so the master's total is the weighted sum
  of the node's weighted deltas. The 100 GB pool stays single and cross-node.
- **The weight is applied exactly once**, on the host that metered the bytes, so
  there is no double-weighting when the master folds the node's number in. This is
  the property a central-weighting design would put at risk.

### Raw bytes and correction

Add to `client_traffics`:

```
raw_up    INTEGER NOT NULL DEFAULT 0
raw_down  INTEGER NOT NULL DEFAULT 0
```

Decided: **columns on `client_traffics`, not a new per-inbound table.**

- The weighted values stay in `up`/`down` untouched, so quota enforcement, limiting,
  the UI and subscriptions keep reading exactly the fields they read now. No
  existing reader changes.
- The raw pair makes a multiplier auditable and correctable: `up / raw_up` shows the
  effective weight, and a mis-set multiplier can be recomputed rather than being
  permanently lost.
- A normalized per-inbound table is deferred with the per-node report (Non-goals).
  It would be the right home for that report, and adding it now would be structure
  with no consumer.

Migration sets `raw_up = up`, `raw_down = down` for existing rows. That is the
honest reading: those bytes really were charged at 1×.

### Baselines and restarts

`sbTraffic` holds last-seen cumulative totals per user and re-baselines when the
process restarts (counters reset). It becomes per-label. Two hazards:

- A **multiplier change** must not be treated as a counter reset. The raw baseline
  is independent of the weight, so the raw delta stays correct while the weight
  changes underneath it: baseline the raw counter, weight the delta.
- A **label change** (a client added to or removed from an inbound) resets only
  that label's baseline. This is why the label is derived deterministically
  (above) rather than randomly.

### Propagation to the metering host

Decided: **extend the existing inbound push payload**, master → node, so the
master stays authoritative and there is one source of truth.

`wireInbound()` already carries `total`, `remark`, `port`, `protocol`, `settings`,
`tag`, `expiryTime` and more; it gains `trafficMultiplier`. The existing
`config_dirty` → reconcile path delivers it, the same way an inbound edit already
propagates.

Timing constraint: the weight must be in place **before** the bytes it weights
flow. So the operational sequence is: set the multiplier on the master → the node
receives it on the next reconcile → only bytes metered after that are weighted.
Bytes metered in the gap are charged at the old weight. This is not a bug and is
documented so the operator is not surprised; the gap is one reconcile tick.

Guard to implement: a node must not weight with a stale multiplier while a push is
in flight. Reuse the existing `justPushed` signal (already used to stop a lagging
node snapshot from merging back) to hold off weighting for that tick, rather than
inventing a new handshake.

### UI

- Inbound form: a "traffic multiplier" field, integer, default 1, with a hint that
  it multiplies how fast this node consumes a quota.
- Inbounds list: a badge on any inbound with a multiplier above 1, so the weight is
  visible without opening the form.
- Client and report views: label the figure **weighted** rather than presenting it
  as raw bytes, and expose the raw pair so the operator can reconcile. A customer's
  page shows the same weighted figure the operator sees, so the two never disagree.

## Migration

Additive and reversible.

1. `inbounds.traffic_multiplier INTEGER NOT NULL DEFAULT 1`.
2. `client_traffics.raw_up`, `client_traffics.raw_down` `INTEGER NOT NULL
   DEFAULT 0`, backfilled from the current `up`/`down`.
3. Existing accumulated totals are **not** rewritten.
4. Setting a multiplier > 1 takes effect for subsequent bytes only.

Rollback: set the multiplier back to 1 (immediate, no restart), or revert the
binary. The new columns are additive, so an older binary ignores them.

## Verification

- **Isolated accounting test** (the lab of Findings 1–2, as a Go test): two inbounds,
  one client, one at 2×; assert the weighted total equals `raw₁ + 2·raw₂`, and that
  the raw columns equal `raw₁ + raw₂`.
- **Weighted exactly once across hosts**: a unit test over the accumulation path
  asserting a node's weighted delta is added, not re-weighted, when the master
  merges it. This is the property the design protects, so it gets its own test.
- **Credential invariance**: the rendered config has the same `password` for a
  client on every inbound, and a client connected through any inbound still
  authenticates (Finding 2 shows this holds; the test keeps it holding).
- **No client-visible change**: subscription output, links and QR for an existing
  client are byte-identical before and after; only the internal `name` label moved.
- **Quota depletion**: a client with a 100 MB quota who spends 60 raw MB on a 2×
  node is disabled (60×2 ≥ 100); the same 60 MB on a 1× node is not.
- **Pool is cross-node**: with a client on a 1× local inbound and a 2× node inbound,
  the master's total equals `local + 2·node`, and the client is disabled at the
  weighted 100 MB, not the raw one.
- **Baseline behavior**: a sing-box restart re-baselines without a spike; changing a
  multiplier mid-run produces neither a spike nor a loss; adding a client to an
  inbound does not disturb other clients' baselines.
- **Migration**: apply to a copy of the production DB; every existing inbound has
  multiplier 1, `raw_up`/`raw_down` equal the old `up`/`down`, and no total moved.
- Manual: set a residential inbound to 2×, push a known payload through it, and
  confirm the customer's visible usage advanced by 2× the payload while `raw_up`
  advanced by 1×.

## Rollout

Three steps, ordered so each one is verifiable before the next, and the
irreversible one is last.

1. **Ship the accounting change with every multiplier at 1.** Weighted and raw are
   identical, so a bug in the weighting path cannot move any customer's number yet.
   Verify the raw columns match `up`/`down` and that no total shifted.
2. **Verify the propagation.** Set one inbound's multiplier, confirm the node
   received it (via the existing reconcile) and that the node's own counters are
   weighted, before relying on it.
3. **Set the residential and CN2GIA inbounds to 2×.** A data change, no restart,
   effective for subsequent bytes.

Deploy through `deploy/deploy.sh` in the low-traffic window (after ~01:00), as the
restart drops connections for a few seconds. Step 3 needs no restart at all.

Known and accepted during rollout: bytes metered between setting the multiplier and
the node receiving it are charged at the old weight.

## Decisions

Resolved before implementation, replacing the earlier open questions.

| # | Question | Decision |
| --- | --- | --- |
| 1 | Label format for `(inbound, client)` | `{inbound-tag}\|{email}`, parsed on the first `\|`. Never contains `>>>`; deterministic so a re-render does not reset a baseline. |
| 2 | Where raw bytes live | New `raw_up` / `raw_down` columns on `client_traffics`. Weighted values stay in `up`/`down`, so no existing reader changes. A per-inbound table is deferred with the per-node report. |
| 3 | Rounding | Integer only. No fractional weight; the billing path never carries float drift. |
| 4 | How the multiplier reaches the metering host | Extend the existing inbound push payload (`wireInbound` gains `trafficMultiplier`), delivered by the existing `config_dirty` reconcile. Reuse `justPushed` as the in-flight guard rather than adding a handshake. |
| 5 | Cross-host usage reconciliation | Already implemented (Finding 4): `NodeClientTraffic` + additive `SetRemoteTraffic` merge into one row. This spec feeds weighted deltas into it and changes nothing about the mechanism. |

Remaining open item, does not block implementation: whether to add a cross-host
per-node usage report later. Out of scope here; it would get its own spec.
