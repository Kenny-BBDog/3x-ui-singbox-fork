# NNNN — Short title in sentence case

Status: Draft
Owner: <who>
Created: YYYY-MM-DD
Supersedes: NNNN or none

## Problem

What is wrong, missing, or risky today. State it so someone who was not in the
conversation understands why this is worth doing. Include the evidence: a log
line, a failing check, a measured number, a user report.

## Goal

What must be true when this is done. Written as observable outcomes, not as a
list of code changes.

## Non-goals

What this deliberately does not do. This is the section that prevents scope
creep, so be specific about the tempting adjacent work.

## Design

The approach, and why it beats the alternatives. Name the alternatives you
rejected and the reason — a future reader will otherwise re-propose them.

Record constraints that are not obvious from the code:

- Compatibility: versions, ABIs, glibc/musl, schema versions.
- Data: what existing rows mean afterwards, and what happens to them.
- Security: what credentials exist, where they live, who can read them.
- Failure: what happens when a step fails, and what state is left behind.

## Verification

How we will know it works, and how we will know it broke something. Prefer
checks that can be run by someone else, and name the command. If something
cannot be verified before shipping, say so and say why.

## Rollout

How it reaches production, in what order, and what the rollback is. State the
window if the change can drop live connections.

## Open questions

Unresolved items, each with who decides. Remove this section when empty.
