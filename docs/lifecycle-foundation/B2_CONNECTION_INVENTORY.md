# S12-B2-B Identity connection inventory

基準: `60137bb7f6bbcf830485ead3f5850809b767c5a4`。B2-Aの87調査IDを保持する。IDは独立SQL Writer数ではない。以下は実装経路と試験対応であり、本番L2資格を意味しない。

公開HTTPは明示Identity owner構成時、`GET /auth/me`と`POST /auth/logout`を除くIdentity/Policy/Secret/Setup/OAuth管理入口をpre-muxで拒否する。Storeの同一Tx接続があってもHTTP全体を資格化していない。未構成/offは既存の正常契約を保持する。

| ID | Sourceの関数/効果 | 状態 | 理由・制限 | 合成試験対応 |
| --- | --- | --- | --- | --- |
| U01 | CreateUser | 接続構造のみ | private participantを使うStore接続。該当する全HTTP条件・全効果の網羅試験とは扱わない。 | 専用動作PASSの主張なし |
| U02 | CreateOAuthUser | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| U03 | UpdateUser username branch | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| U04 | UpdateUser email branch | 接続構造のみ | private participantを使うStore接続。該当する全HTTP条件・全効果の網羅試験とは扱わない。 | 専用動作PASSの主張なし |
| U05 | UpdateUser RoleIDs replacement | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| U06 | SetUserStatus / Disable | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| U07 | SetUserStatus / Lock | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| U08 | SetUserStatus / Unlock/Enable | 接続構造のみ | private participantを使うStore接続。該当する全HTTP条件・全効果の網羅試験とは扱わない。 | 専用動作PASSの主張なし |
| U09 | SetUserStatus / Force Password Change | 接続構造のみ | private participantを使うStore接続。該当する全HTTP条件・全効果の網羅試験とは扱わない。 | 専用動作PASSの主張なし |
| U10 | DeleteUser | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| U11 | RecordLoginSuccess | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| U12 | recordLoginFailureGuarded | 接続構造のみ | private participantを使うStore接続。該当する全HTTP条件・全効果の網羅試験とは扱わない。 | 専用動作PASSの主張なし |
| C01 | ResetPassword | 接続構造のみ | private participantを使うStore接続。該当する全HTTP条件・全効果の網羅試験とは扱わない。 | 専用動作PASSの主張なし |
| C02 | ChangePassword | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| S01 | CreateSession | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| S02 | RefreshSession / refreshSessionAt | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| S03 | GetSession expiry branch | 接続構造のみ | SessionのL2 getterは期限切れを拒否し、物理削除をしない純Read。暗黙cleanup参加は未接続。他factor getterはclosed。 | 専用動作PASSの主張なし |
| S04 | DeleteSession | 接続構造のみ | private participantを使うStore接続。該当する全HTTP条件・全効果の網羅試験とは扱わない。 | 専用動作PASSの主張なし |
| S05 | DeleteUserSessions | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| M01 | StartTOTPEnrollment | 明示L2要求時は拒否 | Recovery以外のfactor-stageはproof/current-state参加を未資格として閉じる。 | 専用動作PASSの主張なし |
| M02 | ConfirmTOTPEnrollment | 明示L2要求時は拒否 | Recovery以外のfactor-stageはproof/current-state参加を未資格として閉じる。 | 専用動作PASSの主張なし |
| M03 | DisableMFA | 明示L2要求時は拒否 | Recovery以外のfactor-stageはproof/current-state参加を未資格として閉じる。 | 専用動作PASSの主張なし |
| M04 | RegenerateRecoveryCodes | 明示L2要求時は拒否 | Recovery以外のfactor-stageはproof/current-state参加を未資格として閉じる。 | 専用動作PASSの主張なし |
| M05 | ConsumeRecoveryCode | 接続構造のみ | Recovery消費を同一Txへ接続。後続Sessionまで一つの原子処理とは扱わない。 | TestB2IdentityRecoveryOneUseAndStageFailure |
| M06 | CreateMFAChallenge | 明示L2要求時は拒否 | Recovery以外のfactor-stageはproof/current-state参加を未資格として閉じる。 | 専用動作PASSの主張なし |
| M07 | GetMFAChallenge expired branch | 明示L2要求時は拒否 | Recovery以外のfactor-stageはproof/current-state参加を未資格として閉じる。 | 専用動作PASSの主張なし |
| M08 | DeleteMFAChallenge | 明示L2要求時は拒否 | Recovery以外のfactor-stageはproof/current-state参加を未資格として閉じる。 | 専用動作PASSの主張なし |
| K01 | CreatePasskeyRegistrationChallenge | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K02 | GetPasskeyRegistrationChallenge expired branch | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K03 | DeletePasskeyRegistrationChallenge | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K04 | CreatePasskeyCeremonySession | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K05 | GetPasskeyCeremonySession invalid branch | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K06 | ConsumePasskeyCeremonySession | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K07 | DeletePasskeyCeremonySession | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K08 | CreatePasskeyCredential | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K09 | UpdatePasskeySignCount | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| K10 | DeletePasskeyCredential | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| E01 | CreateEmailChangeChallenge | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| E02 | GetEmailChangeChallenge expired branch | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| E03 | ConsumeEmailChangeChallenge | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| E04 | deleteEmailChangeChallenge | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| O01 | CreateOAuthLoginState(login) | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O02 | CreateOAuthLoginState(account_link) | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O03 | CreateOAuthLoginState(connected_account) | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O04 | ConsumeOAuthLoginState | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O05 | continueOAuthLogin / completeLoginRedirect | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O06 | LinkOAuthUser | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O07 | autoProvisionOAuthUser | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O08 | DeleteOAuthUserLink(admin) | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O09 | DeleteOAuthUserLink(self) | 未接続 | OAuth state/Link/Provider版/外部検証Flowは未接続。明示L2の公開HTTP経路はpre-muxで拒否。 | 専用動作PASSの主張なし |
| O10 | CreateOAuthAccount | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| O11 | UpdateOAuthAccount(relink) | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| O12 | UpdateOAuthAccount(no token) | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| O13 | DeleteOAuthAccount | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| O14 | RecordOAuthAccountTokenRefreshAttempt | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| O15 | RecordOAuthAccountTokenRefresh(no rotation) | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| O16 | RecordOAuthAccountTokenRefresh(rotation) | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| O17 | RecordOAuthAccountTokenRefreshFailure | 未接続 | Integration資格の別coverage。Human Identityへ合算しない。専用Store/外部rotation/版CASは未接続。 | 専用動作PASSの主張なし |
| P01 | CreateOAuthProvider | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| P02 | UpdateOAuthProvider | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| P03 | DeleteOAuthProvider | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| P04 | UpdateSecuritySettingsRevision | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| P05 | UpdateAppSettingsRevision | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| P06 | UpdateSecret | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| P07 | Memory settings/secret implementations | SOURCE_ONLY | 既存Memory代替実装58関数との対応を維持。永続性/Restart/L2の証明にはしない。 | 専用動作PASSの主張なし |
| P08 | productionEnvironment / config readers | 対象外（理由を記載） | 今回のIdentity参加所有元の外。config/表示/UI/avatarの全体保証へ拡張しない。User削除の実FK閉包は別途実環境確認が必要。 | 専用動作PASSの主張なし |
| X01 | main / ProductStartup composition | SOURCE_ONLY | 既存設計・Sourceのみ。起動は明示L2時にDB/migrationより前で拒否。監査の独立DMLは今回L2へ接続しない。 | 専用動作PASSの主張なし |
| X02 | RunEmbeddedMigrations / runMigrationsFS / applyMigrationBody | SOURCE_ONLY | 既存設計・Sourceのみ。起動は明示L2時にDB/migrationより前で拒否。監査の独立DMLは今回L2へ接続しない。 | 専用動作PASSの主張なし |
| X03 | super_admin permission insert | SOURCE_ONLY | 既存設計・Sourceのみ。起動は明示L2時にDB/migrationより前で拒否。監査の独立DMLは今回L2へ接続しない。 | 専用動作PASSの主張なし |
| X04 | CreateFirstAdmin | 明示L2要求時は拒否 | 必要なproof/Provider/consume-stage契約が未完成。legacy offの正常動作は保持。 | 専用動作PASSの主張なし |
| X05 | CreateRole | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| X06 | UpdateRole | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| X07 | DeleteRole | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| X08 | canonical source trigger builder | SOURCE_ONLY | 既存設計・Sourceのみ。起動は明示L2時にDB/migrationより前で拒否。監査の独立DMLは今回L2へ接続しない。 | 専用動作PASSの主張なし |
| X09 | GetSession / other expired getters | 接続構造のみ | SessionのL2 getterは期限切れを拒否し、物理削除をしない純Read。暗黙cleanup参加は未接続。他factor getterはclosed。 | 専用動作PASSの主張なし |
| X10 | allow / record / clear | SOURCE_ONLY | 既存設計・Sourceのみ。起動は明示L2時にDB/migrationより前で拒否。監査の独立DMLは今回L2へ接続しない。 | 専用動作PASSの主張なし |
| X11 | updateSecret | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| X12 | UpdateAppSettingsRevision | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| X13 | beginSystemSettingsMutation | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| X14 | DeleteUser child/FK closure | 接続構造のみ | Store同一Tx・Checkpoint・履歴接続を実装。合成試験をCI必須Inventoryへ追加（実行結果は最終CI参照）。 | TestB2IdentityUserRoleSameTransaction / AssignmentNilEmptyAndLastAdmin / SessionPendingAndCurrentState / CascadeAndRestrictRollback |
| X15 | UpdateUserUIPreference | 対象外（理由を記載） | 今回のIdentity参加所有元の外。config/表示/UI/avatarの全体保証へ拡張しない。User削除の実FK閉包は別途実環境確認が必要。 | 専用動作PASSの主張なし |
| X16 | RunOAuthTokenRefreshLoop | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| X17 | UpdateSecretTx | 未接続 | Provider/Policy/Secret/独立した所有元の参加契約が未完成。製品L2資格なし。 | 専用動作PASSの主張なし |
| X18 | WriteAudit | SOURCE_ONLY | 既存設計・Sourceのみ。起動は明示L2時にDB/migrationより前で拒否。監査の独立DMLは今回L2へ接続しない。 | 専用動作PASSの主張なし |
| X19 | MemoryAuthStore mirrored User/Session/MFA/Passkey/Email methods | SOURCE_ONLY | 既存Memory代替実装58関数との対応を維持。永続性/Restart/L2の証明にはしない。 | 専用動作PASSの主張なし |
| V01 | UpsertUserAvatar | 対象外（理由を記載） | 今回のIdentity参加所有元の外。config/表示/UI/avatarの全体保証へ拡張しない。User削除の実FK閉包は別途実環境確認が必要。 | 専用動作PASSの主張なし |
| V02 | DeleteUserAvatar | 対象外（理由を記載） | 今回のIdentity参加所有元の外。config/表示/UI/avatarの全体保証へ拡張しない。User削除の実FK閉包は別途実環境確認が必要。 | 専用動作PASSの主張なし |

## Memory 58関数の対応

元B2-Aの対応表を全58行保全する。全行の今回状態は`SOURCE_ONLY`。MemoryのPASSを永続DBの証明へ読み替えない。Memoryへ新しい製品Lifecycle Providerは追加していない。

| Memory source 関数と行 | 主Inventoryとの対応 |
| --- | --- |
| [internal/store/memory_auth.go:88](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L88) CreateFirstAdmin | X04 |
| [internal/store/memory_auth.go:99](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L99) AddUser | X19 |
| [internal/store/memory_auth.go:177](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L177) UpsertUserAvatar | V01 |
| [internal/store/memory_auth.go:194](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L194) DeleteUserAvatar | V02 |
| [internal/store/memory_auth.go:217](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L217) ChangePassword | C02 |
| [internal/store/memory_auth.go:237](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L237) CreateSession | S01 |
| [internal/store/memory_auth.go:270](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L270) RefreshSession | S02 |
| [internal/store/memory_auth.go:292](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L292) DeleteSession | S04 |
| [internal/store/memory_auth.go:302](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L302) DeleteUserSessions | S05 |
| [internal/store/memory_auth.go:316](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L316) RecordLoginSuccess | U11 |
| [internal/store/memory_auth.go:331](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L331) RecordLoginFailure | U12 |
| [internal/store/memory_auth.go:367](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L367) StartTOTPEnrollment | M01 |
| [internal/store/memory_auth.go:382](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L382) ConfirmTOTPEnrollment | M02 |
| [internal/store/memory_auth.go:401](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L401) DisableMFA | M03 |
| [internal/store/memory_auth.go:411](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L411) RegenerateRecoveryCodes | M04 |
| [internal/store/memory_auth.go:427](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L427) ConsumeRecoveryCode | M05 |
| [internal/store/memory_auth.go:452](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L452) CreateMFAChallenge | M06 |
| [internal/store/memory_auth.go:481](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L481) DeleteMFAChallenge | M08 |
| [internal/store/memory_auth.go:491](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L491) CreateEmailChangeChallenge | E01 |
| [internal/store/memory_auth.go:513](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L513) GetEmailChangeChallenge | E02 |
| [internal/store/memory_auth.go:529](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L529) ConsumeEmailChangeChallenge | E03 |
| [internal/store/memory_auth.go:545](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L545) CreatePasskeyRegistrationChallenge | K01 |
| [internal/store/memory_auth.go:573](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L573) DeletePasskeyRegistrationChallenge | K03 |
| [internal/store/memory_auth.go:583](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L583) CreatePasskeyCeremonySession | K04 |
| [internal/store/memory_auth.go:611](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L611) ConsumePasskeyCeremonySession | K06 |
| [internal/store/memory_auth.go:630](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L630) DeletePasskeyCeremonySession | K07 |
| [internal/store/memory_auth.go:670](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L670) CreatePasskeyCredential | K08 |
| [internal/store/memory_auth.go:704](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L704) UpdatePasskeySignCount | K09 |
| [internal/store/memory_auth.go:722](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L722) DeletePasskeyCredential | K10 |
| [internal/store/memory_auth.go:736](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_auth.go#L736) WriteAudit | X18 |
| [internal/store/memory_admin.go:26](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L26) CreateUser | U01 |
| [internal/store/memory_admin.go:50](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L50) CreateOAuthUser | U02 |
| [internal/store/memory_admin.go:78](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L78) UpdateUser | U03/U04/U05 |
| [internal/store/memory_admin.go:108](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L108) SetUserStatus | U06/U07/U08/U09 |
| [internal/store/memory_admin.go:126](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L126) DeleteUser | U10 |
| [internal/store/memory_admin.go:176](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L176) ResetPassword | C01 |
| [internal/store/memory_admin.go:228](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L228) CreateRole | X05 |
| [internal/store/memory_admin.go:255](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L255) UpdateRole | X06 |
| [internal/store/memory_admin.go:274](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/memory_admin.go#L274) DeleteRole | X07 |
| [internal/store/oauth_login.go:61](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/oauth_login.go#L61) CreateOAuthLoginState | O01/O02/O03 |
| [internal/store/oauth_login.go:97](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/oauth_login.go#L97) ConsumeOAuthLoginState | O04 |
| [internal/store/oauth_login.go:133](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/oauth_login.go#L133) LinkOAuthUser | O06 |
| [internal/store/oauth_login.go:190](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/oauth_login.go#L190) DeleteOAuthUserLink | O08/O09 |
| [internal/store/security_settings.go:96](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/security_settings.go#L96) UpdateSecuritySettings | P04 |
| [internal/store/security_settings.go:132](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/security_settings.go#L132) UpdateSecret | P06 |
| [internal/store/app_settings_mutation_memory.go:8](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/app_settings_mutation_memory.go#L8) UpdateAppSettingsRevision | P05 |
| [internal/store/security_settings_mutation.go:9](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/security_settings_mutation.go#L9) UpdateSecuritySettingsRevision | P04 |
| [internal/store/integration_oauth_accounts_memory.go:24](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_accounts_memory.go#L24) CreateOAuthAccount | O10 |
| [internal/store/integration_oauth_accounts_memory.go:75](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_accounts_memory.go#L75) UpdateOAuthAccount | O11/O12 |
| [internal/store/integration_oauth_accounts_memory.go:119](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_accounts_memory.go#L119) RecordOAuthAccountTokenRefreshAttempt | O14 |
| [internal/store/integration_oauth_accounts_memory.go:138](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_accounts_memory.go#L138) RecordOAuthAccountTokenRefreshFailure | O17 |
| [internal/store/integration_oauth_accounts_memory.go:161](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_accounts_memory.go#L161) RecordOAuthAccountTokenRefresh | O15/O16 |
| [internal/store/integration_oauth_accounts_memory.go:198](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_accounts_memory.go#L198) DeleteOAuthAccount | O13 |
| [internal/store/integration_oauth_providers_memory.go:25](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_providers_memory.go#L25) CreateOAuthProvider | P01 |
| [internal/store/integration_oauth_providers_memory.go:74](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_providers_memory.go#L74) UpdateOAuthProvider | P02 |
| [internal/store/integration_oauth_providers_memory.go:105](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/integration_oauth_providers_memory.go#L105) DeleteOAuthProvider | P03 |
| [internal/store/user_ui_preferences.go:92](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/user_ui_preferences.go#L92) UpdateUserUIPreference | X15 |
| [internal/store/secret_transaction.go:43](https://github.com/Kome-Lab/Autostream-ControlPanel/blob/60137bb7f6bbcf830485ead3f5850809b767c5a4/internal/store/secret_transaction.go#L43) updateSecretLocked | P06/X17 |

## 継続条件

Startup freshness、既存embedded migration/起動Writer、Integration token refresh O10–O17、denial audit、v2 setup/first-admin、S12 Bootstrap/Recovery/Approval Consumption/Ceiling、秘密commitment実profile D4、Repo外Writer/旧版混在、実DB schema/engine/FK/triggerは未資格または未接続のまま。局所UserラベルでDeployment quarantineを解除しない。
