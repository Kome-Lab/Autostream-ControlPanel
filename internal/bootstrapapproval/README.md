# IU-WS1-01 Approval / Ceiling / Consumption

This package connects an independently verified approval to the existing
ControlPanel management bootstrap transaction. It supplies no production Origin,
Root, clock, quorum, key, approval policy, Recovery authority or public endpoint.
`super_admin` and ordinary v2 permission cannot construct an approval view.

## Composition and authority boundary

Only explicit `NewFixtureCore` composition is available. It requires all of:

- the exact disposable `autostream_v3_auth_ci` database;
- explicitly applied `ApplyFixtureMigrations(ctx, db, true)`;
- the `IU-WS1-SYNTHETIC-ONLY-1` verifier profile;
- fixture-only qualification, with production qualification false;
- explicit, positive shared lifecycle limits.

There is no environment switch, implicit fallback, default verifier, default
limit, startup migration, normal bootstrap activation or production constructor.
The shared `v3lifecycle` identity validator is used directly. Structural coherence
and fixture observations never issue execution or production qualification.

`v3authorization.NewApprovalManagementService` is the opt-in product seam. Normal
startup and the existing management HTTP routes continue to use their original
composition. The legacy `BootstrapAuthorizer` contract remains compatible.

## Transaction and uncertainty

1. Freeze the management grant and immutable request metadata.
2. Inspect the same Origin attempt outside SQL locks. Only an explicit
   known-unused observation can proceed to `VerifyAndConsume`; unknown or
   consumed state remains Inspect-only, including after a process restart.
3. Clone its response, check exact identity/purpose/request/ceiling, and install
   an unexported context-bound approval view.
4. Open one private `lifecyclefoundation.BeginApprovalSQL` transaction. Bind its
   actual DB pointer, transaction, context, request digest and active lifetime.
5. Keep the existing management order: C epoch, User, Session, then local approval
   checkpoint/consumption and the existing grant/history/marker/critical audit.
6. Commit that one actual transaction. Local approval consumption, finite scope /
   subject / operations ceiling, grant, bootstrap marker, both histories,
   checkpoint and critical audit all commit or roll back together.

There is no nested autocommit pretending to join the CP transaction, no alternate
pool read after CP commit, and no verifier/provider call during CP SQL locks.
An arbitrary DTO, copied `Verification`, raw `*sql.Tx`, changed context, foreign
pool, changed request or completed participant cannot consume the private view.

An external consumed/uncertain approval remains consumed/uncertain when CP rolls
back. Commit acknowledgement loss uses the existing management UNKNOWN result
and local uncertainty gate. It never reissues/reconsumes/releases approval.

`ApprovalManagementService.Inspect` authenticates the current human in a short
local transaction, rolls it back before Origin contact, then performs Origin
read-only inspection and a CP read-only snapshot of the original attempt. It
does not create audit records, call Prepare, retry mutation, grant authority,
clear process uncertainty or release HELD. Origin and CP knowledge are separate.

Only nonsecret management metadata and its equality digest are persisted. The
digest covers the canonical nonsecret management grant; it is not D4 secret
commitment. Passwords/hashes, session/CSRF tokens, provider secrets, recovery
material, raw manifests and private keys are not represented by the DTO.

## Verification inventory

The mandatory `TestIUWS1*` inventory includes:

- real HTTP listener to the actual opt-in product Core/ManagementSQL path;
- exact finite actor class, purpose, subject, scope, operation and ceiling checks;
- nil / unqualified verifier and absent-limit refusal;
- immutable context/DB/transaction/request participation;
- two independent HTTP servers, management services and CP pools, with a
  pretransaction barrier and a duplicate external response;
- critical audit failure rolling back every local row while external consumption
  remains durable;
- physically committed and physically rolled-back acknowledgement loss;
- restart using a fresh service and pool with same-attempt pure inspection;
- independent OS process inspection after both physical ACK-loss outcomes;
- partial CP restore retaining UNKNOWN when one table loses its matching rows;
- separate Origin / CP knowledge and unconditional HELD retention;
- immutable snapshots, manifest replay refusal and session-token exclusion.

SQL tests use only the existing explicitly disposable
`AUTOSTREAM_V3_AUTH_TEST_DSN` fixture. Without it they are NOT_RUN (Go reports
SKIP); mandatory CI inventories must reject missing/skipped/failed roots. Merely
having these tests in source does not establish a PASS.

Actual independent Origin, L2 provider, qualified clock, restore rollback
detection, S12-C / S12-D, production commitment and product acceptance remain
unconnected / NOT_RUN / NOT_QUALIFIED. This internal unit is not first-release
completion, normal product activation or authorization to deploy.
