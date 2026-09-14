# UI regression: 実行入口と証拠

## current source

web/package.jsonの役割名を唯一の新suite入口にします。web directoryで実行する局所検査:

    npm run test:ui-regression:components
    npm run test:ui-regression:parity
    npm run test:ui-regression:browser-contracts
    npm run typecheck

componentsは実React/TanStack componentのSSRとproduction state/URL adapterを使います。parityは59 static entry、100 action、保護bytes、26 navigation binding、依存と旧35 browser登録を確認します。browser-contractsは新しいmatrix / capture / layout / navigationのguardと、current harness上の比較器core9件・診断core30件を登録します。実ブラウザの代用として報告してはいけません。

元35 browserケース、247 harness、API/認証/CSRF/Action/secret/modelとUI operationsの回帰はcurrent Webで継続します。旧表示のcallerを読む比較43件・診断38件の原本全体は固定履歴sourceで実行します。current coreを別名で二重登録しません。テストのskip filterは使いません。

## CIとartifact

既存ci.ymlのui-regression jobがui-regression.ymlを呼び、既存overallはこのjobも必須にします。

|producer|artifact / output|consumer|
|---|---|---|
|export: candidateのnpm run build + export-provenance.mjs|ui-regression-export-${github.sha}-${github.run_attempt}; web/out/|browser全28 familyが同名をdownload|
|export-provenance.mjs|web/out/ui-regression-source.json。実HEAD、tree、lock hash、59 export entryのhash|run-browser.mtsが実HEAD = GITHUB_SHA = export commitを確認|
|browser: test:ui-regression:browser|ui-regression-observations-${family}-${github.sha}-${github.run_attempt}|個別plan、JSON、PNG、layout、failure、executionの解析|
|source-history|ui-regression-history-${github.sha}-${github.run_attempt}|履歴比較のsource/provenance・41対のJSON/PNG/RGBA結果|
|required|export / browser / source-historyすべてsuccess|呼出元ui-regressionとoverall|

既存ST-PORT artifactの名前・needs・同一source setの条件は維持します。実行sourceを偽るGITHUB_SHAの上書きは行いません。未知commit SHAやtreeを実commit pinへ書き込みません。

## 履歴の位置付け

固定beforeと受入017の固定after b266aabb896f0df9880a0354c1135130d6938e8dを使います。旧characterization、source composition、期待hash、fixture、comparison helperはbyte-identicalに保管します。別checkoutの実HEADもafterに一致する必要があります。

historical-comparison.mjsは現在のworkflow sourceから固定sourceを指定し、旧capture / 比較器をそのsourceから読み込みます。before/afterのlock一致、実export、全RGBA差分0を要求します。新しいUIを旧画像へ戻す基準には使いません。

## matrixと未実行の区別

- 28 familyのready: 7幅 × Light/Dark × ja/en = 784条件。
- 共通Shell/Table/Form/Detail/Confirmation: 5 fixture × 12 theme × Light/Dark × ja/en × 2幅 = 480条件。
- 非readyはsurfaces.jsonの8状態それぞれに適用可否と契約理由を記録し、適用する状態を390/1440 × Light/Dark × ja/enで展開します。
- keyboard、200%拡大、forced colors、reduced motion、long text、long ID、system-modeは別条件です。拡大は実DOMのCSS zoom 2を使い、native browser zoomの操作証拠とは区別します。
- 各familyは事前計算したID集合に対して完全一致を要求します。missing/duplicate/zero/FAIL/SKIP/CANCELLED/NOT_REACHEDは非成功です。

PNGはraw観測であり、常にNOT_REVIEWEDとして保存します。document overflowに加えてcontrolの到達性、label/説明の切れ、入力label、focus、secret/diagnostic漏れを調べます。実Table操作はsearch/filter/sort/page size/column visibility/history/focusの製品callerを操作します。実行前に「到達済み」「見た目受入済み」とは扱いません。

この候補を扱う局所環境では実browser、production build、PNG比較、root/Docker/systemdは実行条件にしません。既存依存の再利用結果と、CIでlockどおりに導入して行う実build/実UIの結果を分けます。SHA依存のstatus/residual locator、後続Updater参照は実code commitが成立するまで保留し、テスト期待値で隠しません。


## 残件履歴とcurrent接続

旧残件10 recordとNode 145/5/5のcount/hashは変更せず、`test:ui-regression:history`が固定Git object b266aabb896f0df9880a0354c1135130d6938e8d / 8863c785aaf70f443e396ed3f90045bdee71d15eを読む。元operations209のうち残件履歴3をこの入口に移し、他206は元current operationsで維持する。同じsuiteを別名で二重登録しない。

current-copy-bindings.jsonは新しい実import・copy owner・表示keyへの接続を検査する。実componentからcopy接続を外すnegativeは、描画後の英語本文検査で失敗する。古い残件countの失敗で代用しない。

## driverと未証明

計画は4,144条件。CSS拡大は`css-magnification-200`、native 200%は独立した必須項目`NATIVE_ZOOM_EVIDENCE_PENDING`として残す。実行前の条件はPASSに数えない。

pageIndex=1は画面2。Mobileは可視toolbarのnative select、Desktopは可視headerを操作する。denied時の要求数はstate-drivers.mtsで既存queryごとに定義する。通常viewer fixtureに管理者roleを残さない。partial/staleは成功response・更新失敗・保持された画面データを照合する。long-contentはsynthetic display fieldと参照IDだけを変え、metric名などのcodeは変えない。

keyboardは実focus移動・dialog containmentを検査し、既存disclosureとsection navigationではEnter/Spaceを一回ずつ実行してclick数と実効果を観測する。対応する非mutation操作のないsurfaceはpendingで記録する。reverse Tabは独立したpressTab("backward")へ接続し、実前進列を逆順で戻った観測が成立した条件だけを実施済みとする。未観測をPASSにしない。Escapeは実際の元triggerとのDOM同一性を照合する。

forced-colors/reduced-motionはmatchMedia、focus、文字の状態説明、実animation/transitionとchart設定を照合する。layout測定は全nested scroll位置とfocusをfinallyで復元した後にcaptureする。PNGはNOT_REVIEWED。

局所SSRのquery cacheは固定synthetic dataで、明示した失敗状態の再mountによる再取得を止めて表示projectionを確認する。これは製品のretry/query条件を変更せず、実browserの要求数証明でもない。


## draft・入力・型の直接回帰

通常beforeunloadはnavigateの有無・順序に依存せず現在のdirty readerを確認する。known cross-documentはnative確認だけを要求し、same-documentとfallback anchorは既存のstay/discard確認を使う。anchorの許可はそのevent turnに限定しdirtyを消さない。Navigation APIのないsame-document履歴移動は未証明のまま。native確認UIの表示はuser activation等に依存し、handlerの確認要求と別の未実行証拠とする。

既存logout onSettledとsession guardの確定済みredirect直前だけで、非機密のdocument通知を送る。購読中のフォームだけが受け取り、新規購読へ引き継がない。次の入力・navigation完了/取消・document再表示・error・解除、または5秒の期限で破棄する。認証predicate、refresh、CSRF clear、URL、retryとlogout同期single-flightは維持する。通常loginリンクは終了通知ではない。

STR-01の成功は既存送信intentの同一性に対応する非機密比較snapshotへackし、そこに含まれたvisualだけを同じvisual ownerへ通知する。STR-02/03は独立したvisual編集を解除しない。失敗・409・outcome_unknown・無効結果・別intent・送信後の編集は保存済みにしない。入力・secret・upload sessionの新規保存先や別mutation ownerは作らない。

form-content-driverの実生成式は日英の安全な空text入力だけを選び、両long-content exerciseを実行する。hidden/disabled/重複/credential/非空入力を拒否し、変更したsynthetic入力とmarkerを復元する。旧templateのescape欠落を戻すnegativeは実生成式のSyntaxErrorを検出する。

`typecheck:ui-regression`は元strict対象179ルートを保持した設定と新規直接回帰を、strict/noEmit/skipLibCheck:falseで検査し、current web jobでblocking実行する。これは既存製品typecheckと別の検査である。

protected.jsonの733原record/hashは変更しない。731pathはraw一致、session guardとharnessはapproved-source-deltas.jsonで固定旧hashと正確な追加構文だけを許可する。通知を除いたAST・残りのbytes、harnessの全既存member、runnerの局所narrowing除去後の原本一致を検査する。未知path、欠落supplement、元hash不一致、追加API、既存本文変更は失敗する。

native200%は必須未証明、実browser/focus/PNGは未実行のまま。局所socket回帰を実browser成功とは数えない。実code SHA/pinはREAL_CODE_SHA_PENDING、全9repo横断整理はNOT_STARTED。
