# 0002 — Per-inbound traffic multiplier (residential 2×)

Status: Draft
Owner: Kenny-BBDog
Created: 2026-10-05
Supersedes: none

## Problem

The residential egress cities are more expensive per byte than the main lines, so
they should bill at a higher rate: a residential node counts **2×** against a
customer's quota. The panel counts bytes 1:1 today, and there is no multiplier
concept anywhere in the codebase (verified: `multiplier`, `trafficFactor`,
`coefficient`, `倍率` appear nowhere except `cwndMultiplier`, a TCP parameter, and
`RateLimiter`, an anti-abuse notification helper).

The blocker is not the arithmetic, it is **attribution**. A customer is attached
to many inbounds at once — `dmit_ergou` is on **11** sing-box inbounds (1 main + 10
residential) — and the meters cannot currently say which inbound a byte came
through.

### Measured evidence

Both facts below were established by running an isolated sing-box, not by reading
docs. The lab: two `anytls` inbounds, one shared user identity, known-size
downloads driven through each inbound, counters read over the v2ray stats gRPC API.

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
`addClientTraffic` merges purely by email (its comment says so: "one shared row per
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
succeeded (1 MiB each, no auth error in either the client or server log), while
each counter captured only its own inbound's traffic. `SBUser` has separate
`Password` (the credential) and `Name` (the label) fields, and only the label
feeds the counter name.

Finding 2 is what makes the problem solvable without changing sing-box.

## Goal

- An inbound can carry a traffic multiplier; residential inbounds are 2×, main
  lines 1×.
- Quota enforcement uses weighted bytes, so 2× traffic depletes a quota twice as
  fast.
- The operator sees weighted usage, and the UI is explicit about the weight so
  the number is not mistaken for raw bytes.
- Each customer still has exactly **one** credential, one subscription, and one
  quota. The multiplier must not multiply the customer's identities in the UI.
- Raw byte accounting is preserved and recoverable, so a mistaken multiplier can
  be corrected rather than being permanently baked into a total.

## Non-goals

- **Not** changing sing-box itself. Finding 2 makes a fork of the sing-box core
  unnecessary; we fork the panel, not the core.
- **Not** per-inbound quota display beyond the multiplier (no separate quota per
  node).
- **Not** the customer-facing portal. Monster reads usage through its own layer;
  this spec makes the panel's numbers correct and exposes the multiplier, and the
  portal consumes it later.
- **Not** retroactive re-billing. Traffic recorded before the multiplier existed
  stays 1:1; only new bytes are weighted. See Migration.

## Design

### Model

Add to the inbound (new column, default `1`):

```
traffic_multiplier  INTEGER NOT NULL DEFAULT 1
```

An integer keeps the common case exact and avoids float drift in a billing path.
A multiplier of `2` means "every byte through this inbound counts twice".

The multiplier belongs to the **inbound**, not the client, because it is a
property of the egress (that city's link costs more), not of the customer.

### Attribution

The renderer currently emits one shared `name` (the client email) per inbound and
lists each email once under `stats.users`. Change it so:

- each inbound's user entry gets a label unique to `(inbound, client)`, and
- `stats.users` lists every such label.

The label is internal. It must not leak into anything the client sees (password,
UUID, subscription output, QR, links are all unchanged), and it must be
reversible back to `(inboundId, email)` for accounting.

Open question 1 settles the label format; the counter name must remain parseable
and must not collide with an email.

### Accounting

`PollTraffic` reads one counter pair per label. For each label it already knows
the inbound, so it can read that inbound's multiplier and emit a weighted delta:

```
weightedUp   = deltaUp   * multiplier
weightedDown = deltaDown * multiplier
```

Two properties matter:

- **Quota enforcement must use the weighted value**, or a 2× node would not deplete
  a quota faster. `addClientTraffic` merges by email and compares `up + down`
  against `total`, so the weighted delta has to be what is written there.
- **The write path is a single merge point.** `addClientTraffic` is where every
  delta lands (`inbound_traffic.go`), so weighting before that point keeps the
  change in one place.

Raw bytes are kept alongside the weighted value so the multiplier stays correctable
(see Migration). Storing both is the reason this is not a one-line change.

### Where the multiplier is applied (the master/node problem)

This is the part that a "just multiply the delta" reading would get wrong, and it
is what makes this spec worth writing.

The residential inbounds are **not** on the panel where the operator works:

| Host | Role | Local sing-box inbounds | Residential inbounds |
| --- | --- | --- | --- |
| DMIT | master panel | 1 (`id 20`, 洛杉矶·TLS) | only as mirror rows (`node_id = 1`) |
| LA | node | 11 (`id 15`–`25`: 达拉斯 + 10 residential) | the real ones |

The mirror rows on DMIT exist for the UI and for subscription rendering. The
traffic is metered by the sing-box that actually terminates the connection — LA —
and LA's counters are what the panel's own accounting job reads there.

Measured, and the reason this needs a decision: the same customer holds
**different totals on the two hosts**, because each meters only its own traffic.

```
dmit_ergou   client_traffics (up+down)
  DMIT master   18,814,668,942  (18.8 GB)
  LA node        8,633,984,326  ( 8.6 GB)
```

So a multiplier set on the master does not automatically reach the node that
meters the traffic. The requirement is therefore explicit:

> Setting an inbound's multiplier on the master must reach the node that renders
> that inbound, before the bytes it should weight have flowed.

The existing node sync is the carrier: inbounds already replicate master → node
(the mirror rows are the master's view of the node's own rows). The multiplier
field has to travel with them. Open question 4 fixes the direction and the timing;
the constraint is that it must be in place at the node **before** traffic, because
accounting is not retroactive.

There is also a visibility question the operator will hit immediately: with LA
metering residential traffic and DMIT metering the main line, one customer's usage
is split across two counters and neither host shows the whole truth. That predates
this spec, but a multiplier makes it a billing question rather than a curiosity.
Open question 5 decides whether this spec reconciles the two or records the split
as known and out of scope.

### Baselines and restarts

`sbTraffic` holds last-seen cumulative totals per user and re-baselines when the
process restarts (counters reset). That logic must become per-label. Two hazards:

- A **multiplier change** must not be treated as a counter reset. The raw baseline
  is independent of the weight, so the raw delta stays correct while the weight
  changes underneath it.
- A **label change** (adding or removing a client on an inbound) resets only that
  label's baseline, not every client's. This is why the label must be derived
  deterministically rather than randomly.

### UI

- The inbound form gains a "traffic multiplier" field, default 1.
- The inbounds list shows a badge for any inbound with a multiplier above 1, so
  the weight is visible without opening the form.
- Client and report views must say **weighted**, not present the number as raw
  bytes. A customer's page shows the same weighted figure the operator sees, so
  the two never disagree.

## Migration

Additive and reversible:

1. New column `traffic_multiplier` defaulting to `1`. Existing rows keep meaning
   exactly what they did, so nothing changes behavior until an inbound is set
   above 1.
2. New columns for raw bytes (see Open question 2) default such that the weighted
   value continues to be used by existing code paths until they are taught
   otherwise.
3. Existing accumulated totals are **not** rewritten. Everything recorded before
   the change is 1:1, which is the honest reading: those bytes really were charged
   at 1×.
4. Setting a residential inbound's multiplier to 2 takes effect for subsequent
   bytes only.

Rollback: set the multiplier back to 1 (immediate), or revert the binary. Raw
columns are additive, so an older binary ignores them.

## Verification

- **Isolated accounting test** (the lab from Finding 1/2, as a Go test): two
  inbounds, one client, one 2×; assert the weighted total equals
  `raw₁ + 2·raw₂` and that raw totals are also correct.
- **Credential invariance**: assert the rendered config has the same `password` for
  a client on every inbound, and that a client connected through any inbound still
  authenticates. (Finding 2 shows this holds; the test keeps it holding.)
- **Quota depletion**: a client with a 100 MB quota who spends 60 raw MB on a 2×
  node is disabled (60×2 ≥ 100), while the same 60 MB on a 1× node is not.
- **No client-visible change**: subscription output, links and QR for an existing
  client are byte-identical before and after, apart from nothing — the label is
  internal.
- **Baseline behavior**: restarting sing-box re-baselines without producing a
  spike; changing a multiplier mid-run does not produce a spike or a loss.
- **Migration**: apply to a copy of the production DB, confirm every existing
  inbound has multiplier 1 and totals are unchanged.
- Manual: set a residential inbound to 2×, push a known payload through it, confirm
  the customer's visible usage advanced by 2× the payload.

## Rollout

Two steps, and the order matters because of the restart:

1. **Panel change, multipliers all 1.** Deploy the accounting change with every
   inbound at 1×. Weighted and raw are then identical, so a bug in the weighting
   path cannot change any customer's numbers yet. Verify for a day.
2. **Set the residential inbounds to 2×.** This is a data change, no restart, and
   it takes effect for subsequent bytes.

Deploy through `deploy/deploy.sh` in the low-traffic window (after ~01:00), as the
restart drops connections for a few seconds.

## Open questions

1. **Label format for `(inbound, client)`.** Needs to be unique, deterministic,
   reversible, and non-colliding with an email. A candidate is
   `email@<inbound-tag>`; note that an email legitimately contains `@`, so the
   parse has to split on the last `@`, and a client email could itself look like a
   tag. Decide before implementing; this is the one irreversible detail.
2. **Whether to store raw bytes as new columns on `client_traffics`, or in a new
   per-inbound table.** Columns are simpler and keep the existing merge point;
   a table is normalized and would let per-inbound reporting grow. Decide before
   implementing.
3. **Rounding.** Whether a multiplier may be fractional (for example 1.5) or stays
   an integer. Fractional weights make every byte non-exact and the display
   awkward; recommend integer-only and say so.
4. **How the multiplier reaches the node that meters the traffic.** It must be
   present on LA before residential bytes flow, so master → node propagation has to
   carry the field. Decide: extend the existing inbound sync payload, or set the
   multiplier on the node directly. Extending the sync is the smaller change and
   keeps the master authoritative; it also needs a guard so a node does not meter
   with a stale multiplier while a sync is in flight.
5. **Whether one customer's usage should be reconciled across both hosts.** Today
   each host meters only its own traffic, so a customer attached to both the main
   line (DMIT) and residential nodes (LA) has their usage split across two totals
   and neither host shows the whole. Decide whether this spec fixes that or records
   it as known and out of scope. If it is out of scope, say so explicitly, because
   a multiplier makes the split a billing question rather than a curiosity.
