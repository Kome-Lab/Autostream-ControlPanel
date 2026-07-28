# Task Creation: SSHを使わないUpdaterと変更可能なNodeポートへの移行

作成日: 2026-07-28

## Goal

管理者がControl Panelから更新とNodeポート変更を指示すると、各物理ホストに1つだけ配置された非rootのHost Pull Agentが、Control Panelの既存HTTPS APIへ外向き接続して処理する構成へ移行する。移行完了後は更新用SSH、22番ポート、中央Updaterの8090 listenerを不要にし、各Nodeサービスの待受ポートを安全に変更・検証・rollbackできるようにする。

利用者に見える完了条件は次のとおり。

- SSH/22番とUpdater/8090を閉じたホストをControl Panelから更新できる。
- Host Pull Agent登録ではHost/Port/SSLを入力しない。
- Worker、Encoder/Recorder、Discord Bot、Observabilityのポートを既定値以外へ変更できる。
- ポート変更に失敗した場合、旧ポート・旧設定・旧リリースへ自動復帰する。
- systemd、Docker、reverse proxy構成で「公開endpoint」と「ローカル待受endpoint」の違いが明示される。

## Context inspected

### Control Panel / Updater

- `internal/updateagent/agent.go`
  - 旧per-host Agentにはoutbound register、heartbeat、claim、report、journal recoveryが残っている。
  - 現在のCLIはこのAgentを起動せず、中央Coordinatorだけを起動する。
- `internal/updateagent/remote_helper.go`
- `internal/updateagent/remote_helper_transient.go`
- `internal/updateagent/remote_protocol.go`
  - 現SSH helperにはplan/session/hash、mutation grant、root-owned ledger、ambiguous時のreconcile-only処理がある。
- `internal/updateagent/bootstrap_profile.go`
  - 標準systemd health/version portを8080〜8084へ固定している。
- `internal/store/updater_policy.go`
  - targetは`target_id`、`host_id`、`service_type`、`deployment_mode`のみで、登録済みserviceやlisten portとの明示的な結合がない。
  - Updater status API portは8090が既定。
- `internal/store/services.go`
  - 現在の`services.host/port/public_url`は管理者の希望値、Control Panelの接続先、Heartbeatの報告値を兼用している。
- `internal/httpapi/system_updates.go`
- `internal/store/system_updates.go`
- `internal/store/system_update_mutation_grants.go`
  - host slot、lease、report sequence、mutation grantの既存境界を確認した。
- `web/src/features/nodes/node-registration-view.tsx`
- `web/src/features/application/updater-settings-panel.tsx`
- `web/src/features/application/updater-host-bootstrap-panel.tsx`
  - Node port、Updater 8090、SSH bootstrapが別画面・別設定として存在する。

### Nodeサービス

- `../autostream-worker/cmd/worker/main.go`
- `../autostream-encoder-recorder/cmd/encoder-recorder/main.go`
- `../autostream-discord-bot/cmd/discord-bot/main.go`
- `../autostream-observability/cmd/observability/main.go`
  - 各バイナリは既に`AUTOSTREAM_BIND_ADDR`または`OBSERVABILITY_BIND_ADDR`で任意bind addressを受け取れる。
- 各サービスの`.env.example`、`systemd/*.service.example`、`docker-compose*.yml`
  - systemd既定値とDocker host mappingには8081〜8084の固定値が残る。
  - Dockerコンテナ内portは通常8080で、host公開portやNode広告portとは異なり得る。

### Contracts / Deployment / Docs

- `../autostream-contracts/openapi/control-api.yaml`
- `../autostream-contracts/schemas/system-update-*.schema.json`
  - host別claim/report/grant契約は存在するが、portless Host Agent、transport ownership、port reconfigureの契約はない。
- `.github/workflows/release-host.yml`
  - 現Host Releaseは中央UpdaterとSSH helperを含む12資産を固定している。
- `../autostream-docker/.github/workflows/publish-ghcr.yml`
- `../autostream-docker/source-versions.env`
  - Docker bundleにはminimum agent versionとservice source tagの固定契約がある。
- `../autostream-docs/docs/operations/system-updates.md`
- `../autostream-docs/docs/control-panel/node-agent-registration.md`
- `../autostream-docs/docs/deployment/docker.md`
  - 現行の中央Updater、SSH helper、8090、固定ポート手順を説明している。

### Worktree constraints

調査時点で次の`autostream-docker`ファイルには既存の未commit変更がある。実装時に上書き・巻き戻し・混在させない。

- `.github/workflows/publish-ghcr.yml`
- `services/control-panel/Dockerfile`
- `source-versions.env`

このTask Creationでは実装・テスト・release確認を行っていない。

## Requirements inventory

### Critical requirements

1. **更新経路からSSHを廃止する**
   - 移行後のHost AgentはControl Panelへoutbound HTTPS接続する。
   - Host Agentとlocal executorは受信TCP listenerを持たない。
   - 22番と8090を閉じた状態を本番相当E2Eの必須条件とする。

2. **1物理ホスト1 Agentにする**
   - サービスごとではなく、1つのHost Pull Agentが同じホスト上の複数Nodeサービスを管理する。
   - host identityとruntime tokenはホスト固有とする。
   - claim対象hostはrequest bodyやHeartbeat自己申告ではなく、token assignmentからサーバー側で確定する。

3. **root権限境界を維持する**
   - Host Agentは専用非rootユーザーで起動し、sudo一般権限、Docker group、任意systemd操作権限を持たない。
   - root-owned local executorは固定Unix socketまたは同等のlocal IPCから、allowlist済みoperationだけを受ける。
   - Agentから任意コマンド、任意パス、任意URL、任意unit、任意imageをrootへ渡せない。
   - local executor自身がpeer UID、root-owned policy、plan digest、session、mutation grantを検証する。
   - executor専用policy/credentialは`/etc/autostream-local-executor`へ分離し、他サービスtokenを含む`/etc/autostream`をexecutorから不可視にする。
   - executorの書込可能なrelease rootはサービス種別ごとの固定ディレクトリだけを列挙し、`/opt/autostream`全体を許可しない。

4. **現行のcrash/replay安全性を落とさない**
   - idempotency key、host/target active-job排他、lease token/generation、report sequenceを維持する。
   - mutation grantをhost、target、version、deployment mode、operation、plan hash、session、policy revisionへ拘束する。
   - grant consume前後にroot-owned durable fenceを置く。
   - 結果不明時は再applyせず、reconcileだけを行う。
   - 中央UpdaterとHost Agentの同時claimをownership epoch/fencing tokenで拒否する。

5. **ポートの意味を分離する**
   - `advertised endpoint`: Control Panelや他サービスからNodeへ到達するHost/Port/SSL。
   - `local listen endpoint`: 実サービスがホストまたはコンテナ内で待ち受けるaddress/port。
   - `local health endpoint`: Host Agentが更新後に検証するloopbackまたはCompose network endpoint。
   - direct systemd構成では1つの「サービスport」を3用途へ既定反映する。
   - Docker/reverse proxy構成では、公開portと内部portを明示的に分離できる。

6. **desired / applied / reportedを分離する**
   - 管理者の希望値を`desired`として保存する。
   - 検証済みの稼働値だけを`applied/effective`へ昇格する。
   - Heartbeatは`reported`だけを更新し、desired/appliedを上書きしない。
   - Control Panelのservice callはapplied endpointだけを使用する。

7. **ポート変更を独立した設定Jobにする**
   - software updateへ暗黙に混在させず、revision/idempotency付き`port_reconfigure`として扱う。
   - grant/planにはold/new port、network namespace、protocol、旧/新config digestを含める。
   - 旧設定、旧port、旧release/imageをcheckpointしてから変更する。
   - listener owner、service identity、health、version、config revisionを検証後にだけappliedへ昇格する。

8. **全Node配布形態を扱う**
   - systemd: `/opt/autostream/local-executor/ports`（root:root 0700）配下のサービス別固定env（root:root 0600）をatomic stageし、対象unitだけを再起動する。
   - port sidecarの書込権限は上記専用ディレクトリだけに限定し、tokenを置く`/etc/autostream`は不可視のまま保つ。
   - Docker: container port、published port、Compose network endpointを区別し、承認済みCompose digestを変更する独立transactionにする。
   - reverse proxy: 公開originを安定させ、内部listen portだけを変更できる。

9. **Agent/helper自身をSSHなしで更新できる**
   - 署名済みartifact、atomic symlink/two-slot、旧binary保持、再起動後Heartbeat確認、失敗時rollbackを実装する。
   - この経路が検証されるまでSSH資産を撤去しない。

10. **secretを安全に扱う**
    - Configure Tokenは一回限り、短TTL、stdin/TTY入力を維持する。
    - Runtime Tokenはhost固有、最小scope、root:agent-group、0640とする。
    - Release Tokenを共有長期tokenのままAgentへ配らない。job/artifact限定の短命credentialまたはControl Panel artifact proxyへ置換する。
    - runtime/release/grant tokenをargv、environment、log、永続request fileへ出さない。

### Non-functional requirements

- 旧`ssh_v1`と新`pull_v2`を少なくとも1 Bridge releaseで併存させる。
- 1ホストでactive transportは常に1つだけとする。
- DB/API/schemaはadditive migrationから始め、旧agent・旧policy JSONを互換期間中読み取る。
- 既存portを明示的に変更しない限り、upgradeだけでportを変えない。
- Portの通常許可範囲は1024〜65535とする。1024未満はサービスをroot化せず、reverse proxy経由の明示構成だけを許可する。
- 同一`execution_host_id + network_namespace + protocol + port`を一意予約する。別ホストの同一portは許可する。
- health URLをUI/APIからroot helperへ渡さず、allowlist済みpathからlocal executorが構成する。
- Control Panel障害中でも、grant消費後の失敗はローカルだけでrollbackできる。
- poll/heartbeatはjitter、backoff、rate limitを持つ。
- 全変更に監査イベントと、host/target/policy revision/job/sessionの相関IDを持たせる。

### Good-to-haves

- host固有mTLSと自動rotation。
- TPM/OS keystoreによる秘密鍵保護。
- AppArmor/seccompによるAgentとexecutorの追加分離。
- port drift、長時間ambiguous、grant異常発行の通知。
- reverse proxy設定の自動生成。ただし初期移行の必須条件にはしない。

### Stack and architecture context

- Backend/agents: Go、MariaDB、systemd、Unix domain socket。
- Web: Next.js、React、TypeScript。
- Contracts: OpenAPI、JSON Schema、Go contract tests。
- Packaging: GitHub immutable releases、checksums、attestations、GHCR。
- Deployment: systemd、Docker Compose、reverse proxy。
- Trust boundary:
  - Control Panelがdesired policy、job、lease、grantを所有。
  - Host Agentがoutbound control loopと非特権state/journalを所有。
  - local executorがroot-owned target policy、mutation fence、apply/rollbackを所有。

### Unknowns and assumptions

- Assumption: `host_id`は永続的なexecution host identityとし、Host Agent service IDと1対1で結ぶ。実装時に既存IDとの移行規則を確定する。
- Assumption: direct systemd UIでは1つのportを入力し、advertised/listen/healthへ同じ値を既定反映する。
- Assumption: Docker/reverse proxy UIでは「詳細設定」を開いた場合だけadvertised/listen/publishedを分離する。
- Assumption: Control Panel自身の公開originはreverse proxyの443等で固定し、Control Panel内部bind portの変更は最後の専用canaryで扱う。
- Existing-host bootstrap: リモートから新Agentを最初に配置するには既存経路が1回必要。既定案は現SSH updaterを最後に1回自動利用する。SSHを一度も使わない場合はlocal console/package installを選ぶ。
- Release tag、GitHub上の最新release状態、immutable asset状態は実装開始時に再確認する。既存記録から推測しない。

## Target architecture

```text
Control Panel API (:443 / :8080)
    ▲
    │ outbound HTTPS
    │ register / heartbeat / claim / report / grant
    │
Host Pull Agent（非root、1物理hostに1つ、受信portなし）
    │
    │ root-owned Unix socket / fixed local protocol
    ▼
Local Executor（root、必要時のみ処理）
    ├─ policy / plan / grant / peer UID検証
    ├─ systemd / Docker設定stage
    ├─ update / restart
    ├─ identity / health / version / revision検証
    └─ rollback / reconcile
```

移行期間だけ次を併存させる。

```text
transport_mode=ssh_v1  -> Central Updater -> SSH helper
transport_mode=pull_v2 -> Host Pull Agent -> Local Executor
```

同一hostのownership epochが一致する側だけclaim/grantを許可する。

## Iteration layering

### Iteration 1: Bridge contractとobserve-only Agent

- Outcome: 旧SSH更新を壊さず、portless Host Agentがhost固有identityで登録・Heartbeat・policy取得・local probeできる。
- Scope: additive DB/API/schema、transport ownership、desired/applied/reported endpoint、portless登録、Host Agent artifact、read-only local executor probe。
- Deferred: apply、port変更、SSH撤去。

### Iteration 2: pull_v2で既定portの更新

- Outcome: 非Control Panelのsystemd canary hostで、SSHを使わずsoftware update、rollback、crash recoveryが完了する。
- Scope: local apply/reconcile、mutation grant、artifact credential、Agent/helper自己更新、exclusive transport switch。
- Deferred: port変更、Docker、fleet全体。

### Iteration 3: 任意portと全Nodeサービス

- Outcome: Worker、Encoder/Recorder、Discord Bot、Observabilityで、systemdの任意port変更と旧port rollbackが動作する。
- Scope: `port_reconfigure`、port reservation、各サービス設定adapter、UIのdesired/applied/reported状態。
- Deferred: Docker/reverse proxyの高度なmapping。

### Iteration 4: Docker、fleet移行、legacy撤去

- Outcome: Docker/reverse proxy構成を含む全hostをpull_v2へ移し、Control Panel hostを最後にcanaryした後、SSH/8090資産を別releaseで撤去する。
- Scope: Compose digest transaction、release compatibility matrix、docs、host-by-host migration、legacy removal。

## Dependency graph

```text
1 Contract/DB bridge
├─> 2 Host Agent observe-only
│    └─> 3 Local executor probe
│         └─> 4 pull_v2 update/recovery
│              ├─> 5 Agent/helper self-update + credential lifecycle
│              └─> 6 systemd port reconfigure
│                   └─> 7 Docker/reverse proxy port mapping
└─> 8 Operator UI / migration controls

2..8 complete -> 9 Bridge release, canary, fleet switch, legacy removal
```

## Vertical slices

### 1. Add a backward-compatible host, transport, and endpoint contract

- Value:
  - 旧SSH hostを動かしたまま、新Agentと任意portの状態を安全に表現できる。
- Work:
  - Contractsに`ssh_v1|pull_v2`、host-bound Agent identity、ownership epoch、policy revisionを追加する。
  - targetを登録済み`service_id`へ明示的に結合する。
  - advertised endpointとlocal listen/health endpointを分ける。
  - desired/applied/reported endpoint、apply state、config revision、port reservationをadditive migrationで追加する。
  - Heartbeatはreportedだけを更新し、service callはappliedだけを読む。
  - `update_agent`または新Host Agent typeだけendpointなし登録を許可する。
  - 旧`services.port`、旧Updater policy JSON、旧agent responseをcompatibility adapterで読み取る。
- Likely areas:
  - `../autostream-contracts/openapi/control-api.yaml`
  - `../autostream-contracts/schemas/*`
  - `internal/database/migrations/*`
  - `internal/store/services.go`
  - `internal/store/updater_policy.go`
  - `internal/store/system_updates.go`
  - `internal/httpapi/server.go`
  - `internal/httpapi/system_updates.go`
- Dependencies: none.
- Acceptance:
  - 旧policy/旧agentのfixtureが同じ結果で動作する。
  - EndpointなしHost Agentを登録できるが、Worker等は有効endpoint必須のまま。
  - Heartbeatでdesired/appliedが変更されない。
  - targetとservice IDの不一致、port範囲外、同一host/namespace競合を拒否する。
  - ownership epochが異なるagentによるclaim/grantを拒否する。
- Verification:
  - `autostream-contracts`: `go test ./...`
  - `autostream-control-panel`: store/httpapi focused tests、MariaDB migration/integration。
  - 旧/new protocol compatibility matrix。
- Rollback/safety:
  - 旧列・旧JSONを削除しない。
  - migration downgradeではなくBridge版へのapplication rollbackを前提とする。
- Change Review focus:
  - source of truth、host identity、tenant/host越境、migrationのNULL/default、旧JSON decode。

### 2. Deliver a portless Host Pull Agent in observe-only mode

- Value:
  - SSH更新に影響を与えず、各hostがControl Panelへ外向き接続できることを確認できる。
- Work:
  - 旧`Agent`のpoll、Heartbeat、journal recoveryを再利用し、新しい明示的`host-agent`実行modeを追加する。
  - identity-only root-owned configへpanel URL、host/agent ID、runtime token、service nameだけを保存する。
  - 8090 status serverを起動しない。
  - Host Agentはhost capability、agent/protocol version、applied policy revisionをHeartbeatで報告する。
  - 最初は`observe_only`で、claim/applyを許可せず、port driftとtarget availabilityだけを報告する。
  - 非root systemd unit、installer、uninstaller、checksum/attestation対象artifactを追加する。
- Likely areas:
  - `cmd/autostream-updater/main.go`または新command。
  - `internal/updateagent/agent.go`
  - `internal/updateagent/managed_supervisor.go`
  - `internal/updateagent/status_server.go`
  - `systemd/`
  - `release/`
  - `.github/workflows/release-host.yml`
- Dependencies: Slice 1.
- Acceptance:
  - inbound TCP socketを開かずにregister/heartbeat/policy refreshが続く。
  - host A tokenでhost Bを名乗れない。
  - token stage/activate/rotate/revokeがactive mutationなしで完了する。
  - revoke直後からclaim/report/grantが拒否される。
- Verification:
  - socket enumerationで8090を含む受信listenerがない。
  - service sandbox、owner/mode、restart、network outage/backoffのLinux systemd test。
  - secretがargv、environment、log、state snapshotへないことを検査。
- Rollback/safety:
  - `observe_only`では更新job ownershipを変更しない。
  - 旧中央UpdaterとSSH資産を保持する。
- Change Review focus:
  - endpointless registration、identity binding、secret persistence、systemd sandbox。

### 3. Add a root-owned local executor with probe-only capability

- Value:
  - SSHなしで、非root Agentからroot境界を越えて対象serviceを正確に識別できる。
- Work:
  - 現`remote_helper`のSSH transportと、plan/grant/ledger実行coreを分離する。
  - root-owned Unix socketと、固定・versioned・boundedなlocal protocolを追加する。
  - socket directory owner/mode、peer UID、request size、unknown fields、one-request-per-connectionを検証する。
  - root-owned target policyからだけunit、path、deployment mode、local endpointを取得する。
  - probeはlistener PID/cgroup、service identity、health、`/updater/version`、config revisionを照合する。
  - UI/APIから任意URLを渡さない。
- Likely areas:
  - `internal/updateagent/remote_helper.go`
  - `internal/updateagent/remote_protocol.go`
  - `internal/updateagent/helper_config.go`
  - `internal/updateagent/remote_helper_transient.go`
  - new local executor/socket package and systemd units。
- Dependencies: Slices 1–2.
- Acceptance:
  - 一般local userと別service UIDはsocketへ接続できない。
  - Agent侵害を仮定しても任意command/path/unit/URLをrootへ渡せない。
  - 同じportで偽`/health`を返す別processをtarget成功と判定しない。
  - policy/plan digest不一致をfail closedする。
- Verification:
  - peer credential、symlink、TOCTOU、oversize、unknown field、fake health、wrong cgroupの負系。
  - systemd実機でsocket activation、restart、owner/mode確認。
- Rollback/safety:
  - probe-onlyでmutation operationを実装しない。
- Change Review focus:
  - root boundary、IPC authentication、path ownership、protocol parser、SSRF。

### 4. Execute default-port software updates through pull_v2

- Value:
  - 非Control Panel canary hostで、人も機械もSSHを使わず通常更新できる。
- Work:
  - 旧Agentの廃止済み`/authorize`依存を除去し、現mutation grant契約へ統合する。
  - local executorへstage/apply/reconcileを追加し、現行のplan/session/hash/ledgerを維持する。
  - host ownership epochを原子的に`ssh_v1`から`pull_v2`へ切り替える。
  - grant消費、service stop、artifact切替、start、health、result保存の各境界でdurable fenceを置く。
  - shared Release Tokenを、job/artifact限定credentialまたはartifact proxyへ置き換える。
  - Workerの既定port systemd hostを最初のcanaryにする。
- Likely areas:
  - `internal/updateagent/agent.go`
  - `internal/updateagent/panel.go`
  - `internal/updateagent/artifact.go`
  - `internal/updateagent/remote_helper*.go`
  - `internal/httpapi/system_update_grants.go`
  - `internal/store/system_update_mutation_grants.go`
- Dependencies: Slices 1–3.
- Acceptance:
  - 22/8090を遮断した状態でupdate、health/version検証、reportが成功する。
  - 中央UpdaterとHost Agentが同じhost jobを二重claimできない。
  - apply結果不明時に再applyせずreconcileする。
  - lease/revoke/report ack喪失時に安全に停止またはlocal rollbackへ収束する。
  - Control Panel停止中の失敗でもlocal rollbackでき、復旧後に結果を再送できる。
- Verification:
  - crash point fault injection。
  - token/grant replay、期限切れ、別plan/session/generationの拒否。
  - Linux systemd canary E2E。
- Rollback/safety:
  - rollback先は移行前版ではなく、両protocolを理解するBridge版。
  - canary安定期間中はSSH資産を緊急fallback用に保持する。
- Change Review focus:
  - split brain、grant binding、ambiguous handling、artifact trust、journal durability。

### 5. Make Host Agent and local executor self-updating and rotatable

- Value:
  - SSH撤去後も更新基盤自体を保守・復旧できる。
- Work:
  - Agentとexecutorを署名済みartifact、two-slot/atomic symlink、旧binary保持で更新する。
  - Host Agent capability、Host Release manifest、directive、grant、root plan、durable stateを`recovery_protocol_version=2`へexactに結び、legacy protocolを拒否する。
  - binary/marker fileだけでなく、`bin`、temporary slot、`slotsRoot`、新規state rootと親directory entryまでfsyncしてからstate/grantを確定する。
  - `.new`を安全に回収し、単一で安全な`.old`だけをdurable stateと照合して復元する。複数、malformed、unsafe、state矛盾はmanual recoveryへfail closedにする。
  - activate、resumed activating、rollback、`current`再構築の前に、slot owner/mode、marker、2 binary digest、version、commit、release binding、protocolをfresh processで再検証する。
  - staging失敗を`stable + failed_generation`としてgrant収束より先に永続化し、一致するprepared/consumed/applied stage grantを`token_sha256`とexact bindingを保持したreceipt-free terminal `phase=failed`へ収束させる。同一IPC requestのlost-response replayはno-op successとし、異bindingを拒否する。
  - root recovery supervisorは固定Unix socketへUID 0専用・2秒timeout・非mutationのwatchdog statusを要求し、hung/status不一致ではrollback fenceを解除しない。
  - restart後のAgent Heartbeatとexecutor probe成功後にだけ新slotを確定する。
  - token rotationをstage -> new-token heartbeat -> activate -> old-token revokeにする。
  - active mutation中の通常rotationを拒否し、緊急revoke後もconsume済みhelperがrollbackを完遂できるようにする。
- Dependencies: Slice 4.
- Acceptance:
  - Agent更新中にprocess killしても旧または新の健全なslotへ収束する。
  - executor更新失敗時に旧executorで復旧できる。
  - protocol minimum version未満のAgentは新jobをclaimできず、recovery protocol 1のstate/release/grantを受理しない。
  - directory sync/rename失敗時に旧slotが残り、reserved orphanが残らない。
  - fresh processがmarker/binary/owner/mode tamperをactivate、resume、rollbackの前に拒否する。
  - failed generationとprepared/consumed/applied grantが再起動後も二重mutationなしで収束する。
  - Host Agent UIDはwatchdog statusを使えず、root peerはwatchdog status以外のmutationを要求できない。
  - SSHなしでAgent、executor、runtime tokenを更新できる。
- Verification:
  - directory sync fault injection、reserved artifact recovery、fresh-process slot tamper、grant crash matrix、root-only/hung Unix socket regression。
  - Linux container/focused testsとroot fixture。
  - 実systemdのkill/reboot、version skew、rotation/revoke timing、amd64/arm64 canaryはrelease前の外部proofとして別に実行する。
- Rollback/safety:
  - SSH資産撤去のhard gateとする。
- Change Review focus:
  - self-update deadlock、slot activation、credential overlap、downgrade/replay。

### 6. Reconfigure arbitrary ports transactionally on systemd Nodes

- Value:
  - Worker、Encoder/Recorder、Discord Bot、ObservabilityのportをControl Panelから安全に変更できる。
- Work:
  - `port_reconfigure` jobとgrant bindingへold/new port、protocol、network namespace、policy/config revision/digestを追加する。
  - direct modeではNode登録の1つのservice portをadvertised/listen/healthへ既定反映する。
  - `/opt/autostream/local-executor/ports`（root:root 0700）配下の4つの固定sidecarだけをlocal executorへ書込許可し、root-owned envの旧値をcheckpointして新値をatomic stageする。
  - sidecarはサービス別bind変数と`AUTOSTREAM_CONFIG_REVISION`の正確な2行だけを許可し、未知キー、重複、symlink、unsafe parent、owner/mode不整合を拒否する。
  - `ProtectSystem=strict`を維持し、token、policy、credentialを含む`/etc/autostream`全体へ書込権限を与えない。
  - 4サービスのsystemd `EnvironmentFile`、installer、adapter、policy example、testsを同じ固定path契約へ揃える。
  - `AUTOSTREAM_BIND_ADDR`と`OBSERVABILITY_BIND_ADDR`のサービス別adapterを実装する。
  - local executorがhost lock取得後に実bind競合を再検査する。
  - listener owner、service ID、health、version、config revisionとconfig SHA-256成功後にだけapplied portを更新する。
  - 失敗時は旧env、旧port、旧releaseへrollbackする。
- Repositories:
  - `autostream-control-panel`
  - `autostream-worker`
  - `autostream-encoder-recorder`
  - `autostream-discord-bot`
  - `autostream-observability`
  - `autostream-contracts`
- Dependencies: Slices 1、3–5.
- Acceptance:
  - 1024、非既定値、65535が成功する。
  - 1023、65536、予約port、同一host/namespace競合を拒否する。
  - 別hostの同一portは許可する。
  - health失敗、bind競合、process crash時に旧portへ復帰し、Control Panel applied値と一致する。
  - Heartbeat reported driftがdesired/appliedを書き換えない。
  - packaged local executorが`ProtectSystem=strict`のまま4つのsidecarを更新でき、`/etc/autostream`へは書き込めない。
- Verification:
  - 各Go repoの`go test ./...`と`go build ./...`。
  - 非既定portで`/health`、`/updater/version` smoke。
  - 実systemd VMでport変更、rollback、reboot/reconcile。
- Rollback/safety:
  - software update jobとport jobを同時実行しない。
  - active portは検証成功まで変更しない。
- Change Review focus:
  - config transaction、port reservation、service-specific env、fake health、rollback completeness。

### 7. Support Docker and reverse-proxy port mappings

- Value:
  - container内部port、host published port、Node広告portが異なる構成でも任意portを安全に管理できる。
- Work:
  - UI/APIへdirectとadvanced mappingを追加する。
  - Composeを`${SERVICE_PORT:-既定値}`等の明示変数で構成し、container内部portとpublished portを分離する。
  - Node広告endpoint、Compose network health endpoint、host published endpointを別々に保持する。
  - Compose変更は既存trusted frozen model/digestを維持し、新しい承認済みCompose revisionを作る。
  - port mapping変更後にcontainer identity、image/platform digest、health/version、config revisionを再検査する。
  - reverse proxyでは公開originを維持したままlocal listen portを変更する。
- Repositories:
  - 各Node service repoの`docker-compose*.yml`
  - `autostream-control-panel/internal/updateagent/docker_*`
  - `autostream-docker`
  - `autostream-docs`
- Dependencies: Slice 6.
- Acceptance:
  - advertised 443 / local 18084 / container 8080のような非同一構成が動作する。
  - Compose digest変更なしにport mappingを勝手に変更できない。
  - mapping失敗時に旧Compose digest、旧env、旧image、旧portへ復帰する。
  - 同じhost portを別containerが占有している場合に事前拒否する。
- Verification:
  - 全Composeの`docker compose ... config`。
  - default/non-default container/published port smoke。
  - amd64/arm64 image build、immutable digest、forced rollback。
- Rollback/safety:
  - systemd slice完了後に開始する。
  - `autostream-docker`既存未commit変更を別diffとして保護する。
- Change Review focus:
  - advertised/listen/publishedの混同、Compose digest、container ownership、dirty worktree保護。

### 8. Expose migration and port state clearly in the operator UI

- Value:
  - 管理者がSSHや8090を意識せず、hostの移行状態とport変更結果を判断できる。
- Work:
  - Host Agent登録ではPort/SSL/API URL欄を非表示にする。
  - Updater status API 8090設定をpull_v2 hostでは表示しない。
  - direct modeでは「サービスport」1項目、advanced modeでは「公開endpoint」「ローカル待受」「Docker published」を分ける。
  - desired/applied/reported、pending、drift、rollback、blocked reasonを表示する。
  - transport mode、observe-only readiness、ownership epoch、agent/helper versionを表示する。
  - 切替操作はactive jobなし、Agent online、policy applied、probe成功、self-update readyを前提にする。
  - 旧SSH bootstrap UIはssh_v1 hostにだけ残し、pull_v2へ切替後は表示しない。
- Likely areas:
  - `web/src/features/nodes/node-registration-view.tsx`
  - `web/src/features/application/updater-settings-panel.tsx`
  - `web/src/features/application/updater-host-bootstrap-panel.tsx`
  - `web/src/lib/system-updates.ts`
  - `web/src/types/domain.ts`
- Dependencies: Slice 1でUI contractを開始できる。最終動作はSlices 2–7に依存。
- Acceptance:
  - portless Host Agentにport入力がない。
  - 未適用portを稼働中portとして表示しない。
  - direct/advancedの違いと影響範囲が明示される。
  - busy/ambiguous/rollback中に競合する保存・transport切替を拒否する。
  - keyboard、focus、loading/error/empty statesを含む。
- Verification:
  - `npm ci`
  - `npm run typecheck`
  - `npm run lint`
  - `npm run test:system-updates`
  - `npm run build`
  - 実ブラウザで登録、port変更、失敗rollback、transport switchを確認。
- Rollback/safety:
  - backend capabilityがない場合は旧UIを表示し、新操作を送らない。
- Change Review focus:
  - misleading port labels、pending/effective表示、double submit、stale revision、accessibility。

### 9. Publish the Bridge release, migrate host by host, then remove legacy SSH

- Value:
  - fleet全体を止めず、ホスト単位でpull_v2へ移し、最終的にSSH資産を安全に撤去できる。
- Work:
  1. Contractsとadditive DB/APIを含むBridge Control Panelを先にreleaseする。
  2. Host Agent、local executor、units、installer、manifest、checksum、attestationをHost Releaseへ追加する。旧12資産はBridge期間中維持する。
  3. 各hostへAgentをobserve-onlyで配置する。
     - 既存host: 現SSH updaterを最後に1回自動利用、またはlocal console install。
     - 新規host: Node installerに同梱し、SSH設定を作らない。
  4. 非Control Panel systemd hostでpull update、forced rollback、port change、Control Panel outage、Agent restartをcanaryする。
  5. Docker canaryを実行する。
  6. active jobがないhostからownership epochを切り替え、`pull_v2`を唯一のactive transportにする。
  7. Control Panel hostを最後に移行する。
  8. bake期間後、新しいreleaseで中央Updater、8090 listener、SSH key、`authorized_keys`、sshd drop-in、remote helper、SSH bootstrap UI/docsを撤去する。
  9. 全service tag公開後に`autostream-docker/source-versions.env`を更新し、新しいDocker bundle tagを発行する。既存tagは動かさない。
- Dependencies: Slices 1–8.
- Acceptance:
  - SSH/22と8090を遮断した本番相当systemd/Docker E2Eが成功する。
  - Agent/helper自己更新、token rotation、crash recovery、port rollbackがSSHなしで成功する。
  - 旧agentは新manifestをconsumeせず、minimum protocol/version不足でfail closedする。
  - 全hostがpull_v2か、明示的な未移行リストへ分類される。
  - legacy撤去release後にSSH更新設定と8090 listenerが残っていない。
- Verification:
  - Control Panel: `go test ./...`, `go build ./...`, `go vet ./...`, MariaDB integration、Web全gate。
  - Contracts: `go test ./...`。
  - 全Node repo: `go test ./...`, `go build ./...`, non-default port smoke、Host Release dry-run。
  - Docker: `python -m unittest discover -s tests -v`、全5image no-cache build、Compose smoke、manifest test。
  - Docs: `npm ci`, `npm run docs:check`, `npm run docs:build`。
  - Workflows: actionlint、release asset name/count、checksum、attestation、immutable release確認。
- Rollback/safety:
  - Bridge期間のapplication rollback先はBridge版。
  - host切替後もbake期間中はSSH資産を無効化して保持し、削除は別releaseにする。
  - legacy削除後のssh_v1復帰は自動rollbackではなくmanual recovery扱い。
- Change Review focus:
  - release ordering、asset compatibility、host migration inventory、tag immutability、real Linux/Docker evidence。

## Verification gates

### Gate A: Contract and migration safety

- 旧DB snapshot、旧Updater policy JSON、旧agent responseをBridge版で読み込める。
- MariaDB上でhost ownershipとport reservationの同時実行試験が通る。
- Host A tokenでHost B job/grantへ到達できない。

### Gate B: Local privilege boundary

- Agent UID以外はexecutor IPCへ接続できない。
- root recovery supervisorだけが固定payloadのwatchdog statusを要求でき、Agent UIDからのwatchdog statusとroot peerからの通常mutationを双方拒否する。
- watchdog statusは2秒timeoutのread-only handshakeで、state初期化、grant recovery、mutationを行わない。
- Agentから任意command/path/URL/unit/imageをrootへ渡せない。
- secretがargv、env、log、journal、plan fileへ残らない。

### Gate C: Crash and replay safety

- grant consume、stop、config switch、start、health、result persistの前後でkillしても二重mutationしない。
- 再起動後は新状態の検証成功または旧状態rollbackへ収束する。
- stale epoch、stale lease、別session、別plan、二重grantを拒否する。
- self-updateはfileとdirectory entryの各sync/rename faultで旧slotを保持し、安全な単一`.old`以外を自動復元しない。
- fresh processはslot marker、2 binary digest、owner/mode、version、commit、release、protocolのdriftをswitch/restart前に拒否する。
- staging failureは`stable + failed_generation`を先に保存し、一致するprepared/consumed/applied grantをreceipt-free terminal `phase=failed`へ収束させる。terminal stateは`token_sha256`とexact bindingを保持し、同一IPC lost-response replayは再consume・再applyなしのno-op success、異bindingは拒否となる。

### Gate D: Arbitrary port behavior

- 同一host競合拒否、別host同port許可、予約port拒否。
- direct、Docker、reverse proxyでadvertised/listen/healthの対応が正しい。
- port変更失敗時に旧portとControl Panel applied値が一致する。
- 偽health processを成功扱いしない。

### Gate E: SSH-free production-shaped proof

- firewallで22と8090を閉じる。
- 受信socketなしのHost Agentがoutbound HTTPSだけで動作する。
- update、port change、rollback、Agent/helper self-update、token rotation、Control Panel outage recoveryを実行する。
- systemdとDockerの両方で証明する。

## Risks and dependencies

### P0 blockers

- Shared Release Tokenを長期credentialのまま各Host Agentへ配る設計。
- AgentのHeartbeat自己申告をhost/target/port認可の根拠にする設計。
- 中央UpdaterとHost Agentのsplit-brainを防ぐownership epochがない状態。
- Portを拘束しないmutation grant。
- desired/applied/reported endpointが同じDB列を共有する状態。
- Agent/helper自身のSSH-free update/rollbackが未完成の状態。
- Docker Compose digestを更新せず、Node登録値だけでmappingを書き換える設計。

### Operational risks

- 既存hostへ最初のHost Agentを置くためのbootstrap手段は完全には消せない。
- Control Panel public originを同時変更すると全Agentが切断される。
- Port変更中のservice outageは避けられない。maintenance/when-idle policyが必要。
- 旧SSH資産削除後は、旧方式への自動rollbackができない。
- 各repoのrelease順を誤ると旧agentが新manifestをconsumeする可能性がある。

### Mitigations

- Additive Bridge release、observe-only、host単位exclusive切替、Control Panel host最後、legacy削除は別release。
- immutable artifact、minimum agent/protocol version、capability gate。
- active job/policy mutation/token rotationの排他。
- desired/applied state machineとlocal durable rollback。
- release前にdirty worktreeとsource version pinを再監査する。

## Parallel work opportunities

Slice 1のcontract namesとDB shapeが確定した後、次を並列化できる。

1. **Control Panel data/API**
   - Store、MariaDB、claim/grant、endpoint state、ownership epoch。
2. **Host Agent / local executor**
   - poll/recovery再利用、IPC、root policy、probe/apply/reconcile。
3. **Node service adapters**
   - Worker、Encoder、Discord、Observabilityのbind/config/systemd smoke。
4. **Docker mapping**
   - Compose variables、digest transaction、published/container port。
5. **Web UI**
   - endpointless registration、port state、migration readiness。
6. **Contracts/docs/release**
   - OpenAPI/schema、runbook、asset/workflow contract。

共有衝突が大きいファイルは`internal/httpapi/system_updates.go`、`internal/store/updater_policy.go`、`internal/updateagent/config.go`、`web/src/lib/system-updates.ts`である。これらのinterfaceとownerを先に固定し、統合順をSlice 1 -> Agent/executor -> service adapters -> UI -> releaseとする。

## Handoff

- Next module: `implementation` -> `change-review`
- Goal: Host self-updateのP1電源断安全性を閉じたcurrent worktreeについて、最終の全repository gate、文書同期、独立reviewまで完了する。
- Evidence:
  - recovery protocol 2、directory fsync、reserved artifact recovery、fresh-process slot検証、failed generationとreceipt-free terminal `phase=failed` grant収束、同一IPC replay no-op success、異binding拒否、root-only watchdog statusのsource/focused testsがある。
  - stage grant consumeはjobを同じdurable transactionで`staging`へ予約し、stage claim後のterminal cancelを拒否する。
  - Docker 29.6.2 / Compose 5.3.1のisolated root DINDでfresh-process reconcile、unhealthy rollback、foreign ownerのgrant前拒否を確認した。
  - 上記はlocal source/test証拠であり、公開Host Release、実systemd/Docker canary、22/8090遮断E2E、release/deployの証拠ではない。
- Mutable files:
  - current taskで承認された各repositoryの既存移行変更と検証用fixture。
  - `autostream-docs`のoperations、Node登録、Docker runbook、docs contract checker。
  - このmigration planと再開handoff。
- Protected/dirty:
  - `autostream-docker/.github/workflows/publish-ghcr.yml`
  - `autostream-docker/services/control-panel/Dockerfile`
  - `autostream-docker/source-versions.env`
- Risks:
  - local focused proofを公開artifact、実systemd/Docker、fleet移行のproofと誤認すること。
  - 大量dirty worktreeや保護3ファイルをcleanup、上書き、commitすること。
- Next check:
  - 最新treeのGo/Linux/MariaDB/Web/Contracts/Node 4サービス/Docker image/Docs/Compose/workflow gateを集約し、`git diff --check`、全repo status、保護3ファイルhashを確認して独立change reviewへ渡す。
- Overrides:
  - ユーザーはSSHを最終構成から廃止し、Node portを変更可能にする方針を選択済み。
  - commit、push、tag、Release、deploy、既存tag移動は行わない。

### Checkpoint

- Current skill: `implementation` / `change-review`
- Gate: IN PROGRESS
- Completed:
  - additive Bridge contract、execution-host ownership fence、desired/applied/reported endpoint、port reservation。
  - 受信TCPを持たないHost Pull Agent、root-owned Local Executor、Auto Configure、pull_v2 software update/recovery。
  - 4つのsystemd Node serviceの任意port変更、rollback、UIのdesired/applied/reported表示。
  - 全4 Node repositoryの任意bind設定、systemd sidecar、Composeのport変数化。
  - Docker advertised/published/container portの独立job、fixed policy/frozen Compose fence、API/UI、drift/result binding。
  - staged Runtime Token rotationのStore、HTTP、Host Agent、Local Executorとemergency recovery。
  - Webのtypecheck、lint、44件のsystem-update test、60 route production build。
  - Host self-updateのrecovery protocol 2、directory fsync、旧slot復元、reserved artifact recovery、fresh-process slot検証。
  - `stable + failed_generation`先行永続化、prepared/consumed/applied grantのreceipt-free terminal `phase=failed`収束、同一IPC replay no-op success、異binding拒否、stage claim後cancel拒否。
  - healthy Executorのrestart/identity/protocol確認とUID 0専用・2秒timeout・非mutationのwatchdog status。
  - Docker 29.6.2 / Compose 5.3.1のisolated root DINDでfresh-process reconcile、unhealthy rollback、foreign owner拒否。
- In progress:
  - 最新worktreeの全Go/Linux/MariaDB/Web/Contracts/Node 4サービス/Docker image/Docs/Compose/workflow gate結果の集約。
  - 文書同期後のdocs check/build、全repo diff/status、保護3ファイルhash確認、独立change review。
- Blocked by:
  - local implementationのP1 blockerはなし。
  - release/fleet切替は、公開Host Releaseのasset/checksum/attestation、実systemd process kill/reboot、実Docker host canary、22/8090遮断E2E、production release/deployが未実施。
- Next skill: `implementation` -> `change-review`
- Next check:
  - 文書同期後のdocs check/buildと旧未完了文言のゼロ確認を行う。
  - 最新treeの全gate証拠、`git diff --check`、全repo status、保護3ファイルhashを集約し、独立change reviewでblockerを再確認する。
- Action: continue now
- Todo tail: Next / upcoming task: 文書同期後の全gateと独立change reviewを完了する。
- Release boundary: commit、push、tag、Release発行、deployment、既存tagの移動は未実施。

### Slice 9 local Review Gate addendum（2026-07-28）

- Review Gate: PASS（ownership、release workflow、fleet gateの独立レビューでP0〜P3なし）。
- Completed:
  - legacy SSH ownerをserver側に保存するmigration 058と、全CAS/policy/token/activity fenceを持つ`pull_v2 → ssh_v1 → pull_v2` round-trip。
  - ownership epoch overflow拒否、strict Contracts/OpenAPI/JSON Schema、UIのDanger Confirm・ambiguous refresh-only。
  - Contracts、Control Panel/Host、Node 4、Docsのimmutable release workflow。exact assets/body/digest/tag/attestationを検証し、失敗draft/release/tagを自動削除しない。
  - exact release matrix、Docker source freeze、authoritative roster、receipt chain、typed systemd/Docker canary、Control Panel last、24時間bake、別legacy撤去releaseを検証するfleet gate CLI/runbook。
  - MariaDB 11.4のactivation/deactivation round-trip、legacy scope/全target不足拒否、migration 058 partial replay。
  - fleet gate 62/62、docs check/build、Web 45/45・typecheck・lint、Node 4全Go/release focused、Contracts focused/full、release workflow actionlint。
- Deliberately not repeated:
  - 変更非該当で直前GREENの全5 Docker image no-cache build、DIND full matrix、無変更packageの重複全回帰。既存証拠を保持し、今回の差分境界をfocused検証した。
- Ship Gate: BLOCKED pending operator-owned external proof.
  - immutable public release URL/run ID/tag commit/exact assets/checksum/attestation。
  - 実systemd/Docker canary、process kill/reboot、Host Agent/Executor self-update、token rotation、Control Panel outage、port rollback。
  - host listener/firewall snapshot、外部22/8090遮断、outbound HTTPS heartbeat/job。
  - authoritative full-host roster、phase receipt、Control Panel host最後、24時間以上のincident-free bake。
  - bake後の別immutable releaseによるSSH/8090撤去と撤去後E2E。
- Operator runbook: `autostream-docs/docs/runbooks/bridge-release-fleet-gate.md`。
- Release boundary: commit、push、tag、Release発行、deployment、既存tag移動は未実施。これらはユーザーが手動で行う。
