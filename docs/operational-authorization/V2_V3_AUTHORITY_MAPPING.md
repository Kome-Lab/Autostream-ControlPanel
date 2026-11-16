# S13 実装における読み方

承認済み判断は **Role=PRESERVE / Scope=HYBRID / Workload Identity=SEPARATE**。この先に保全した旧資料は、S13着手前の固定Sourceレビューです。旧資料内の「未実装」「0/99」「NOT_RUN」は当時の設計・正式移行試験の状態を表します。S13の実装と新しい自動試験結果は [仕様](OPERATIONAL_AUTHORIZATION_SPECIFICATION.md) と [検証報告](S13_VERIFICATION.md) で区別します。

S13では全99 Permission文字列を閉じたOperational Operation Catalogとして維持し、実際のRole内容・User Role割当からGrantを展開します。Role名だけの権限付与ではありません。以前のDIRECT66／SPLIT24／COMPOSITE4／REVIEW5は将来の細分化候補であり、S13の実装件数へ転用しません。実ルート未使用の5件も削除しません。

v2のGlobal権限をGlobalとして保持し、既存Handler/Storeの対象・所有・状態・Assignment・委任条件を残します。super_adminの割当時特例とRole作成/更新時の部分集合検証を区別します。Bootstrap／Trust Root／独立台帳／Recovery／鍵管理は含めません。

Permission入口175件、Any入口10件に加え、Session-only登録でHandlerがshow/hide権限を選ぶ PUT /streams/{id}/video-cover-state 1件にもOperational投影を適用します。登録構造とHandler内の正確な分岐は変更しません。ほかのSession-only経路、BのEvidence入口、Cの独立認可は維持します。

実DBのCustom Role、User割当、実ユーザー・実環境での移行は未取得・未実施です。Human PermissionをService Token、Host/Node/Worker/Updater/Local Executor資格へ変換しません。製品全体受入はNOT_RUN、Phase 1終了保留、本番移行未許可、正式v2復帰NOT_CERTIFIEDを維持します。

---

# V2 → V3 Authority Mapping Matrix

作成日：2026-11-14 JST。方針A：既存ユーザー・通常業務の実効権限と対象範囲を原則維持する。

本資料はSource確認と設計案。実装・テスト・Build・CI・DB照会・Migration適用・実権限変更はいずれも未実施。

## 1. 固定基準と読み方

- v2 ControlPanel：`93181192d8f036bae40b490957489c9ee8290769`。権限99件とGo HTTP 261 Method/Pathを固定Sourceから対応付けた。
- v3 ControlPanel：`d42a0fb2212c2378b822effc9b8dfd7031d457cb`。Contracts：`45aaa9932bcd92c728c86c95349853724fe1accc`。Updater：`cdc106ab795623e41e2fe42a73c598c782e5c8de`。
- **Source事実**は固定CommitのGo/SQLへリンクする。**設計案**は本資料の`panel.*`候補、展開規則、移行手順であり、実装済みAPIを意味しない。実DBのUser/Custom Role/割当は未取得。
- E001〜E261はV2_PERMISSION_ROUTE_MATRIX.mdの全Route識別子。P001〜P099はDefaultPermissionsの順序。

## 2. 現在のv3モデルとの実装差

現行v3の`Action`は`registry.reference_only`のみである。CP Intent/DecisionはCanonical Scope、Execution、Candidate、Guard、Revision等を扱い、既存Panel業務全体の汎用Permission catalogではない。[internal/v3authorization/types.go:13](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/d42a0fb2212c2378b822effc9b8dfd7031d457cb/internal/v3authorization/types.go#L13)

ManagementGrantはIssue/ReadHistoryと別の認可情報であり、Realm/Host/Resource/Backend/Candidate/Capability/Policy/Trust/Membership/Administratorのwrite、read、subjects.delegateを列挙する。Scopeは正確なRealm/Host/Resource/Backend、Actionも上記一種類。Subjects上限32、Operations上限12等の検証がある。このモデルへ99権限を単に`administrator.write`又は`read`で押し込むと、意味も対象範囲も失われる。[internal/v3authorization/management_types.go:14](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/d42a0fb2212c2378b822effc9b8dfd7031d457cb/internal/v3authorization/management_types.go#L14) [v3evidence/types.go:29](https://github.com/Kome-Lab/Autostream-Contracts/blob/45aaa9932bcd92c728c86c95349853724fe1accc/v3evidence/types.go#L29)

したがって本表は**PanelのHuman業務Capabilityを追加設計するための論理対応**である。94権限について候補を定義し、5件をREVIEWで保全した。現行v3へ製品接続済みと証明した件数は**0/99**。現在のManagementGrantへ自動登録・Bootstrapする設計ではない。

## 3. 分類と集計

DIRECTは現行の一つの業務意味を同義に保持する候補、SPLITは一つの旧Permissionを複数の明示的操作へ展開、COMPOSITEは別Permissionとの組合せを保つ必要があるもの、REVIEWは実装上の意味を追加判断すべきもの。EXCLUDEDはHuman権限として移行しない実行資格、UNKNOWNはSourceから意味を確定できない場合を指す。

集計の母数はDefaultPermissionsの**99キー**。主分類は1キー1件とし、分割と複合条件が併存する場合はSPLITを主分類、追加AND条件を行内へ記す。Service資格は別表なので99件のEXCLUDEDへ加算しない。

| 主分類 | 件数 |
| --- | --- |
| DIRECT | 66 |
| SPLIT | 24 |
| COMPOSITE | 4 |
| REVIEW | 5 |
| EXCLUDED | 0 |
| UNKNOWN | 0 |

論理対応候補あり94/99、REVIEW保全5/99。UNKNOWN=0は「Sourceの権限キーを未分類のまま残していない」という意味であり、実データ・外部Service・新v3仕様まで確定した意味ではない。

## 4. 全Permission Mapping

Role列のsuper_adminは初期作成時の99権限付与を意味する。現在の実DBで99件が揃っているとは断定しない。Custom Roleは実際にそのPermissionを保持するRoleを指し、全Custom Roleへの自動付与ではない。

Scopeの`Panel内`は同じCPインスタンスのHuman業務対象範囲。既存Sourceにない「自分が作成したStreamだけ」「割当されたHostだけ」等の制限を追加しない。逆にMedia等に既にある本人所有条件は維持する。

| ID | v2 Role | v2 Permission | HTTP / Operation | 追加条件 | 対象Scope | v3 Capability候補（設計） | v3 Operation候補（設計） | 移行判定 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| P001 | super_admin / 該当Permissionを持つCustom Role | `users.read` | E244 GET `/users`<br>E246 GET `/users/{id}`<br>E254 GET `/users/{id}/oauth-links` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内User（他人含む） | `panel.users.read` | `users.read` | DIRECT |
| P002 | super_admin / 該当Permissionを持つCustom Role | `users.create` | E245 POST `/users` | Role付き作成だけroles.assignも必要。通常作成はpending_password_change。 条件詳細は対応E行。 | Panel内User（他人含む） | `panel.users.create` | `users.create` | COMPOSITE |
| P003 | super_admin / 該当Permissionを持つCustom Role | `users.update` | E247 PUT `/users/{id}`<br>E250 POST `/users/{id}/unlock` | Role更新にはroles.assign。自分のRole更新はsuper_adminも不可。 条件詳細は対応E行。 | Panel内User（他人含む） | `panel.users.update_profile`<br>`panel.users.update_roles`<br>`panel.users.unlock` | `users.update_profile`<br>`users.update_roles`<br>`users.unlock` | SPLIT |
| P004 | super_admin / 該当Permissionを持つCustom Role | `users.disable` | E248 POST `/users/{id}/disable`<br>E249 POST `/users/{id}/lock` | 対象super_adminにはactorもsuper_admin。最後のactive管理者保護の適用範囲を維持。 条件詳細は対応E行。 | Panel内User（他人含む） | `panel.users.disable`<br>`panel.users.lock` | `users.disable`<br>`users.lock` | SPLIT |
| P005 | super_admin / 該当Permissionを持つCustom Role | `users.delete` | E253 DELETE `/users/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内User（他人含む） | `panel.users.delete` | `users.delete` | DIRECT |
| P006 | super_admin / 該当Permissionを持つCustom Role | `users.reset_password` | E251 POST `/users/{id}/reset-password`<br>E252 POST `/users/{id}/force-password-change` | 他super_adminの再設定はsuper_adminのみ。通常actorは対象Permissionが自己の部分集合であること。forceはstatus経路。 条件詳細は対応E行。 | Panel内User（他人含む） | `panel.users.reset_password`<br>`panel.users.force_password_change` | `users.reset_password`<br>`users.force_password_change` | SPLIT |
| P007 | super_admin / 該当Permissionを持つCustom Role | `users.manage_mfa` | E255 POST `/users/{id}/oauth-links`<br>E256 DELETE `/users/{id}/oauth-links/{link_id}` | 実HTTPは他User OAuth link削除。手動作成は常時拒否。他人TOTP/passkey解除ルートの実在は認定しない。 条件詳細は対応E行。 | Panel内User（他人含む） | `panel.users.manage_mfa` | `users.manage_mfa` | DIRECT |
| P008 | super_admin / 該当Permissionを持つCustom Role | `roles.read` | E243 GET `/permissions`<br>E257 GET `/roles`<br>E259 GET `/roles/{id}` | Role/Permission catalogを読む。 条件詳細は対応E行。 | Panel内Role / 対象User・Provider | `panel.roles.read`<br>`panel.permissions.read` | `roles.read`<br>`permissions.read` | SPLIT |
| P009 | super_admin / 該当Permissionを持つCustom Role | `roles.create` | E258 POST `/roles` | 要求PermissionはDefaultPermissions内かつactorの実効Permission部分集合。 条件詳細は対応E行。 | Panel内Role / 対象User・Provider | `panel.roles.create` | `roles.create` | DIRECT |
| P010 | super_admin / 該当Permissionを持つCustom Role | `roles.update` | E260 PUT `/roles/{id}` | super_admin Role変更不可。非super_adminの自己所属Role更新不可。要求Permissionはactorの部分集合。 条件詳細は対応E行。 | Panel内Role / 対象User・Provider | `panel.roles.update` | `roles.update` | DIRECT |
| P011 | super_admin / 該当Permissionを持つCustom Role | `roles.delete` | E261 DELETE `/roles/{id}` | super_admin Role削除不可。非super_adminの自己所属Role削除不可、対象Permissionはactorの部分集合。 条件詳細は対応E行。 | Panel内Role / 対象User・Provider | `panel.roles.delete` | `roles.delete` | DIRECT |
| P012 | super_admin / 該当Permissionを持つCustom Role | `roles.assign` | E245 POST `/users`<br>E247 PUT `/users/{id}`<br>E048 POST `/integrations/oauth-providers`<br>E050 PUT `/integrations/oauth-providers/{id}` | users.create/update又はOAuth provider変更の該当入口と組合せ。これ単独の独立ルートはない。 条件詳細は対応E行。 | Panel内Role / 対象User・Provider | `panel.roles.assign` | `roles.assign` | COMPOSITE |
| P013 | super_admin / 該当Permissionを持つCustom Role | `streams.read` | E197 GET `/streams`<br>E208 GET `/streams/{id}`<br>E209 GET `/streams/{id}/stop-outcome`<br>E210 GET `/streams/{id}/visual-settings`<br>E211 GET `/streams/{id}/media-assets/{variant_id}`<br>E213 GET `/streams/{id}/video-cover-state`<br>E222 GET `/streams/{id}/external-e2e-config`<br>E227 GET `/streams/{id}/youtube-live-notification`<br>E235 GET `/streams/{id}/encoder-preflight`<br>E236 GET `/streams/{id}/preview/{name}`<br>E237 POST `/streams/{id}/preview-links`<br>E238 GET `/streams/{id}/audio-status`<br>E239 GET `/streams/{id}/worker-events` | 読取に加え署名付き公開previewリンク作成を含む。自身のStreamだけに限定するACLはない。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.read`<br>`panel.streams.read_stop_outcome`<br>`panel.streams.read_visual`<br>`panel.streams.read_external_e2e`<br>`panel.streams.read_preview`<br>`panel.streams.issue_preview_link`<br>`panel.streams.read_audio`<br>`panel.streams.read_worker_events`<br>`panel.streams.read_youtube_notification`<br>`panel.streams.read_encoder_preflight` | `streams.read`<br>`streams.read_stop_outcome`<br>`streams.read_visual`<br>`streams.read_external_e2e`<br>`streams.read_preview`<br>`streams.issue_preview_link`<br>`streams.read_audio`<br>`streams.read_worker_events`<br>`streams.read_youtube_notification`<br>`streams.read_encoder_preflight` | SPLIT |
| P014 | super_admin / 該当Permissionを持つCustom Role | `streams.create` | E200 POST `/streams`<br>E201 POST `/media-assets/upload-sessions`<br>E202 GET `/media-assets/upload-sessions/{session_id}/assets`<br>E203 POST `/media-assets`<br>E204 GET `/media-assets/{id}`<br>E205 POST `/media-assets/{id}/variants`<br>E206 DELETE `/media-assets/{id}` | media各ルートはstreams.create OR streams.update。assignment指定時には対応するassign/unassign条件。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.create`<br>`panel.media.upload_session`<br>`panel.media.inspect_upload`<br>`panel.media.upload`<br>`panel.media.read_owned`<br>`panel.media.create_variant`<br>`panel.media.soft_retire_owned`<br>`panel.media.delete_owned_draft` | `streams.create`<br>`media.upload_session`<br>`media.inspect_upload`<br>`media.upload`<br>`media.read_owned`<br>`media.create_variant`<br>`media.soft_retire_owned`<br>`media.delete_owned_draft` | SPLIT |
| P015 | super_admin / 該当Permissionを持つCustom Role | `streams.update` | E201 POST `/media-assets/upload-sessions`<br>E202 GET `/media-assets/upload-sessions/{session_id}/assets`<br>E203 POST `/media-assets`<br>E204 GET `/media-assets/{id}`<br>E205 POST `/media-assets/{id}/variants`<br>E206 DELETE `/media-assets/{id}`<br>E212 PUT `/streams/{id}/visual-settings`<br>E223 PUT `/streams/{id}/settings`<br>E224 PUT `/streams/{id}/runtime-settings`<br>E233 POST `/streams/{id}/mark-failed`<br>E240 POST `/streams/{id}/worker-events/test` | runtime apply、状態遷移、media OR条件を含む。assignment変更時の別Permissionを落とさない。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.update_settings`<br>`panel.streams.update_runtime`<br>`panel.streams.update_visual`<br>`panel.streams.mark_failed`<br>`panel.streams.test_worker_event`<br>`panel.media.upload_session`<br>`panel.media.inspect_upload`<br>`panel.media.upload`<br>`panel.media.read_owned`<br>`panel.media.create_variant`<br>`panel.media.soft_retire_owned`<br>`panel.media.delete_owned_draft` | `streams.update_settings`<br>`streams.update_runtime`<br>`streams.update_visual`<br>`streams.mark_failed`<br>`streams.test_worker_event`<br>`media.upload_session`<br>`media.inspect_upload`<br>`media.upload`<br>`media.read_owned`<br>`media.create_variant`<br>`media.soft_retire_owned`<br>`media.delete_owned_draft` | SPLIT |
| P016 | super_admin / 該当Permissionを持つCustom Role | `streams.start` | E225 POST `/streams/{id}/start-readiness`<br>E226 POST `/streams/{id}/start`<br>E228 POST `/streams/{id}/youtube-live-notifications/{notification_id}/recover` | 開始可否照会はMutation予約ではない。状態・assignment・Node準備条件を保持。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.check_start_readiness`<br>`panel.streams.start`<br>`panel.streams.recover_youtube_notification` | `streams.check_start_readiness`<br>`streams.start`<br>`streams.recover_youtube_notification` | SPLIT |
| P017 | super_admin / 該当Permissionを持つCustom Role | `streams.stop` | E229 POST `/streams/{id}/stop`<br>E230 POST `/streams/{id}/force-stop`<br>E231 POST `/streams/{id}/youtube/complete`<br>E232 POST `/streams/{id}/youtube/relay-static/recovery/resolve` | 通常停止、強制停止、YouTube終了、relay-static業務復旧の合計を保持。v3独立Recovery quorumとは別。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.stop`<br>`panel.streams.force_stop`<br>`panel.streams.complete_youtube`<br>`panel.streams.resolve_relay_static_recovery` | `streams.stop`<br>`streams.force_stop`<br>`streams.complete_youtube`<br>`streams.resolve_relay_static_recovery` | SPLIT |
| P018 | super_admin / 該当Permissionを持つCustom Role | `streams.retry_upload` | E234 POST `/streams/{id}/retry-upload` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.retry_upload` | `streams.retry_upload` | DIRECT |
| P019 | super_admin / 該当Permissionを持つCustom Role | `streams.delete` | E221 DELETE `/streams/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.delete` | `streams.delete` | DIRECT |
| P020 | super_admin / 該当Permissionを持つCustom Role | `streams.show_cover` | E214 PUT `/streams/{id}/video-cover-state` | E214のbody.active=trueでのみ必要。hide_coverとのANDではない。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.show_cover` | `streams.show_cover` | DIRECT |
| P021 | super_admin / 該当Permissionを持つCustom Role | `streams.hide_cover` | E214 PUT `/streams/{id}/video-cover-state` | E214のbody.active=falseでのみ必要。show_coverとのANDではない。 条件詳細は対応E行。 | Panel内Stream＋関連Assignment/Config | `panel.streams.hide_cover` | `streams.hide_cover` | DIRECT |
| P022 | super_admin / 該当Permissionを持つCustom Role | `encoder_profiles.read` | E095 GET `/profiles/encoder`<br>E103 GET `/profiles/encoder/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 encoder_profiles の種別/ID | `panel.encoder_profiles.read` | `encoder_profiles.read` | DIRECT |
| P023 | super_admin / 該当Permissionを持つCustom Role | `encoder_profiles.create` | E099 POST `/profiles/encoder` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 encoder_profiles の種別/ID | `panel.encoder_profiles.create` | `encoder_profiles.create` | DIRECT |
| P024 | super_admin / 該当Permissionを持つCustom Role | `encoder_profiles.update` | E107 PUT `/profiles/encoder/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 encoder_profiles の種別/ID | `panel.encoder_profiles.update` | `encoder_profiles.update` | DIRECT |
| P025 | super_admin / 該当Permissionを持つCustom Role | `encoder_profiles.delete` | E111 DELETE `/profiles/encoder/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 encoder_profiles の種別/ID | `panel.encoder_profiles.delete` | `encoder_profiles.delete` | DIRECT |
| P026 | super_admin / 該当Permissionを持つCustom Role | `archive_profiles.read` | E096 GET `/profiles/archive`<br>E104 GET `/profiles/archive/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 archive_profiles の種別/ID | `panel.archive_profiles.read` | `archive_profiles.read` | DIRECT |
| P027 | super_admin / 該当Permissionを持つCustom Role | `archive_profiles.create` | E100 POST `/profiles/archive` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 archive_profiles の種別/ID | `panel.archive_profiles.create` | `archive_profiles.create` | DIRECT |
| P028 | super_admin / 該当Permissionを持つCustom Role | `archive_profiles.update` | E108 PUT `/profiles/archive/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 archive_profiles の種別/ID | `panel.archive_profiles.update` | `archive_profiles.update` | DIRECT |
| P029 | super_admin / 該当Permissionを持つCustom Role | `archive_profiles.delete` | E112 DELETE `/profiles/archive/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 archive_profiles の種別/ID | `panel.archive_profiles.delete` | `archive_profiles.delete` | DIRECT |
| P030 | super_admin / 該当Permissionを持つCustom Role | `caption_profiles.read` | E097 GET `/profiles/caption`<br>E105 GET `/profiles/caption/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 caption_profiles の種別/ID | `panel.caption_profiles.read` | `caption_profiles.read` | DIRECT |
| P031 | super_admin / 該当Permissionを持つCustom Role | `caption_profiles.create` | E101 POST `/profiles/caption` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 caption_profiles の種別/ID | `panel.caption_profiles.create` | `caption_profiles.create` | DIRECT |
| P032 | super_admin / 該当Permissionを持つCustom Role | `caption_profiles.update` | E109 PUT `/profiles/caption/{id}` | 同Profile参照中のlive/starting Streamへの適用経路も既存能力。 条件詳細は対応E行。 | Panel内 caption_profiles の種別/ID | `panel.caption_profiles.update`<br>`panel.caption_profiles.apply_live` | `caption_profiles.update`<br>`caption_profiles.apply_live` | SPLIT |
| P033 | super_admin / 該当Permissionを持つCustom Role | `caption_profiles.delete` | E113 DELETE `/profiles/caption/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 caption_profiles の種別/ID | `panel.caption_profiles.delete` | `caption_profiles.delete` | DIRECT |
| P034 | super_admin / 該当Permissionを持つCustom Role | `overlay_profiles.read` | E098 GET `/profiles/overlay`<br>E106 GET `/profiles/overlay/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 overlay_profiles の種別/ID | `panel.overlay_profiles.read` | `overlay_profiles.read` | DIRECT |
| P035 | super_admin / 該当Permissionを持つCustom Role | `overlay_profiles.create` | E102 POST `/profiles/overlay` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 overlay_profiles の種別/ID | `panel.overlay_profiles.create` | `overlay_profiles.create` | DIRECT |
| P036 | super_admin / 該当Permissionを持つCustom Role | `overlay_profiles.update` | E110 PUT `/profiles/overlay/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 overlay_profiles の種別/ID | `panel.overlay_profiles.update` | `overlay_profiles.update` | DIRECT |
| P037 | super_admin / 該当Permissionを持つCustom Role | `overlay_profiles.delete` | E114 DELETE `/profiles/overlay/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 overlay_profiles の種別/ID | `panel.overlay_profiles.delete` | `overlay_profiles.delete` | DIRECT |
| P038 | super_admin / 該当Permissionを持つCustom Role | `discord_configs.read` | E080 GET `/discord/configs`<br>E082 GET `/discord/configs/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 discord_configs の種別/ID | `panel.discord_configs.read` | `discord_configs.read` | DIRECT |
| P039 | super_admin / 該当Permissionを持つCustom Role | `discord_configs.create` | E081 POST `/discord/configs` | Config作成に付随するBot Token秘密保存も含む。secrets.update追加要求はない。 条件詳細は対応E行。 | Panel内 discord_configs の種別/ID | `panel.discord_configs.create` | `discord_configs.create` | DIRECT |
| P040 | super_admin / 該当Permissionを持つCustom Role | `discord_configs.update` | E083 PUT `/discord/configs/{id}` | Config更新に付随するBot Token秘密保存も含む。期待更新時刻を確認。 条件詳細は対応E行。 | Panel内 discord_configs の種別/ID | `panel.discord_configs.update` | `discord_configs.update` | DIRECT |
| P041 | super_admin / 該当Permissionを持つCustom Role | `discord_configs.delete` | E084 DELETE `/discord/configs/{id}` | 参照中削除を拒否。付随Bot Tokenのクリアも含む。 条件詳細は対応E行。 | Panel内 discord_configs の種別/ID | `panel.discord_configs.delete` | `discord_configs.delete` | DIRECT |
| P042 | super_admin / 該当Permissionを持つCustom Role | `discord_target_presets.read` | E085 GET `/discord/target-presets`<br>E087 GET `/discord/target-presets/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 discord_target_presets の種別/ID | `panel.discord_target_presets.read` | `discord_target_presets.read` | DIRECT |
| P043 | super_admin / 該当Permissionを持つCustom Role | `discord_target_presets.create` | E086 POST `/discord/target-presets` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 discord_target_presets の種別/ID | `panel.discord_target_presets.create` | `discord_target_presets.create` | DIRECT |
| P044 | super_admin / 該当Permissionを持つCustom Role | `discord_target_presets.update` | E088 PUT `/discord/target-presets/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 discord_target_presets の種別/ID | `panel.discord_target_presets.update` | `discord_target_presets.update` | DIRECT |
| P045 | super_admin / 該当Permissionを持つCustom Role | `discord_target_presets.delete` | E089 DELETE `/discord/target-presets/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 discord_target_presets の種別/ID | `panel.discord_target_presets.delete` | `discord_target_presets.delete` | DIRECT |
| P046 | super_admin / 該当Permissionを持つCustom Role | `video_cover_presets.read` | E215 GET `/video-cover-presets`<br>E217 GET `/video-cover-presets/{id}`<br>E218 GET `/video-cover-presets/{id}/media-assets/{variant_id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 video_cover_presets の種別/ID | `panel.video_cover_presets.read` | `video_cover_presets.read` | DIRECT |
| P047 | super_admin / 該当Permissionを持つCustom Role | `video_cover_presets.create` | E216 POST `/video-cover-presets` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 video_cover_presets の種別/ID | `panel.video_cover_presets.create` | `video_cover_presets.create` | DIRECT |
| P048 | super_admin / 該当Permissionを持つCustom Role | `video_cover_presets.update` | E219 PUT `/video-cover-presets/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 video_cover_presets の種別/ID | `panel.video_cover_presets.update` | `video_cover_presets.update` | DIRECT |
| P049 | super_admin / 該当Permissionを持つCustom Role | `video_cover_presets.delete` | E220 DELETE `/video-cover-presets/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 video_cover_presets の種別/ID | `panel.video_cover_presets.delete` | `video_cover_presets.delete` | DIRECT |
| P050 | super_admin / 該当Permissionを持つCustom Role | `youtube_outputs.read` | E090 GET `/youtube/outputs`<br>E092 GET `/youtube/outputs/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内 youtube_outputs の種別/ID | `panel.youtube_outputs.read` | `youtube_outputs.read` | DIRECT |
| P051 | super_admin / 該当Permissionを持つCustom Role | `youtube_outputs.create` | E091 POST `/youtube/outputs` | Output作成に付随するStream Key秘密保存も含む。 条件詳細は対応E行。 | Panel内 youtube_outputs の種別/ID | `panel.youtube_outputs.create` | `youtube_outputs.create` | DIRECT |
| P052 | super_admin / 該当Permissionを持つCustom Role | `youtube_outputs.update` | E093 PUT `/youtube/outputs/{id}` | Output更新に付随するStream Key変更/クリア、relay binding競合条件を含む。 条件詳細は対応E行。 | Panel内 youtube_outputs の種別/ID | `panel.youtube_outputs.update` | `youtube_outputs.update` | DIRECT |
| P053 | super_admin / 該当Permissionを持つCustom Role | `youtube_outputs.delete` | E094 DELETE `/youtube/outputs/{id}` | 参照/relay binding条件を確認。付随Stream Keyのクリアも含む。 条件詳細は対応E行。 | Panel内 youtube_outputs の種別/ID | `panel.youtube_outputs.delete` | `youtube_outputs.delete` | DIRECT |
| P054 | super_admin / 該当Permissionを持つCustom Role | `services.assign` | E180 POST `/services/{id}/assign` | worker型をこの汎用入口で割当する場合workers.assignも必要。primary/standbyと保存時条件を維持。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | `panel.services.assign` | `services.assign` | COMPOSITE |
| P055 | super_admin / 該当Permissionを持つCustom Role | `services.unassign` | E181 DELETE `/services/{id}/assignment` | worker型をこの汎用入口で解除する場合workers.unassignも必要。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | `panel.services.unassign` | `services.unassign` | COMPOSITE |
| P056 | super_admin / 該当Permissionを持つCustom Role | `services.disable` | E182 DELETE `/services/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | `panel.services.disable` | `services.disable` | DIRECT |
| P057 | super_admin / 該当Permissionを持つCustom Role | `workers.read` | E183 GET `/workers`<br>E184 GET `/workers/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | `panel.workers.read` | `workers.read` | DIRECT |
| P058 | super_admin / 該当Permissionを持つCustom Role | `workers.assign` | E185 POST `/workers/{id}/assign` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | `panel.workers.assign` | `workers.assign` | DIRECT |
| P059 | super_admin / 該当Permissionを持つCustom Role | `workers.unassign` | E186 DELETE `/workers/{id}/assignment` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | `panel.workers.unassign` | `workers.unassign` | DIRECT |
| P060 | super_admin / 該当Permissionを持つCustom Role | `workers.restart` | E187 POST `/workers/{id}/restart` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | `panel.workers.restart` | `workers.restart` | DIRECT |
| P061 | super_admin / 該当Permissionを持つCustom Role | `workers.disable` | HTTP受理経路なし（catalogのみ） | 登録のみ。実装済みHTTP/直接Permission判定は固定Source内に見つからない。権限情報を保存し、削除も実動作の新規付与もしない。 条件詳細は対応E行。 | Panel内Service/Node/Stream Assignment | 旧entitlementのまま保全・非活性 | 未確定 | REVIEW |
| P062 | super_admin / 該当Permissionを持つCustom Role | `archives.read` | E001 GET `/streams/{id}/artifacts`<br>E003 GET `/streams/{id}/artifacts/{artifact_id}/shares`<br>E198 GET `/archive/streams`<br>E199 GET `/archive/processing-streams` | 他人作成物に絞るcreator ACLはなくPanel内対象。 条件詳細は対応E行。 | Panel内Stream/Artifact/Share | `panel.archives.read_streams`<br>`panel.archives.read_processing`<br>`panel.archives.read_artifacts`<br>`panel.archives.read_shares` | `archives.read_streams`<br>`archives.read_processing`<br>`archives.read_artifacts`<br>`archives.read_shares` | SPLIT |
| P063 | super_admin / 該当Permissionを持つCustom Role | `archives.download` | E002 GET `/streams/{id}/artifacts/{artifact_id}/download`<br>E004 POST `/streams/{id}/artifacts/{artifact_id}/shares` | 共有リンク作成能力を含む。shareのallow_download/期限/失効は別条件。 条件詳細は対応E行。 | Panel内Stream/Artifact/Share | `panel.archives.download`<br>`panel.archives.create_share` | `archives.download`<br>`archives.create_share` | SPLIT |
| P064 | super_admin / 該当Permissionを持つCustom Role | `archives.delete` | E005 DELETE `/streams/{id}/artifacts/{artifact_id}/shares/{share_id}`<br>E006 DELETE `/streams/{id}/artifacts/{artifact_id}`<br>E007 PUT `/streams/{id}/artifacts/{artifact_id}` | PUT renameもarchives.deleteで許可。 条件詳細は対応E行。 | Panel内Stream/Artifact/Share | `panel.archives.delete_artifact`<br>`panel.archives.rename_artifact`<br>`panel.archives.revoke_share` | `archives.delete_artifact`<br>`archives.rename_artifact`<br>`archives.revoke_share` | SPLIT |
| P065 | super_admin / 該当Permissionを持つCustom Role | `archives.retry_upload` | HTTP受理経路なし（catalogのみ） | 登録のみ。実装済みHTTP/直接Permission判定は固定Source内に見つからない。権限情報を保存し、削除も実動作の新規付与もしない。 条件詳細は対応E行。 | Panel内Stream/Artifact/Share | 旧entitlementのまま保全・非活性 | 未確定 | REVIEW |
| P066 | super_admin / 該当Permissionを持つCustom Role | `logs.read` | E241 GET `/streams/{id}/logs`<br>E242 GET `/stream-logs` | Stream別ログと全Streamログ履歴。 条件詳細は対応E行。 | 同CPの該当履歴 | `panel.logs.read_stream`<br>`panel.logs.read_history` | `logs.read_stream`<br>`logs.read_history` | SPLIT |
| P067 | super_admin / 該当Permissionを持つCustom Role | `logs.export` | HTTP受理経路なし（catalogのみ） | 登録のみ。実装済みHTTP/直接Permission判定は固定Source内に見つからない。権限情報を保存し、削除も実動作の新規付与もしない。 条件詳細は対応E行。 | 同CPの該当履歴 | 旧entitlementのまま保全・非活性 | 未確定 | REVIEW |
| P068 | super_admin / 該当Permissionを持つCustom Role | `audit_logs.read` | E188 GET `/audit-logs` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 同CPの該当履歴 | `panel.audit_logs.read` | `audit_logs.read` | DIRECT |
| P069 | super_admin / 該当Permissionを持つCustom Role | `audit_logs.export` | E189 GET `/audit-logs/export` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 同CPの該当履歴 | `panel.audit_logs.export` | `audit_logs.export` | DIRECT |
| P070 | super_admin / 該当Permissionを持つCustom Role | `secrets.read_status` | E165 GET `/secrets/status` | 秘密本文readでなくStatus/Fingerprint。Policy投影にもOR条件で利用。 条件詳細は対応E行。 | 許可Secret Name / Status投影 | `panel.secrets.read_status` | `secrets.read_status` | DIRECT |
| P071 | super_admin / 該当Permissionを持つCustom Role | `secrets.create` | HTTP受理経路なし（catalogのみ） | 登録のみ。実装済みHTTP/直接Permission判定は固定Source内に見つからない。権限情報を保存し、削除も実動作の新規付与もしない。 条件詳細は対応E行。 | 許可Secret Name / Status投影 | 旧entitlementのまま保全・非活性 | 未確定 | REVIEW |
| P072 | super_admin / 該当Permissionを持つCustom Role | `secrets.update` | E166 PUT `/secrets/{name}` | 許可済みSecret名へ非空値は暗号化upsert、空値は削除。secrets.create/deleteの別Permissionを要求しない。 条件詳細は対応E行。 | 許可Secret Name / Status投影 | `panel.secrets.set_known`<br>`panel.secrets.clear_known` | `secrets.set_known`<br>`secrets.clear_known` | SPLIT |
| P073 | super_admin / 該当Permissionを持つCustom Role | `secrets.delete` | HTTP受理経路なし（catalogのみ） | 登録のみ。実装済みHTTP/直接Permission判定は固定Source内に見つからない。権限情報を保存し、削除も実動作の新規付与もしない。 条件詳細は対応E行。 | 許可Secret Name / Status投影 | 旧entitlementのまま保全・非活性 | 未確定 | REVIEW |
| P074 | super_admin / 該当Permissionを持つCustom Role | `api_tokens.read` | E167 GET `/api-tokens` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | Panel内Token/Node＋発行Scope制約 | `panel.api_tokens.read` | `api_tokens.read` | DIRECT |
| P075 | super_admin / 該当Permissionを持つCustom Role | `api_tokens.create` | E168 POST `/api-tokens`<br>E169 POST `/api-tokens/{id}/rotate`<br>E171 GET `/nodes`<br>E172 GET `/nodes/action-permissions`<br>E173 POST `/nodes/registration-tokens`<br>E174 PUT `/nodes/{id}`<br>E175 GET `/nodes/{id}/configuration`<br>E176 POST `/nodes/{id}/configure-token`<br>E177 POST `/nodes/{id}/rotate-token` | rotationにはrevokeも必要。発行予定scopeごとのHuman Permission、秘密設定、active work、pull_v2制限を保持。 条件詳細は対応E行。 | Panel内Token/Node＋発行Scope制約 | `panel.tokens.issue`<br>`panel.tokens.rotate`<br>`panel.nodes.list`<br>`panel.nodes.project_actions`<br>`panel.nodes.issue_registration_token`<br>`panel.nodes.update`<br>`panel.nodes.read_configuration`<br>`panel.nodes.regenerate_configure_token`<br>`panel.nodes.rotate_runtime_token` | `tokens.issue`<br>`tokens.rotate`<br>`nodes.list`<br>`nodes.project_actions`<br>`nodes.issue_registration_token`<br>`nodes.update`<br>`nodes.read_configuration`<br>`nodes.regenerate_configure_token`<br>`nodes.rotate_runtime_token` | SPLIT |
| P076 | super_admin / 該当Permissionを持つCustom Role | `api_tokens.revoke` | E170 DELETE `/api-tokens/{id}` | 単独DELETEによる失効と、create系の再発行/rotationにおける追加条件。 条件詳細は対応E行。 | Panel内Token/Node＋発行Scope制約 | `panel.api_tokens.revoke` | `api_tokens.revoke` | DIRECT |
| P077 | super_admin / 該当Permissionを持つCustom Role | `system_settings.read` | E190 GET `/security/settings` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 同CPの全体設定 | `panel.system_settings.read` | `system_settings.read` | DIRECT |
| P078 | super_admin / 該当Permissionを持つCustom Role | `system_settings.update` | E191 PUT `/security/settings`<br>E193 GET `/settings/app/manage`<br>E194 PUT `/settings/app`<br>E195 POST `/settings/app/test-email` | SMTP/Turnstile等の秘密設定もこの権限の内部能力。secrets.updateを後付けANDにして縮小しない。 条件詳細は対応E行。 | 同CPの全体設定 | `panel.settings.update_security`<br>`panel.settings.read_managed_app`<br>`panel.settings.update_app`<br>`panel.settings.test_email` | `settings.update_security`<br>`settings.read_managed_app`<br>`settings.update_app`<br>`settings.test_email` | SPLIT |
| P079 | super_admin / 該当Permissionを持つCustom Role | `system_updates.read` | E136 GET `/system-updates`<br>E139 GET `/system-updates/host-self-updates`<br>E143 GET `/system-updates/updaters/{id}/settings`<br>E147 GET `/system-updates/updaters/{id}/runtime-token-rotations/active`<br>E151 GET `/system-updates/updaters/{id}/bootstrap-jobs` | Policy内release-token fingerprintの表示はsecrets.read_status OR secrets.updateで追加投影。 条件詳細は対応E行。 | 登録Updater/Host/Target/Job/Policy | `panel.updates.read_jobs`<br>`panel.updates.read_self_jobs`<br>`panel.updates.read_policy`<br>`panel.updates.read_active_rotation`<br>`panel.updates.read_bootstrap_jobs` | `updates.read_jobs`<br>`updates.read_self_jobs`<br>`updates.read_policy`<br>`updates.read_active_rotation`<br>`updates.read_bootstrap_jobs` | SPLIT |
| P080 | super_admin / 該当Permissionを持つCustom Role | `system_updates.execute` | E137 POST `/system-updates`<br>E138 POST `/system-updates/{id}/cancel`<br>E140 POST `/system-updates/hosts/{host_id}/self-updates`<br>E141 POST `/system-updates/host-self-updates/{id}/retry`<br>E142 POST `/system-updates/host-self-updates/{id}/cancel`<br>E144 PUT `/system-updates/updaters/{id}/settings`<br>E145 POST `/system-updates/updaters/{id}/pull-ownership/activate`<br>E146 POST `/system-updates/updaters/{id}/pull-ownership/deactivate`<br>E148 POST `/system-updates/updaters/{id}/runtime-token-rotations`<br>E149 POST `/system-updates/updaters/{id}/runtime-token-rotations/{rotation_id}/cancel`<br>E150 POST `/system-updates/updaters/{id}/runtime-token-rotations/{rotation_id}/emergency-revoke`<br>E152 POST `/system-updates/updaters/{id}/bootstrap-jobs` | 12 execute Routeに含まれるsoftware/port mode分岐を14種Human操作へ展開。systemd/Docker、Port snapshot/endpoint revision/desired revision/fence/冪等制約。GitHubToken変更はsecrets.update追加。rotationはcreate/revoke/secrets.updateのAND、Host Bootstrap作成はsecrets.update。Service Grant issue/consumeを含めない。 条件詳細は対応E行。 | 登録Updater/Host/Target/Job/Policy | `panel.updates.create`<br>`panel.updates.reconfigure_port_local`<br>`panel.updates.reconfigure_port_local_and_advertised`<br>`panel.updates.cancel`<br>`panel.host_self_updates.create`<br>`panel.host_self_updates.retry`<br>`panel.host_self_updates.cancel`<br>`panel.updater_policy.update`<br>`panel.updater_pull.activate`<br>`panel.updater_pull.deactivate`<br>`panel.runtime_rotation.stage`<br>`panel.runtime_rotation.cancel`<br>`panel.runtime_rotation.emergency_revoke`<br>`panel.host_bootstrap.create` | `updates.create`<br>`updates.reconfigure_port_local`<br>`updates.reconfigure_port_local_and_advertised`<br>`updates.cancel`<br>`host_self_updates.create`<br>`host_self_updates.retry`<br>`host_self_updates.cancel`<br>`updater_policy.update`<br>`updater_pull.activate`<br>`updater_pull.deactivate`<br>`runtime_rotation.stage`<br>`runtime_rotation.cancel`<br>`runtime_rotation.emergency_revoke`<br>`host_bootstrap.create` | SPLIT |
| P081 | super_admin / 該当Permissionを持つCustom Role | `incidents.read` | E064 GET `/observability/incidents` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.incidents.read` | `incidents.read` | DIRECT |
| P082 | super_admin / 該当Permissionを持つCustom Role | `incidents.acknowledge` | E071 POST `/observability/incidents/{id}/acknowledge` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.incidents.acknowledge` | `incidents.acknowledge` | DIRECT |
| P083 | super_admin / 該当Permissionを持つCustom Role | `incidents.resolve` | E072 POST `/observability/incidents/{id}/resolve` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.incidents.resolve` | `incidents.resolve` | DIRECT |
| P084 | super_admin / 該当Permissionを持つCustom Role | `diagnostics.read` | E065 GET `/observability/diagnostics` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.diagnostics.read` | `diagnostics.read` | DIRECT |
| P085 | super_admin / 該当Permissionを持つCustom Role | `diagnostics.run` | E070 POST `/observability/incidents/{id}/diagnostics/rerun` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.diagnostics.run` | `diagnostics.run` | DIRECT |
| P086 | super_admin / 該当Permissionを持つCustom Role | `remediation.read` | E067 GET `/observability/remediation-actions` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.remediation.read` | `remediation.read` | DIRECT |
| P087 | super_admin / 該当Permissionを持つCustom Role | `remediation.approve` | E068 POST `/observability/remediation-actions/{id}/approve` | ObservabilityへapproveするHuman操作。machine remediation.execute tokenとは別。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.remediation.approve` | `remediation.approve` | DIRECT |
| P088 | super_admin / 該当Permissionを持つCustom Role | `remediation.execute` | E069 POST `/observability/remediation-actions/{id}/execute` | ObservabilityへexecuteするHuman操作。対象ID/Contextと外部Service条件は保持。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.remediation.execute` | `remediation.execute` | DIRECT |
| P089 | super_admin / 該当Permissionを持つCustom Role | `notification_channels.read` | E073 GET `/observability/notification-deliveries`<br>E074 GET `/observability/notification-channels`<br>E076 GET `/observability/notification-channels/{id}` | Observabilityへのproxy。CP外の内部判定を推測しない。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.notification_channels.read`<br>`panel.notification_deliveries.read` | `notification_channels.read`<br>`notification_deliveries.read` | SPLIT |
| P090 | super_admin / 該当Permissionを持つCustom Role | `notification_channels.create` | E075 POST `/observability/notification-channels` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.notification_channels.create` | `notification_channels.create` | DIRECT |
| P091 | super_admin / 該当Permissionを持つCustom Role | `notification_channels.update` | E077 PUT `/observability/notification-channels/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.notification_channels.update` | `notification_channels.update` | DIRECT |
| P092 | super_admin / 該当Permissionを持つCustom Role | `notification_channels.delete` | E078 DELETE `/observability/notification-channels/{id}` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.notification_channels.delete` | `notification_channels.delete` | DIRECT |
| P093 | super_admin / 該当Permissionを持つCustom Role | `notification_channels.test` | E079 POST `/observability/notification-channels/{id}/test` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.notification_channels.test` | `notification_channels.test` | DIRECT |
| P094 | super_admin / 該当Permissionを持つCustom Role | `integrations.read` | E047 GET `/integrations/oauth-providers`<br>E049 GET `/integrations/oauth-providers/{id}`<br>E052 GET `/integrations/oauth-accounts`<br>E056 GET `/integrations/oauth-accounts/{id}`<br>E059 GET `/archive/destinations`<br>E061 GET `/archive/destinations/{id}` | OAuth defaultRole/autoProvision変更はroles.assignとRole割当検証。connect callbackはstate-cookie/provider/purpose/targetと現在権限を再確認。 条件詳細は対応E行。 | Panel内Provider/Account/Destination | `panel.oauth_providers.read`<br>`panel.oauth_accounts.read`<br>`panel.drive_destinations.read` | `oauth_providers.read`<br>`oauth_accounts.read`<br>`drive_destinations.read` | SPLIT |
| P095 | super_admin / 該当Permissionを持つCustom Role | `integrations.create` | E048 POST `/integrations/oauth-providers`<br>E053 POST `/integrations/oauth-accounts/start`<br>E054 GET `/integrations/oauth-accounts/callback`<br>E055 POST `/integrations/oauth-accounts/callback`<br>E060 POST `/archive/destinations` | OAuth defaultRole/autoProvision変更はroles.assignとRole割当検証。connect callbackはstate-cookie/provider/purpose/targetと現在権限を再確認。 条件詳細は対応E行。 | Panel内Provider/Account/Destination | `panel.oauth_providers.create`<br>`panel.oauth_accounts.connect`<br>`panel.drive_destinations.create` | `oauth_providers.create`<br>`oauth_accounts.connect`<br>`drive_destinations.create` | SPLIT |
| P096 | super_admin / 該当Permissionを持つCustom Role | `integrations.update` | E050 PUT `/integrations/oauth-providers/{id}`<br>E053 POST `/integrations/oauth-accounts/start`<br>E054 GET `/integrations/oauth-accounts/callback`<br>E055 POST `/integrations/oauth-accounts/callback`<br>E057 PUT `/integrations/oauth-accounts/{id}`<br>E062 PUT `/archive/destinations/{id}` | OAuth defaultRole/autoProvision変更はroles.assignとRole割当検証。connect callbackはstate-cookie/provider/purpose/targetと現在権限を再確認。 条件詳細は対応E行。 | Panel内Provider/Account/Destination | `panel.oauth_providers.update`<br>`panel.oauth_accounts.update`<br>`panel.oauth_accounts.reconnect`<br>`panel.drive_destinations.update` | `oauth_providers.update`<br>`oauth_accounts.update`<br>`oauth_accounts.reconnect`<br>`drive_destinations.update` | SPLIT |
| P097 | super_admin / 該当Permissionを持つCustom Role | `integrations.delete` | E051 DELETE `/integrations/oauth-providers/{id}`<br>E058 DELETE `/integrations/oauth-accounts/{id}`<br>E063 DELETE `/archive/destinations/{id}` | OAuth defaultRole/autoProvision変更はroles.assignとRole割当検証。connect callbackはstate-cookie/provider/purpose/targetと現在権限を再確認。 条件詳細は対応E行。 | Panel内Provider/Account/Destination | `panel.oauth_providers.delete`<br>`panel.oauth_accounts.delete`<br>`panel.drive_destinations.delete` | `oauth_providers.delete`<br>`oauth_accounts.delete`<br>`drive_destinations.delete` | SPLIT |
| P098 | super_admin / 該当Permissionを持つCustom Role | `metrics.read` | E066 GET `/observability/metrics` | 現行対象・状態・認証条件を保つ同義操作候補。 条件詳細は対応E行。 | 選択Observability内の該当対象 | `panel.metrics.read` | `metrics.read` | DIRECT |
| P099 | super_admin / 該当Permissionを持つCustom Role | `service_health.read` | E175 GET `/nodes/{id}/configuration`<br>E178 GET `/service-health`<br>E179 GET `/service-health/{id}/runtime-config` | 一覧/Runtime config/Node configurationの読取。Node configurationはapi_tokens.createとのOR。 条件詳細は対応E行。 | Panel内Service/Node | `panel.service_health.read` | `service_health.read` | DIRECT |

## 5. 分割しても既存能力を欠かさない規則

**設計案**：旧Permissionの展開は、その旧Permissionが許可していた全操作の和集合とする。機械的に「一つだけ最小権限を選ぶ」移行にしない。たとえば`system_updates.execute`は本表の14種類（12 execute Route内のPort mode分岐を含む）、`streams.stop`は通常停止・強制停止・YouTube終了・relay-static復旧、`archives.delete`は削除・rename・共有失効の合計を保持する。

一方で分割された各操作は**元の追加条件を持つ**。`users.create`だけから`roles.assign`を新たに付与せず、RoleなしUser作成を引き続き許し、Role付き作成は元のANDを要求する。`streams.create OR streams.update`のMedia APIをANDに変更しない。`api_tokens.create`からrotation能力を展開しても、元の`api_tokens.revoke`等がないactorにrotationを許さない。Profile/Discord/YouTube/App設定内の既存秘密書込能力へ、これまで不要だった`secrets.update`を追加して縮小しない。

分割Capabilityを組み合わせた際にも、元の条件を取り除いた無制限操作を作らない。個々のE行の認証、対象、状態、追加Permissionを候補Capabilityへ結び付ける。

## 6. super_adminの能力維持

| 維持対象 | 移行ルール（設計） | 境界 |
| --- | --- | --- |
| 初期99 Permissionと現在実効集合 | 実際のRole/Permission割当を取得できる次工程で確定。99件一括付与を現在状態の代用にしない | Source上の初期既定値と実DB差分を分ける |
| Panel全体の業務管理 | 同CP全体の既存業務対象を保持。既存にないHost/Realm/creator限定へ縮小しない | 別CP/別組織まで広げない |
| 他super_adminへの管理 | 対象super_adminのstatus/password/Role割当の特別条件を明示的に維持 | 99 Permissionを持つ非super_adminに同じ例外を自動付与しない |
| Custom Role管理 | Permission部分集合制約を守った作成・変更・削除を維持 | super_admin Role自体の更新/削除は禁止のまま |
| 自分のUser Role変更 | 元通り拒否 | super_adminだから許すという新例外を加えない |
| Node/Host/Update管理 | 各旧Permissionの全Human操作を保持。API名のBootstrap/Recoveryも通常Host管理として区別 | Host実行Token/Grantを人間の権利として継承しない |
| 既存後発対象への範囲 | v2が同CPの全対象を扱えた権限は移行時に存在したIDだけへ固定しない | 将来の新種Capabilityへの万能wildcardを新設しない |

Sourceの根拠：[validateRoleAssignments](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/93181192d8f036bae40b490957489c9ee8290769/internal/httpapi/server_users_roles.go#L221)、[validateUserRoleUpdate](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/93181192d8f036bae40b490957489c9ee8290769/internal/httpapi/server_users_roles.go#L189)、[setUserStatus](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/93181192d8f036bae40b490957489c9ee8290769/internal/httpapi/server_users_roles.go#L299)、[resetPassword](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/93181192d8f036bae40b490957489c9ee8290769/internal/httpapi/server_users_roles.go#L376)、[updateRole](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/93181192d8f036bae40b490957489c9ee8290769/internal/httpapi/server_users_roles.go#L478)、[deleteRole](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/93181192d8f036bae40b490957489c9ee8290769/internal/httpapi/server_users_roles.go#L529)。

super_adminの業務上の特別扱いを新モデルで表す方法（Role由来属性又は操作ごとの明示権限）は未決定。名称だけの自己申告ではなく、移行時の検証済みRole割当を起点とし、現在状態の変更で失効する必要がある。これをv3 Trust Rootや独立承認者の資格に流用しない。

## 7. Human権限として移行しないもの

以下のEXCLUDEDは99 Permissionとは別母集団。過去履歴・識別子・bytesは保全するが、v3の現在実行資格として再利用しない。

| v2情報 | 判定 | v3での扱い候補 |
| --- | --- | --- |
| Service Token Scope / Bearer Token | EXCLUDED | サービス主体を独立識別。Human Capabilityへ直接変換しない。再登録・再承認の別工程 |
| Node Registration / Configure / Activation Token | EXCLUDED | 発行可能というHuman権限と、発行物の実行資格を分離 |
| Runtime Token Rotation旧/後継Credential | EXCLUDED | Rotation状態・監査は保全。旧credentialを新Authorityへ昇格しない |
| System Update Lease / Generation / Fence | EXCLUDED | 旧Leaseは再利用しない。v3の現行実行条件を新規取得 |
| Mutation Grant発行/Consume記録 | EXCLUDED | HumanのUpdate作成権からIssue/Consume資格を派生させない |
| Host Self Update Grant | EXCLUDED | job/Host/ownership/nonce/expiry/consume制約の別実行資格 |
| Host/Pull/Execution Host Ownership epoch | EXCLUDED | 対象の履歴/登録事実として保持。現行v3FenceやHost所有権を自己認定しない |
| Runtime Secret Lease | EXCLUDED | 必要秘密への短期サービス取得条件。Humanの秘密Status閲覧とは別 |
| Stream primary/standby Assignment | EXCLUDED（資格の直接継承） | 業務構成データとして保全。Machineのprimary判定とHumanの編集権を分離 |
| 公開Archive Share / Preview Token | EXCLUDED（Human Capability化） | 公開linkの互換性と失効条件は別計画で扱い、Human管理権限へ変換しない |

新たなBootstrap承認、Trust Root変更、独立台帳管理、Recovery quorum、署名Root/鍵管理、独立承認者名簿変更は、既存Panel管理者から**自動付与しない**。これは既存の通常Host Bootstrap jobやYouTube業務復旧能力を削除する指示ではない。

## 8. 具体的な移行規則

1. **User/Principal**：同じCP内の既存User IDを安定した対応キーとする。username/emailで再識別・名寄せしない。複数CPがある場合のnamespace対応を明示して承認する。OAuthは既存Providerとsubjectの対応を保全し、メール一致だけで別主体を合併しない。
2. **Account状態**：active/disabled/locked/pending_password_changeを保持する。inactiveをactiveに戻さず、pendingを通常操作可能へ自動変換しない。認証Credential/MFA/Sessionの移行は別途互換確認が必要で、本資料では取得しない。
3. **Role**：Role ID、名称、User割当、Permission集合を論理的に保持する。組込みsuper_adminと任意名Custom Roleを区別し、admin/operator/member/viewer等を存在する組込みRoleだと仮定しない。
4. **Permission展開**：本表のDIRECT/SPLIT候補を展開。COMPOSITEの別Permissionは自動付与せず元のAND/OR式を保存する。REVIEW5件は旧権限行として保全し、新しい操作の有効化を保留する。
5. **複数Role**：実効集合は全Role Permissionの和集合。重複は除去、deny優先や最小Role一つの選択へ変更しない。Role数やSubjects上限への切捨ては禁止。
6. **Scope**：Human側の同CPグローバル範囲と、本人Media等の既存所有条件を別々に記録。v3 exact registry Scopeへ無理に射影せず、新Human Scope仕様が決まるまで適用しない。
7. **Custom Role**：名前から能力を推測せず各Permissionを展開。未知DBキーや`*`が後日検出されたらUNKNOWNとして個別レビューし、無視・自動拡張しない。通常APIはDefaultPermissions以外を受け付けない。
8. **旧Grant/Token/Lease**：前節の通り履歴をPRESERVE。現在実行資格の移植/復活/再Consumeはしない。
9. **監査**：旧監査行を変更せず保全。新しい移行イベントにSource commit、入力snapshot識別子、Mapping版、対象Principal、前後の論理権限集合と差分Codeを別記録する。Credentialや完全秘密payloadを収集・記録しない。
10. **実効比較**：Actor×Operation×対象×状態ごとに旧式と新式を評価し、許可集合の欠落/増加を比較。単なるPermissionキー数比較では不足。super_admin専用分岐とSession-only操作も含む。
11. **差分**：減少/増加とも自動承認しない。原因をScope欠落、分割漏れ、AND/OR誤り、Role特例、既存安全条件、新機能に分類。既存能力維持を修正するか、別途明示承認された仕様差分として管理する。
12. **失敗と復旧**：段階的・原子的な移行単位、入力snapshot/Mapping版へのCAS、適用記録の冪等性を実装前に定める。commit不明はUNKNOWNで書込停止し、勝手に再付与しない。Rollbackは旧DBへの巻戻しや旧実行Token再活性化で済ませず、履歴を保持した補償/再検証計画を作る。正式v2復帰は別認定。

## 9. 次工程の判断事項

- 新Human Capability catalogとScope表現、super_admin由来特例の表現方法。現在のregistry専用契約を変更するか、別のPanel業務契約を置くか。
- 本表の**24 SPLIT**を含む94件の候補名/操作粒度。候補名はまだ公開APIではない。
- REVIEW5件の保全形式。旧キーを保持し、現在使われる別キー（例：streams.retry_upload、secrets.update）と混同しない。
- 次工程で承認された実データsnapshot/移行検証の取得方法。今回は実Role名、人数、実割当、稼働設定、Migration適用履歴を照会していない。
- 最後の管理者保護の競合/状態範囲、既存Role変更の非原子的な認可・保存間隔への対策。既存Sourceにない保証を移行済みとは扱わない。
- v3管理データへの権限委譲とTrust Root/Bootstrap/Recoveryの独立承認をどこで分離するか。S11信頼起点の確定を本資料で代行しない。

実Evidence発行無効／全量PRESERVE／旧Execution Authority非再利用／自動開始保留維持／UNKNOWN・HELD非成功化／製品全体受入NOT_RUN／Phase 1終了保留／本番移行未許可／正式v2復帰NOT_CERTIFIED。

## S13 全Permission → Operation 実装対応

| v2 Permission | Route ID | Handler追加条件の根拠 | Scope | v3 Operation |
|---|---|---|---|---|
| users.read | E244, E246, E254 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | users.read |
| users.create | E245 | createUser | Global＋既存対象制約 | users.create |
| users.update | E247, E250 | updateUser | Global＋既存対象制約 | users.update |
| users.disable | E248, E249 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | users.disable |
| users.delete | E253 | deleteUser | Global＋既存対象制約 | users.delete |
| users.reset_password | E251, E252 | resetPassword | Global＋既存対象制約 | users.reset_password |
| users.manage_mfa | E255, E256 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | users.manage_mfa |
| roles.read | E243, E257, E259 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | roles.read |
| roles.create | E258 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | roles.create |
| roles.update | E260 | updateRole | Global＋既存対象制約 | roles.update |
| roles.delete | E261 | deleteRole | Global＋既存対象制約 | roles.delete |
| roles.assign | 入口なし／追加判定または未使用：元表参照 | validateOAuthProviderRolePolicy, createUser, updateUser | Global＋既存対象制約 | roles.assign |
| streams.read | E197, E208, E209, E210, E211, E213, E222, E227, E235, E236, E237, E238, E239 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | streams.read |
| streams.create | E200, E201, E202, E203, E204, E205, E206 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | streams.create |
| streams.update | E201, E202, E203, E204, E205, E206, E212, E223, E224, E233, E240 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | streams.update |
| streams.start | E225, E226, E228 | validateServiceTokenScopePermissions | Global＋既存対象制約 | streams.start |
| streams.stop | E229, E230, E231, E232 | validateServiceTokenScopePermissions | Global＋既存対象制約 | streams.stop |
| streams.retry_upload | E234 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | streams.retry_upload |
| streams.delete | E221 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | streams.delete |
| streams.show_cover | 入口なし／追加判定または未使用：元表参照 | updateVideoCoverState | Global＋既存対象制約 | streams.show_cover |
| streams.hide_cover | 入口なし／追加判定または未使用：元表参照 | updateVideoCoverState | Global＋既存対象制約 | streams.hide_cover |
| encoder_profiles.read | E095, E103 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | encoder_profiles.read |
| encoder_profiles.create | E099 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | encoder_profiles.create |
| encoder_profiles.update | E107 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | encoder_profiles.update |
| encoder_profiles.delete | E111 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | encoder_profiles.delete |
| archive_profiles.read | E096, E104 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archive_profiles.read |
| archive_profiles.create | E100 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archive_profiles.create |
| archive_profiles.update | E108 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archive_profiles.update |
| archive_profiles.delete | E112 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archive_profiles.delete |
| caption_profiles.read | E097, E105 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | caption_profiles.read |
| caption_profiles.create | E101 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | caption_profiles.create |
| caption_profiles.update | E109 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | caption_profiles.update |
| caption_profiles.delete | E113 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | caption_profiles.delete |
| overlay_profiles.read | E098, E106 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | overlay_profiles.read |
| overlay_profiles.create | E102 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | overlay_profiles.create |
| overlay_profiles.update | E110 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | overlay_profiles.update |
| overlay_profiles.delete | E114 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | overlay_profiles.delete |
| discord_configs.read | E080, E082 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_configs.read |
| discord_configs.create | E081 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_configs.create |
| discord_configs.update | E083 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_configs.update |
| discord_configs.delete | E084 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_configs.delete |
| discord_target_presets.read | E085, E087 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_target_presets.read |
| discord_target_presets.create | E086 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_target_presets.create |
| discord_target_presets.update | E088 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_target_presets.update |
| discord_target_presets.delete | E089 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | discord_target_presets.delete |
| video_cover_presets.read | E215, E217, E218 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | video_cover_presets.read |
| video_cover_presets.create | E216 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | video_cover_presets.create |
| video_cover_presets.update | E219 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | video_cover_presets.update |
| video_cover_presets.delete | E220 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | video_cover_presets.delete |
| youtube_outputs.read | E090, E092 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | youtube_outputs.read |
| youtube_outputs.create | E091 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | youtube_outputs.create |
| youtube_outputs.update | E093 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | youtube_outputs.update |
| youtube_outputs.delete | E094 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | youtube_outputs.delete |
| services.assign | E180 | assignService, prepareStreamDiscordAssignment | Global＋既存対象制約 | services.assign |
| services.unassign | E181 | unassignService | Global＋既存対象制約 | services.unassign |
| services.disable | E182 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | services.disable |
| workers.read | E183, E184 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | workers.read |
| workers.assign | E185 | assignService | Global＋既存対象制約 | workers.assign |
| workers.unassign | E186 | unassignService | Global＋既存対象制約 | workers.unassign |
| workers.restart | E187 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | workers.restart |
| workers.disable | 入口なし／追加判定または未使用：元表参照 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | workers.disable |
| archives.read | E001, E003, E198, E199 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archives.read |
| archives.download | E002, E004 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archives.download |
| archives.delete | E005, E006, E007 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archives.delete |
| archives.retry_upload | 入口なし／追加判定または未使用：元表参照 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | archives.retry_upload |
| logs.read | E241, E242 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | logs.read |
| logs.export | 入口なし／追加判定または未使用：元表参照 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | logs.export |
| audit_logs.read | E188 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | audit_logs.read |
| audit_logs.export | E189 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | audit_logs.export |
| secrets.read_status | E165 | canViewUpdaterReleaseTokenFingerprint | Global＋既存対象制約 | secrets.read_status |
| secrets.create | 入口なし／追加判定または未使用：元表参照 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | secrets.create |
| secrets.update | E166 | runtimeTokenRotationAdminAllowed, validateServiceTokenScopePermissions, validateNodeConfigurationSecretPermissions, createUpdateHostBootstrapJob, updateUpdaterPolicy, canViewUpdaterReleaseTokenFingerprint | Global＋既存対象制約 | secrets.update |
| secrets.delete | 入口なし／追加判定または未使用：元表参照 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | secrets.delete |
| api_tokens.read | E167 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | api_tokens.read |
| api_tokens.create | E168, E169, E171, E172, E173, E174, E175, E176, E177 | runtimeTokenRotationAdminAllowed | Global＋既存対象制約 | api_tokens.create |
| api_tokens.revoke | E170 | runtimeTokenRotationAdminAllowed, regenerateNodeConfigureToken, rotateNodeRuntimeToken, rotateServiceToken | Global＋既存対象制約 | api_tokens.revoke |
| system_settings.read | E190 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | system_settings.read |
| system_settings.update | E191, E193, E194, E195 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | system_settings.update |
| system_updates.read | E136, E139, E143, E147, E151 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | system_updates.read |
| system_updates.execute | E137, E138, E140, E141, E142, E144, E145, E146, E148, E149, E150, E152 | validateServiceTokenScopePermissions | Global＋既存対象制約 | system_updates.execute |
| incidents.read | E064 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | incidents.read |
| incidents.acknowledge | E071 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | incidents.acknowledge |
| incidents.resolve | E072 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | incidents.resolve |
| diagnostics.read | E065 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | diagnostics.read |
| diagnostics.run | E070 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | diagnostics.run |
| remediation.read | E067 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | remediation.read |
| remediation.approve | E068 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | remediation.approve |
| remediation.execute | E069 | validateServiceTokenScopePermissions | Global＋既存対象制約 | remediation.execute |
| notification_channels.read | E073, E074, E076 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | notification_channels.read |
| notification_channels.create | E075 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | notification_channels.create |
| notification_channels.update | E077 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | notification_channels.update |
| notification_channels.delete | E078 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | notification_channels.delete |
| notification_channels.test | E079 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | notification_channels.test |
| integrations.read | E047, E049, E052, E056, E059, E061 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | integrations.read |
| integrations.create | E048, E053, E054, E055, E060 | startOAuthAccountConnection, finishOAuthAccountConnection | Global＋既存対象制約 | integrations.create |
| integrations.update | E050, E053, E054, E055, E057, E062 | startOAuthAccountConnection, finishOAuthAccountConnection | Global＋既存対象制約 | integrations.update |
| integrations.delete | E051, E058, E063 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | integrations.delete |
| metrics.read | E066 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | metrics.read |
| service_health.read | E175, E178, E179 | H0＋対応するE行のHandler/Store条件 | Global＋既存対象制約 | service_health.read |
