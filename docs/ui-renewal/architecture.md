# UI renewal: 表示構成と責任境界

この文書はUI表示世代の保守案内です。実装の独立受入、公式CI成功、配布可能性を宣言するものではありません。

## 共通表示

- AppShell / AppSidebar / MobileNavigationを再利用し、既存の26 navigation href・ANY permissionの組を保ちます。1024pxは折畳み、1280px以上は展開、運用一覧は広い画面を使います。
- PageHeader / PageActionsでページh1、主要操作、更新、補助操作を分けます。背景のblurや全面の装飾を避け、既存12テーマと意味色を維持します。
- DataTableは実TanStack Tableの1 instanceを使います。TableRecordは1 row / 1 cell renderingで、モバイルも同じaction ownerです。TableToolbar / TablePaginationは同じtable stateへ接続します。
- 有限集合のclient modeは検索、並べ替え、enum filter、列表示、8/20/50/100件、ページ境界を扱います。server modeは受信したpageの順序・件数を保ち、総数不明と表示します。
- table-location / use-table-urlは実popstateを購読し、framework history state、別用途のqueryとhashを維持します。自由検索語、資格情報、メールをURLやStorageに保存しません。
- Field / FieldGroup、FormFooter、DefinitionList、DetailSection / SectionNavigationは既存のcontrolとcontrollerを包みます。別のquery client、mutation owner、secret ownerは作りません。

FormFooterは既存formの操作を表示します。離脱確認は同じform ownerの非機密fieldまたは既存dirty状態をDraftExitContextへ登録し、同じclose/tab/target変更callbackから呼びます。保存成功だけで比較baselineを確定し、失敗・曖昧結果・pendingは保持します。secret値は比較・保存しません。forced logoutと権限喪失は確認を迂回します。Navigation APIの通常navigation/back/reload経路へ接続し、非対応engineのreload/backは未証明のままです。

## 画面ごとの構成

|画面群|表示の整理|維持する責任|
|---|---|---|
|Dashboard|稼働配信、重要incident、基盤状態、設定数を区別。2〜3列の表示|既存queryとstatus Foundation。追加incident GETはincidents.readで有効化|
|Streams / create / detail|名称の実button、一覧toolbar、Readiness再確認、detailのsection、form footer|StreamActionController、STR-01〜10、選択・作成・編集state、Preview/HLS owner|
|Workers / Nodes|共通sub-navigation、登録・接続・health・assignment・jobの分離|既存Node/Worker query、restart/configuration controller。Node A3は無効のまま|
|Archive / public share|処理・成果物を先に表示し、profile/destination・現在/過去runを分離|既存run選択・artifact更新・共有URL one-time owner。公開shareにadmin shellを付けない|
|Account / login|profile/security/appearance、未知のセキュリティ状態、入力label、更新表示|既存session/MFA/passkey/OAuth ceremony、DB preference revision、secret reveal/erase|
|Monitoring / Metrics / Audit / 履歴resource|ページheader、section、状態と時刻、実chart、server modeの一覧|既存期間・polling・cursor・query key・reconcile、Auditのserver検索|
|Application / System updates|overview、target、history、登録service、Host/runtime/target/secretのsection|revision/epoch/eligibility、active-stream保護、bootstrap暗号化、単一controller|
|Generic resources / Settings|共通Fieldとfooter、同じDataTableのrow action、ページ名|RES-01〜40、APP-01/02、permission、payload、secret lifecycle|

既存の認証・API・CSRF・status vocabularies・migration・installer・public contractは表示層の責任へ移しません。製品のrelease bundleという語も維持します。

## 検査の入口

[verification.md](verification.md)からcurrent / historicalの入口とartifactの対応を確認できます。画面・entry・actionの機械可読対応はweb/tests/fixtures/ui-regression/のsurfaces.json、entries.json、actions.jsonです。100 actionの元recordはID、permission、risk、duplicate、retry、secretを維持し、現在のUI owner pathを追加しています。

28 familyの本文・form・状態・accessible nameは既存I18nProviderとdomain別copyに接続します。ユーザー入力・ID・API enumは保持します。固定の安全なpresenter出力は上位adapterで表示し、raw provider errorを辞書へ渡しません。実componentのja/en描画とsource bindingは局所検査対象です。画像・実browserの独立受入とは区別します。

## 後続作業

全9repoの旧工程名・不要物・build/CI/配布整理はNOT_STARTEDです。固定source集合と承認済みの個別処置・consumer対応表が渡されるまでは、名前だけの削除、一括改名、worktree/branch整理を始めません。
