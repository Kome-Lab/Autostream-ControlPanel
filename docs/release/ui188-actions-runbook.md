# UI188 Actions introduction and dispatch runbook

This document is a reviewable future procedure. It does not authorize Git
signing/staging/commit/push, PR/main/tag/Release, workflow dispatch, runner
allocation or production work. Local source/static readiness is distinct from
delivery, exact-SHA CI, Full E2E and release approval.

## Gates

| Gate | Current status | Evidence needed to advance |
| --- | --- | --- |
| SOURCE_READY | Local reconstructed bytes and focused tests verified | Reviewed product and integration diff with full tree/path hashes |
| WORKFLOW_STATIC_READY | Local actionlint, YAML/schema and positive/negative tests | Not an Actions run |
| Gate A / CI_EXACT_SHA_PASS | Evaluate per immutable delivered SHA | Each repository's original blocking jobs plus new checks; preserve FAIL, SKIP and unavailable proof independently |
| Runner / cost | BLOCKED_UNCONFIRMED | Organization plan/eligible runner group, actual available size, billing owner and spend limit |
| Default dispatcher | NOT_INSTALLED | Separately approved minimal default-branch introduction |
| Gate B / FULL_E2E_PASS | NOT_RUN | One authorized 1,000-second two-stream run, all28 groups and26 scenario obligations, raw exits retained, sealed receiver and verified safe artifact |
| RELEASE_APPROVED / production | NOT_RUN | Separate PR/main/tag/Release/deployment/real Discord decisions and compatibility/rollback acceptance |

The existing repository CIs retain their installer, Memory/real MariaDB,
contract, native/DAVE, race, Web/browser, security and build responsibilities.
Required skips and unavailable jobs do not count as PASS. A green small preflight
does not replace any original CI denominator. Existing release-host workflows
are not invoked by this implementation.

## One delivery decision with bounded phases

1. Review the complete source map and per-repository diffs. Before the first
   push, establish branch protection, signing/token permissions, every natural
   workflow, and external auto-deploy/pull/image consumers. Missing accessible
   hooks or a404 does not prove that external consumers are absent. Stop the
   Git writes if a required delivery condition is unknown; independent local
   E2E correction can continue. Owner confirmation for these exact features
   resolves external production reflection without claiming all consumers absent.
2. After explicit approval, deliver the reviewed CP product commit and its
   integration-only successor from the fixed CP base; deliver the BOT product
   commit independently from its fixed base. Push only the approved feature
   refspecs and capture final signed SHAs. Capture P_CP's terminal natural result
   before pushing its I_CP child, even when that result is FAIL. Keep P_BOT's
   result independent. The authorized batch allows CP two normal pushes and BOT
   one; no unchanged re-push, empty commit, rerun or force push. Main, PR, tag and
   publication remain separate decisions.
3. Verify natural CI against the exact delivered CP integration SHA and BOT
   product SHA. Check every original blocking job's conclusion and denominator,
   not just a moving branch badge. Record new runs independently from old FAILs.
   The first-attempt feature push also permits the bounded standard-runner
   bootstrap in `ui188-integration.yml`; it cannot dispatch Full E2E.
4. Introduce only `.github/workflows/ui188-full-e2e.yml` and its standard-library
   `scripts/ci/ui188-full-e2e/gate.py` on default main through a separately
   authorized review/merge. The dispatcher checks approved immutable feature
   SHAs. The full product feature does not have to be merged merely to dispatch.
   The integration preflight belongs to the feature and need not enter this thin
   dispatcher change. Main operations still require their own approval.
5. Provision an approved GitHub-hosted Linux x64 runner in group
   `ui188-hosted-linux`, label `ui188-linux-8core`, restrict access to this repo,
   and set pool concurrency1. Configure the `ui188-full-e2e` environment with an
   explicit trusted reviewer. Verify it is a hosted runner, not a user PC or a
   public self-hosted PR runner.
6. Have the billing owner approve one run. Configure repository variables
   `UI188_FULL_E2E_ENABLED=true` and `UI188_APPROVED_DISPATCH` with the reviewed
   `cp_sha`, `bot_sha`, `lock_sha256`, a matching `approval_id` beginning `UI188-`,
   `runner_label=ui188-linux-8core`, `billing_confirmed=true`, `max_minutes=120`.
   The variable contains no secret. Actual CP/BOT commit SHAs remain pending
   until the authorized signed delivery; never put a tree or moving ref there.
7. Dispatch from main with those exact values and `cost_ack=true`. The free
   authorizer rejects push/PR/fork/feature dispatch, mismatched pins, missing
   approval and run_attempt>1 before the paid job. The protected environment
   reviewer must verify this specific source/cost receipt. Disable the enabled
   flag after the authorized dispatch; the read-only workflow cannot consume or
   revoke a repository variable. Concurrency serializes dispatches but does not
   make an approval receipt cryptographically single-use. Any later independent
   dispatch needs a fresh owner approval. No automatic rerun is implemented.
8. Require measured CPU>=8, MemTotal>=30GiB and free disk>=180GiB, plus the live
   canary before the original S0–S5 run. Capacity failure reports
   `BLOCKED_RUNNER_CAPACITY`. The standard public Ubuntu size in the handoff is
   below the5.7CPU live envelope; it is not an approved fallback.
9. Inspect run/job/source/environment IDs, all28 groups,26 obligations and original exits,
   per-socket/audio/PCM/cgroup metrics and sealed-input hash. Download only the
   sanitized ZIP and verify its outside hash, members and CRC. Separately verify
   the GitHub upload wrapper and retain approved private evidence if required
   through a separately designed private storage channel. Do not upload raw data
   to this public repo as a workaround.

The actual feature commits, signatures, natural runs and remaining delivery
budget are recorded in the private delivery result. This runbook creates no
permission to repeat them. Ordinary CI does not authorize Full E2E or production.

## Runner cost and runtime limits

The proposed size is8vCPU/32GB/300GB. The billing owner must establish the actual
account rate, rounding, storage, taxes and spend controls before enabling it.
Plan eligibility, billing terms and actual allocation remain unconfirmed. No
priced runner is created or used by the short standard-runner bootstrap.

The live Compose allocation is runtime4CPU/8GiB, DB0.7CPU/1GiB and WorkerB0.9CPU/
896MiB (5.6CPU total). This is a future hosted-job envelope, not permission to
run a full local stack. Timeout120 minutes covers preparation and acceptance;
preparation's native build is bounded at60 minutes. An overrun is FAIL/NOT_RUN,
not permission to shorten media or lower quality thresholds.

Official references:

- [Manual workflow requirements](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow)
- [Larger runner sizes](https://docs.github.com/en/actions/reference/runners/larger-runners)
- [Larger runner eligibility and billing](https://docs.github.com/en/actions/concepts/runners/larger-runners)
- [Current runner rates](https://docs.github.com/en/billing/reference/actions-runner-pricing)
- [Artifact read access](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/download-workflow-artifacts)
