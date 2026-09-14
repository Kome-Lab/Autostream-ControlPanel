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

`typecheck:ui-regression`は005のstrict対象187ルートを保持した設定と新規直接回帰を、strict/noEmit/skipLibCheck:falseで検査し、current web jobでblocking実行する。これは既存製品typecheckと別の検査である。

protected.jsonの733原record/hashは変更しない。004時点では731pathはraw一致、session guardとharnessはapproved-source-deltas.jsonで固定旧hashと正確な追加構文だけを許可する。通知を除いたAST・残りのbytes、harnessの全既存member、runnerの局所narrowing除去後の原本一致を検査する。未知path、欠落supplement、元hash不一致、追加API、既存本文変更は失敗する。

native200%は必須未証明、実browser/focus/PNGは未実行のまま。局所socket回帰を実browser成功とは数えない。006の新しいcode SHA/pinはREAL_CODE_SHA_PENDING。署名配送済み005と現在pinは保持し、全9repo横断整理はNOT_STARTED。

## CI指摘の補正と条件所有

006は署名配送済み005のsourceを継続する非署名candidateである。製品のquery、認可、retry、action controller、Preview、draft、state ownerは変えない。Streamの9 cell componentをmodule-levelへ移し、現在のrow/locale/permission/callbackを同じcontextから渡す。行IDと元triggerを保持し、Loginの全文labelは折返し・幅・高さだけを補正する。

独立した計画条件ごとにBrowserHarnessとfixtureを一組だけ作る。fixture/init/viewport/media/clock/localeは最初の製品GET前に設定する。同じ条件内の履歴移動・回復・Table操作は同じinstanceで行い、失敗条件の再試行はしない。required responseのrelease、settlement、closeを順に行う。cleanup/outputの失敗は次条件への進行を止め、元の失敗を保全する。静的export/serverだけがfamily内で共有される。

各条件の「<id>.browser-version.json」「<id>.lifecycle.json」、必要時のFetch診断と「<id>.render-failure-*.json」を既存observation artifactへ保存する。keyboard traceは条件ID、方向、閉じたDOM identity、段階、geometry/booleanだけで4KiB以内。入力値、body/script text、任意のrequest/error payloadをtraceへ入れない。

可視dialogを操作scopeとし、closed detailsのsummary外と、属性・style・可視の対応widgetを確認できるnative form proxyを区別する。summaryを開いた時は列controlのlabel/可視性を検査し、元Tableの列表示変更・復元も保持する。全DOM/Storageのsecret・診断文字列、重複ID、label参照とzero-observationの失敗条件は残す。

有限overlay animationのfinished promise、同じowner、正の安定geometryを既存wait上限内で観測する。Escape前に閉じるownerを固定し、exit完了後に元triggerを照合する。無限skeleton/chart animationは一括待機しない。layoutによるnested scroll/focus復元後、同じgeometryでcaptureする。固定sleep、timeout増量、撮影用のmotion変更は行わない。

surfaces.jsonの事前keyboard経路を使い、必須triggerの欠落・飛ばし・逆順不一致・stationary・不可視focusを拒否する。modalでは全tabbable controlと両方向wrapを確認する。通常documentの境界、UA内部の未観測、modalからのescapeは別扱いである。native200%、UA keyboard、Navigation API非対応same-document履歴の必須未証明は維持する。

4,144条件のID/順序/適用数は維持する。Nodesのremote状態だけを既存 /admin/registered-nodes/ へ接続し、登録ready/formは /admin/nodes/ を使う。初期loadingは画面のrequired owner集合をholdし、各要求の実発行とresponse 0を確認する。英語failure待機とstate検査は同じpredicate。Archive共有一覧は実IDに対応するGETだけを追加し、live Previewは同じ既存issueの要求・応答後に1 POST/403を検査する。再生成功の証拠ではない。

## 型依存と追加の保護検査

HLS本体1.7.0と全既存lock recordを保持する。devDependencies追加は @svta/cml-cmcd 2.4.0、eventemitter3 5.0.4だけで、必要peerは @svta/cml-structured-field-values 1.1.3、@svta/cml-utils 1.5.0。dependency-integrity.test.mtsは旧lock・peer・registry・integrity・登録を検証し、installed version不一致もFAILにする。

元733recordのfixture bytes/hashと004の2例外は不変。現在は729path raw一致、004のharness/session 2path、006のworkflow checker/package-lock 2pathを別々に照合する。ci-source-deltas.json/.mtsは固定before hashと限定構造を使い、任意path、旧lock変更、余計なdependency、test削除、一括local参照免除を拒否する。go.mod/go.sumは変更しない。Goの実YAML位置/安全な既存workflow_call/外部full40SHAのpositive・negativeも実行する。

3つの新しいdirect testは既存test:ui-regression:browser-contractsに一度ずつ登録される。既存current web CIが同じscriptを呼ぶため、新jobや二重suiteは追加しない。元187 strict root/optionsは保持し、既存globとimportが新しいhelper/testを含める。

006時点の局所offline npm ciは既存cacheのNext 16.3.3欠落で未成立だった。指定4件の限定取得と既存cache上のHLS1.7.0を使った検査は、exact新lock検証ではない。このENOTCACHEDとmixed-cacheの117 PASS/1 FAILを履歴として保持する。

## Exact lockと状態観測の補正

007では別のprivate検証コピーと所有cache/node_modulesを使い、既存Node/npmから公式npm registryに限って、固定lockの依存を `npm ci --include=dev --ignore-scripts --no-audit --no-fund` で取得した。package/lockの前後hashは一致し、installed 454件の版不一致は0件。optional未導入分はOS/CPUとその依存到達性を分類し、適用対象の必須依存に欠落がないことを照合した。共有cacheやjunction先は変更せず、lockやHLS1.7.0は維持する。ignore-scriptsの導入結果をproduction buildやnative binaryの動作証拠にはしない。

status-onlyはMetrics/Monitoringのinitial-loading、公開Archive共有のinitial-loading・blocking-error・permission-deniedの5状態、既存40条件だけに明示する。既存observer/callerで、実GETと未応答または正しい403/503、route、可視main/heading/status/alert、文言、正のgeometryを要求する。空白、未到達、wrong HTTP、hidden control、readyでの操作消失、secret、ID/label参照異常、overflowは引き続き拒否する。4,144条件のID・順序・分母は変更しない。直接回帰は実componentのSSR、生成observer式、既存harness/socketのcounterを接続し、実Chromeの描画証拠とは区別する。

Dialogはconnectedな開き始めのownerを明示open・自動判定の両経路で固定し、その同一ownerがpaintedになるまで既存10秒のwait内で観測する。有限animationと安定geometryを確認し、owner交換・閉鎖・欠落・永久透明・キャンセル・期限超過・cleanup失敗を拒否する。一般のuiPainted、Fetch/fatal、期限、animation設定は変更しない。

公式CI・実Chrome・production build・PNG比較は今回未実行。native200%、Navigation APIなしのsame-document履歴、UA内部keyboard、Preview再生、新UI画像の必須未証明を維持し、局所PASSで補完しない。
