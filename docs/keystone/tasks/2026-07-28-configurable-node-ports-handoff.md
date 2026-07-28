# Codex handoff: SSH-free updater and configurable Node ports

この文書を新しい Codex タスクの指示として使用すること。作業ルートは
`C:\Users\yhika\Documents\Autostream`。

## 目的

既存の移行計画
`autostream-control-panel/docs/keystone/tasks/2026-07-28-ssh-free-updater-configurable-node-ports-migration.md`
を、ローカル実装・検証・運用文書同期まで完了する。

確定済みの仕様:

- 物理ホストごとに、外向き通信だけを行う非 root Host Agent を1つ置く。
- root 権限処理は固定 Unix socket の Local Executor に限定する。
- Host Agent に受信 API port（旧 8090）は持たせない。
- Worker、Encoder Recorder、Discord Bot、Observability の各サービス port は個別に変更可能にする。
- systemd/local の service port は `1024..65535`。
- Docker は advertised port を `1..65535`、published/container port を `1024..65535` とし、published bind は `127.0.0.1` 固定。
- desired / applied / reported を分離し、予約、probe、commit、rollback をトランザクションとして扱う。
- Auto Configure は systemd 導入だけを対象とする。Docker の初期 root policy と frozen Compose baseline は手動準備が必要。
- reverse proxy の自動編集は、別の権限モデルが未定義のため対象外。
- 既存 tag は移動しない。commit、push、tag、Release、deploy は明示承認なしに行わない。

## 完了済み

- SSH observer から pull ownership へ移行する Bridge 契約。
- endpointless Host Agent、Local Executor、systemd installer/uninstaller、Auto Configure。
- `pull_v2` の software update/recovery。
- systemd 任意 port 変更。
- Docker の advertised/published/container port 分離、loopback bind、ledger、rollback/reconcile。
- runtime token の stage/claim/proof/activate/revoke と緊急回復。
- Web UI の transport/port state 表示。
- Contracts/OpenAPI/JSON Schema と Node 4サービスの port/env/health 契約。
- Host self-update の immutable release binding、grant claim、同一bindingのterminal replay no-op成功、異binding拒否、MariaDB migration 057。
- 通知表示と履歴名の改善を含む既存変更は保持すること。

直近までの検証:

- `autostream-contracts`: `go test ./... -count=1`、`go build ./...` PASS。
- Control Panel Web: typecheck、lint、system-updates 44 tests、build（60 routes）PASS。
- Worker / Encoder Recorder / Discord Bot / Observability:
  `go test ./...`、`go build ./...`、`go vet ./...` PASS。
- Compose 12構成を parse。base/local の非既定 published/container port と
  全サービスの loopback bind を確認。
- MariaDB 実環境:
  migration 057 の legacy revision drift、二重実行、NULL claim 拒否、
  Host self-update lifecycle/grant/strict proof が PASS。
- docs check PASS。
- repo-local `.gocache*` / `.gotmp*` は 3523.4 MiB 削除済み。

上記は自己更新P1修正前の統合テストを含む再開時点の履歴である。以後は下記の
P1 focused proofと、最終統合gateの結果を分けて扱う。古いfull PASSだけを最新treeの
証明にせず、公開releaseや実host proofへも読み替えないこと。

## 再開時点のP1 blockerと収束（履歴）

独立レビューでは、`internal/updateagent/host_self_update_executor.go`のA/B slot
stagingが従来binary fileだけをfsyncし、`bin`、temporary slot、`slotsRoot`の
directory entryをcrash-durableにする前に`state=staged` / `grant=applied`を
永続化できるP1が見つかった。電源断後にstaged stateと旧版・欠落slotが
組み合わさり得る問題だった。

current worktreeでは、このblockerを次の契約へ収束させた。

- Host Agent capability、Host Release manifest、directive、grant、root plan、
  durable stateを`recovery_protocol_version=2`へexactに結び、protocol 1を拒否。
- 新規state rootと親entry、`bin`、temporary slot、`slotsRoot`、rename/removeの
  各境界をdirectory fsyncし、failure時は旧slotを復元。
- `.new`を回収し、安全な単一`.old`だけをdurable stateと照合して復元。
  multiple、malformed、unsafe、state矛盾はmanual recoveryへfail closed。
- slot root、`bin`、2 binary、markerのowner/mode、generation、version、commit、
  artifact/release binding、4 protocol、2 binary SHA-256、`--version`を
  activate、resumed activating、rollback、`current`再構築前にfresh processで再検証。
- staging失敗を`stable + failed_generation`として先に保存し、同じgenerationの
  `prepared` / `consumed` / `applied` stage grantを、`token_sha256`とexact bindingを
  保持しreceiptを持たないcredential-free terminal `phase=failed`へ収束。
- socket response喪失後の同一IPC requestはgrantを再consume・mutationを再applyせず
  no-op成功とし、異なるbindingは拒否。
- stage grant consumeとjobの`staging`予約/revision更新を同じdurable transactionで
  行い、stage claim後のterminal cancelを拒否。
- root recovery supervisorから固定Unix socketへ、UID 0専用、2秒timeout、
  credentialなし、非mutationの`host_self_update_watchdog_status`を追加。
  hung socketやstatus不一致ではrollback fenceを解除しない。

directory sync fault、reserved artifact recovery、fresh-process tamper、grant crash
matrix、healthy Executor restart/identity、root-only/hung socketのsource/focused testsは
追加済みで、Linux container/root fixtureでも実行した。Docker port jobは
Docker 29.6.2 / Compose 5.3.1のisolated root DINDで、実process crash後の
fresh-process reconcile、grant二重消費なし、unhealthy rollback、foreign ownerの
grant前拒否までPASSした。

これらはlocal source/test証拠である。公開Host Releaseのasset/checksum/attestation、
実GitHub download、実systemdのsocket activation/process kill/reboot、
amd64/arm64 canary、全Docker imageの公開証拠、22/8090遮断E2E、fleet移行、
release/deployは未実施であり、production-readyの証明ではない。

## 次に行うこと

1. 最新treeの最終gateを集約する。
   - Control Panelの全Go test/build/vet、Linux build/container tests。
   - MariaDB 11.4のmigration 057、Host self-update lifecycle、全`TestMariaDB`。
   - Web、Contracts、Node 4サービス、Compose、Docker image、workflow。
2. 文書と計画checkpointを最新結果に同期する。
   - `autostream-docs/docs/operations/system-updates.md`
   - `autostream-docs/docs/control-panel/node-agent-registration.md`
   - `autostream-docs/docs/deployment/docker.md`
   - 元のmigration planと本handoff。
3. `git diff --check`、全repo status、独立change reviewを完了する。
   - `autostream-docker`の既存dirty 3件
     (`.github/workflows/publish-ghcr.yml`,
     `services/control-panel/Dockerfile`,
     `source-versions.env`) はユーザー変更として保持し、hashを再確認する。
4. commit、push、tag、Release、deploy、既存tag移動は行わない。

## 再開時点のworktree snapshot（履歴）

- Control Panel: modified 60 / untracked 183。
- Contracts: modified 15 / untracked 23。
- Worker: modified 14 / untracked 2。
- Encoder Recorder: modified 12 / untracked 4。
- Discord Bot: modified 14 / untracked 4。
- Observability: modified 12 / untracked 4。
- Docs: modified 18。
- Docker: modified 3（上記の保護対象のみ）。
- commit / push / tag / release / deploy は未実施。

上記件数は再開時点のsnapshotであり、現在値は最終の各repo `git status`を正とする。
大量の変更は今回の移行実装であり、無関係として削除しないこと。current worktreeと
本handoff、migration plan末尾のCheckpointをauthoritativeにして、最終統合検証と
change reviewへ進むこと。

## Slice 9 local Ship Gate checkpoint（2026-07-28）

current worktreeで、公開・fleet操作の前に閉じられるSlice 9のsource gateを次まで進めた。

- migration 058でBridge切替前のlegacy SSH ownerを保存し、`pull_v2 → ssh_v1 → pull_v2`をserver-owned CASとして実装した。legacy tokenの`updates.claim/report/authorize`、pull policy全target coverage、job/grant/self-update/rotation/recovery、epoch/revision/digestをfail-closedで再検証する。
- ownership epochの`int64` overflowをStore、JSON Schema、OpenAPIで拒否し、MariaDB 11.4でactivation/deactivation round-trip、unsafe legacy route拒否、migration 058 partial replayを確認した。
- Contracts、Control Panel、Node 4サービス、Docsのrelease workflowは、exact asset/body/tag/attestationを検証する。失敗時のrelease/tag自動削除を廃止し、公開成功を再検証した後だけworkflow-owned staging tagを削除する。
- `autostream-docs/scripts/bridge-fleet-gate.mjs`とrunbookを追加した。exact release matrix、Docker source freeze、authoritative roster、phase receipt chain、systemd→Docker→non-control→Control Panel順、typed canary evidence、24時間以上のbake、別legacy撤去releaseを検証する。focused testsは62/62 PASS、docs check/buildもPASS。
- Node 4サービスの全Go testとrelease workflow focused tests、Web 45/45・typecheck・lint、Contracts focused/full、Control Panel変更package、release workflow actionlint、MariaDB重点integrationはPASSした。時間短縮のため、今回変更していない全5 Docker imageのno-cache rebuildと既にGREENのDIND matrixは再実行していない。
- 独立change reviewはownership、Contracts/Control Panel/Docs release、Node 4 release、fleet gateのすべてでP0〜P3なし。

これはlocal Review Gateの証拠であり、Ship Gate完了ではない。commit、push、tag、release、deployは行っていない。公開immutable release、実systemd process kill/reboot、amd64/arm64 download、実Docker host、22/8090遮断E2E、authoritative fleet inventory、host単位移行、24時間以上のbake、別releaseでのlegacy撤去は未実施である。

次のoperator actionは`autostream-docs/docs/runbooks/bridge-release-fleet-gate.md`に従う。release順はContracts、Control Panel/Host、Node 4、Docker、Docsで、observer導入後は非Control Panel systemd canary、Docker canary、non-control fleet、Control Panel host最後、bake、別legacy撤去releaseの順とする。各phaseでCLIが`PASS`しない場合は停止する。
