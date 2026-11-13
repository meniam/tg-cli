# Testing & Verification

**Verify every meaningful change. Add no permanent test by default.**
Keep a small suite that catches important failures. Test count and coverage are
signals, never targets or quotas. Project-required checks still apply.

## When to keep a test

Add a permanent test only when all three conditions hold:

1. It catches a specific important failure: wrong order totals, lost data,
   unauthorized access, a broken public contract or a blocked core workflow.
2. Existing checks do not adequately catch that failure.
3. Future changes need this automatic protection, and its value exceeds upkeep.

A new endpoint, component, algorithm or bug fix alone does not qualify.
Use one-off checks otherwise. Do not commit disposable scripts or assertions
unless they meet the same threshold.

Test observable behavior and invariants with expectations from requirements,
contracts or independently checked examples, never solely from implementation.
Choose the smallest effective set of checks at the level where the failure occurs;
for example, verify the order total through its calculation boundary without
repeating the same assertion for every helper. Test helpers separately only for
an additional important risk.

Skip tests for trivial accessors, delegation, framework guarantees or internal
call counts; verify relevant outcomes instead. Avoid elaborate fixtures, excessive
mocks and new infrastructure unless the risk justifies their cost.

When touching existing tests, remove obsolete, redundant or implementation-coupled
checks only if important protection remains. Investigate failures; never weaken
assertions, skip checks or change expected results merely to get a pass. Update
behavioral expectations only for an intended contract change.

## Verification workflow

1. Identify intended behavior and failure consequences. Assess impact, complexity,
   dependency boundaries, reversibility and recurrence risk; inspect existing coverage.
2. Run relevant existing checks and required project gates. Add focused temporary
   checks for gaps. For a bug, reproduce the defect and verify the fix; retain a
   regression test only if it meets the threshold above.
3. Select methods below by the actual risk, not by file type. Static checks or a
   passing suite prove only what they check. Increase depth for serious failures:
   independent expectations, boundaries, negative cases and realistic integration.
   Critical behavior needs durable regression protection; reuse existing protection
   or add qualifying tests. Document exceptions and seek independent review where feasible.
4. Compare results with expectations. Stop when identified risks are adequately
   checked; repeat only after relevant changes, failures or unresolved uncertainty.
5. Report what ran and the actual outcome, plus unverified behavior and why.
   Claim completion only after appropriate verification; disclose blocked or
   inconclusive checks without presenting assumptions as verified facts.

## Choose checks by risk

This is a menu, not a checklist; no row automatically requires a permanent test.

| Risk or change | Appropriate verification |
| --- | --- |
| Copy, formatting; simple rename, DTO or refactor | Diff review; applicable lint/types, existing tests and targeted usage checks |
| Dependencies or configuration | Validate configuration; startup/smoke check |
| Business rules or algorithms | Independent examples, boundaries and invariants/properties |
| API or external integration | HTTP, schema/contract checks; sandbox and failure cases |
| Queries, search or migrations | Representative data and real engine; apply migrations to a disposable DB and check schema/data |
| Jobs, queues, concurrency or transactions | Integration, retries, failures, parallel execution and consistency |
| UI or user journey | Browser interaction; E2E for affected workflows, not changes unable to affect user-visible behavior |
| Performance | Before/after benchmarks or profiling |
| Access or security boundaries | Negative cases, permissions and boundary checks |

Use realistic dependencies when integration is the risk; mocks cannot establish
that a real database, queue, service or framework works correctly.

## Checklist

1. Each new permanent test catches an important uncovered failure worth guarding against in future changes.
2. Relevant checks ran, expectations are independent, and important existing protection remains.
3. Results and verification gaps are reported accurately; temporary artifacts are removed.
