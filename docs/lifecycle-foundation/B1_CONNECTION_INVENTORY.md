# B1 fixed-source connection matrix

Baseline: 2446b5b451cc3749a3245be640b5b43cf086183e.
Connected means opt-in code plus synthetic evidence; it never means production
enabled. Every admission/CP participant/UNKNOWN journal is the existing B0 one.
Every explicitly required but unsupported route is closed, without fallback.

| Source / owner | Existing Tx and locks | Gate and CP participation | UNKNOWN / B1 status / handoff |
| --- | --- | --- | --- |
| cmd/control-panel/main.go | DB open -> standard migrations -> activation/startup writers | ProductStartup before DB open; required provider currently unavailable | Required startup rejects. No startup freshness/migration writer admission; B3/B4 pending. Unset legacy behavior retained |
| v3authorization/sql_authority.go Read | Snapshot, User/capability/target/policy | R before snapshot; same borrowed SQL Tx and CP record | Result withheld on ambiguity; connected synthetic |
| Same ResolveTarget / ResolvePolicyTrust | Snapshot target/policy/trust | Separate R before snapshot, same Tx | Connected synthetic; no Ledger I/O in structural codecs |
| authority_store.go Generation | Epoch query | R and private SQL executor | Connected synthetic; diagnostic caller not authority by itself |
| Same Save | Own Tx or private Management Save binding; C epoch then generation CAS/history | W before any lock; same CP participant/Tx; bound Save reuses Management admission | Unknown blocks, no retry. Both ownership forms connected synthetic |
| management_sql.go Apply | C epoch -> User -> Session -> grants/records/audit; bound Save | W before enter/begin, same Tx/Checkpoint/critical audit | Connected synthetic; exact operations/Scope/Subject authorization retained |
| Same Inspect | C epoch -> User -> Session; records/current grant + critical audit | R, same Tx; only fixed critical-audit append is an auxiliary R write | Connected synthetic; not pure recovery Q |
| httpapi/server_v3_authorization_management.go | Trusted Session/CSRF -> API | Existing Handler calls configured Management Service | Handler unchanged; actual HTTP/SQL synthetic path tested |
| management_sql.go denial / RecordTransportDenial | Existing separate denial audit transaction | No untracked audit fallback allowed on L2 owner | Required L2 rejects; denial-audit participation unconnected. Legacy behavior unchanged |
| Same Bootstrap | Dedicated C epoch -> User -> Session -> Marker/grant/audit; no Save call | Explicit L2 immediately refuses | Closed; S12-A/C origin, consumption, ceiling and purpose contract missing. No Bootstrap executed |
| operationalauthorization/service.go Current / Authorize / ValidateDelegation | Snapshot + current Revision, existing union/typed constraints | R before evaluation; private SQL source can share B0 Tx | Opt-in connected; typed DelegationNone normal use unchanged. B2 state coverage not certified |
| store/operational_authorization_mariadb.go | Operational epoch -> nonlocking canonical reads | BeginSQL shares admitted Tx; no new C epoch lock | Connection seam implemented; existing SQL/source regressions retained |
| v3authorization/service.go Issue | Canonical resolve -> authority Read -> Claim -> prepare/seal -> WithCurrent/Finish | Require homogeneous Gate/DB composition; stage admissions below | Partial composition refuses before Claim. Stage atomicity only, no one distributed issuance commit |
| sqlstore.go Claim | Conditional PENDING insert; collision/identity lookup | EVIDENCE_CHANGE before claim; same Tx/CP record; exact immutable identity | Lost ACK returns UNKNOWN, no signing/takeover/retry. Connected synthetic |
| sql_authority.go WithCurrent + authorityTransaction.finish | C epoch -> User -> owner -> issuance; one-shot bound Finish | EVIDENCE_CHANGE before locks; same existing binding in B0-owned Tx | One-shot/current-generation enforcement retained; connected synthetic |
| sqlstore.go Finish CLOSED | Original conditional liability close | Separate EVIDENCE_CHANGE admission; same Tx | Connected synthetic. Naked ISSUED Finish refused on L2 owner |
| service.go Read / readRecord; sqlstore.go Lookup; AuthorizeRead | Current read authority, exact historical bytes/current applicability | R for Lookup and each current-authority boundary; no signing requirement | Connected stage seams; remote side-effect atomicity not claimed |
| store/admin.go; oauth_login.go Role/User assignment | Canonical v2 mutations, existing typed delegation and last-admin | No B1 mutation admission | B2/D2 pending; existing handlers/API source unchanged |
| auth_sessions_mariadb.go User/Session/lockout/cleanup | Existing guard/User/Session/Operational trigger where applicable | Private participant and lock-prefix preparation only | B2/D2 pending; not identity L2 |
| auth_user_credentials_mariadb.go; auth_credential_values.go | Credential/password/status updates | No live coverage or commitment adopted | B2/D2/D4 pending |
| oauth_login.go Identity Link/provisioning/provider changes | Independent existing helpers/implicit writers | No B1 identity admission | B2/D2 pending; provider/subject interpretation unchanged |
| auth_mfa_mariadb.go; auth_passkeys_mariadb.go; auth_email_change_mariadb.go | MFA, recovery codes, passkeys/challenges, email state | No B1 identity admission | B2/D2 pending; no secret export |
| security_settings_mutation.go and policy readers | Authentication policy applicability changes | No B1 synchronization | B2/D2 pending |
| operational_last_admin_mariadb.go; admin.go; auth_sessions_mariadb.go | guard -> User -> Session/Assignment -> Operational trigger | Original source byte-identical; future prefix precedes guard | Existing actual HTTP/SQL tests remain required. B1 preparation is not writer integration |
| Cascades, startup/cleanup/background writers above | Single DML and implicit writes, not only routes | Inventoried, not closed | B2 and startup ownership work pending; no broad Gate certification |
| Lifecycle Gate.Inspect / Service.Inspect | Pure CP/ledger observation | Current separately authorized Q, no mutation/slot release | Connected synthetic Q. No uncertainty cleared |
| Abort / Reconcile / Recovery | No qualified product entry | Contract only; unavailable | S12-C/D; independent approval and old-writer fence required |
| Contracts Canonical Decode/Encode | Pure structural contract | Outside authorization Gate | Unchanged, no Ledger I/O |
| Updater/Host Continuity | Separate workload execution qualification | Outside CP Lifecycle integration | Read-only reference, unchanged |

No B1 entry is production-enabled. Source-confirmed but unconnected entries
remain explicitly unconnected. B2 coverage, actual provider/Root and host
qualification remain required before any real L2 claim. The earlier B0 inventory
is retained as its historical state rather than rewritten as current execution.
