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

## Current UIとbrowser契約の補正

既存35 browserのReadiness source検査は、実row cell・detail・viewから同じaction controllerへの接続を個別に読む。権限snapshot、fresh取得、guard順序、単一mutationと403/pendingは維持する。可視・一意・enabledなtargetをscrollし、rect中心のviewportとhitを確認して既存native入力へ渡す。既存disabled負例だけはdisabledのまま実入力を試し、正例へ転用しない。Accountの言語切替後は実en tabを使う。

Tableのページ表示は一意なaria-live counterを読み、URLだけでなく実row範囲・履歴・sort/filter/page sizeを照合する。Workerはeligible fixtureの名前・種別と実confirmationのIDへ固定する。Nodesの登録済みlink、登録dialogの安全なName/Description、主section-navigationはchecked-in ownerへ結び付ける。正規GET /video-cover-presetsだけを追加し、未知path/method/origin拒否は維持する。

Previewの長文16条件は、変更しない128文字境界でのguard拒否・実利用不可表示・POST0を確認する。合法な既存条件は引き続き実POST1と応答403を要求する。映像再生の証拠ではない。Service Healthは同じquery/cacheの取得済み行をstale/errorと併記し、nodeの状態は既存canonical presenterを使う。Securityの初期loading、System Updatesの再取得表示と日英copyは実query状態に従う。

実keyboard controlに通常・forced-colorsのoutlineを付け、生成CSSのunlayered規則がoutline-none utilityより優先されることを検査する。AccountのMFA四入力とpasskey名称は明示labelと一意なid/htmlForを持つ。Table検索と列選択は狭幅で折り返し、列popupは同じflow内で収める。hidden native proxyはCSS寸法/clip/scaleと一意な可視peerを照合する。到達性の残失敗は値を含まない4KiB以内のgeometryを既存artifactへ残す。

Mobile createは開始Menuの実targetを一度だけcreate ownerへ渡す。実close callback後の同じfocus-scope cleanupを終えたmicrotaskで復帰し、MutationObserverは使わない。Page起点、dirty Stay/Discard、保存ack、cancel、再入、権限/session/unmountによる中止を既存ownerのまま検査する。制御DOM/SSR/socketと生成CSSは実Chromeのfocus・描画・native zoomの証拠ではない。

元4,144条件・35 browser・165契約・247 harness・206 current＋3 history・100 actionの目的を維持し、新規回帰は既存入口へ追加する。作成formの必須section経路が存在しない場合や、scope外の登録form label不足はFAIL/未証明のまま記録し、別操作や観測省略で代替しない。152 NOT_PROVENと固定history beforeの失敗、native200%、未対応Navigation APIの履歴、UA内部keyboard、Preview再生、新UI画像の未証明は継続する。全repoの改名・削除・build/CI整理はNOT_STARTED。
# Form sections, Node draft input, and create reentry

The form's existing `SectionNavigation` now targets its real basic, schedule, start, output, visual, and encoder regions. Create keeps `#create-stream`; edit/live-edit use instance IDs. The navigation only scrolls and focuses. `UI-FORM-SECTIONS-010` and `UI-KEYBOARD-FORM-010` exercise the actual form markup and section callback, both locales, unique references, Enter/Space, and missing/duplicate/hidden/wrong-focus negatives. The same 112 planned form conditions keep their purpose and point to `create-stream-basic` inside the main navigation.

The current legacy Node registration owner associates seven conditional label/control pairs with `useId`, including the visible SelectTrigger. Defaults, permissions, mutation and token ownership remain unchanged. The existing driver accepts only the exact source-bound worker defaults in a fresh condition document before its first registration dialog; both Name and Description and the visible worker type must still match. It uses the existing evaluate/native value setter/input/change boundary, including Textarea's own prototype, and restores the exact original value. `UI-FORM-CONTENT-009/011/012/013` bind source defaults, all 16 planned Nodes long-input IDs, the real emitted expressions, the runner permit, and rejection/cleanup paths. A partial failure never authorizes erasing an independent edit. The original empty-only Stream condition remains.

`StreamsView.requestCreateClose` joins normal Sheet close and the successful form's `onSaved` callback through the same draft guard. Its accepted callback records one short-lived close intent and requests the existing open state to close. The view layout consumes that intent after the actual `useDraftExit` layout has disabled the create guard. Only the same document, route and create generation may remove `#create-stream`, preserving pathname, search and framework state. New opens, navigation, permission/session loss and unmount invalidate old intents. Cancellation and exceptions do not cause retries. Shared guard/session code and the existing focus lease remain unchanged.

`UI-CREATE-REENTRY-010` through `013` retain Menu/Link/form/Sheet and save-ack coverage. `UI-CREATE-NAVIGATION-011` through `014` connect the real hook listener with synchronous cancelable replace events and commit/layout after the calling stack. The same route mounts hash synchronization once. Tests cover both APIs, clean/discard/Stay/pending, success/normal close, two creations, stale generations, Page/foreign routes, another active draft owner, cancellation, exceptions and listener cleanup. Moving History back into the accepted callback, removing the listener or committing from the setter violates these contracts. This controlled hook/event boundary is not a React/Chrome rendering or native focus acceptance test.

The runner owns each returned draft restoration action once. A sole body or restoration failure retains the original thrown object; a dual failure retains both objects in `DraftRestorationFailure`. The same outer writer keeps the primary failure status and writes at most one `<condition.id>.draft-restoration.json` through its existing `wx` output. That diagnostic has a closed schema, registered condition ID, booleans and fixed phase/code only, bounded to 4 KiB. It never serializes input, labels, HTML, URL, Storage, stack or arbitrary error text. Unknown causes use `RESTORE_UNKNOWN`. Fixture/socket/browser cleanup counts keep their existing meaning. Output failure stops later conditions and preserves both the condition and output causes, including an additional execution-output or server-close failure.

`UI-DRAFT-RESTORATION-011/012` run the actual exercise, real driver restoration, condition lifecycle and outer writer against controlled input/native-setter boundaries. `UI-DRAFT-OUTPUT-011/012` run the actual main exercise/catch/writer with existing socket fixtures and owned temporary files. They cover normal, primary-only, restore-only, dual and marker-composite failures, independent edits/owner replacement, exact error identities, single restoration, bounded non-secret output, real `wx` collision preservation and next-condition stop. The driver and its fresh Node defaults/original-value protection are unchanged. Existing suite entry points register these tests once; suite overlap is not added to an aggregate PASS count.

These are local source, controlled DOM/callback, and exact dependency checks. They do not establish native browser focus/geometry, UI images, official CI success, or UI acceptance. The fixed-history capture failure, 152 NOT_PROVEN, native 200% zoom, history without Navigation API, UA keyboard, Preview playback, and unaccepted images remain separate. All 4,144 IDs/order/applicability, status-only 40, and the original 35 browser registrations remain. Cross-repository naming/removal/build/CI/distribution cleanup: **NOT_STARTED**.
<!-- ui-regression correction 013 -->

013では、Worker表示結合の欠落optional値と明示不正値を区別し、既存normalizerと再起動controllerへ接続した。3つの読取り権限が全てない場合は日英の権限statusと未確認統計を表示し、認証取得中・失敗を区別する。query・permission・mutation ownerは維持する。

Visual ModeSelectの4通常用途と2preset用途は、用途を示す日英の可視labelと一意なSelectTrigger参照を持つ。Node作成の実triggerは同じcontrolled Dialogに属し、既存draft判断とRadixのfocus cleanupを使用する。runnerはdialog消滅後、同じopening targetへのfocus成立を既存期限内で待つ。Streams Refreshの対象はPageActions secondary、Nodesの英語ボタン名はRefreshとし、更新日時sortや共通辞書は変更しない。

datetime-localは実host内のTab反復と脱出を区別し、hostごと各方向16step、観測全体128stepの上限、両方向の順序・focus・値不変・実section activationを検査する。内部segment identityは `UA_DATETIME_SEGMENT_IDENTITY_PENDING` として残す。普通のcontrol、真のtrap、不可視・交換・編集・focus逸脱は拒否する。

旧35のMobile createとAccount Appearanceには、実段階・DOM状態・既存GET/PUT settlementの有限診断を追加した。失敗時だけ `UI_BROWSER_DIAGNOSTIC_013` の固定JSONを出し、原error/causeと診断失敗を保持する。原因未確定の製品focus/locale変更や再操作は行わない。

同じpackage/lockの所有exact環境で、実caller・発行式・制御DOM・callback・harness/socketによる正負回帰、strict、product typecheck、対象lint、current/history、source/binding/assembly/LOCを確認する。局所証拠は実ブラウザー、公式CI、画像、実focus、UI受入の代替ではない。旧35、4,144条件とstatus-only40のID・順序・目的・分母を維持する。

既存の52到達性・12focus位置、固定historyのbefore失敗、旧NOT_PROVEN、native200%、Navigation APIなし履歴、UA内部keyboard、Preview再生、新UI画像は未解決・未証明として保持する。全repoの改名・削除・build/CI整理はNOT_STARTED。013は非署名・未commit candidateで停止し、Bundle10 COMPLETEとは扱わない。
<!-- ui-regression correction 014 -->

014は013全sourceを継続し、Worker viewだけを補正する。`mergeOperationalNodes`は両方で互換idが欠落するときにown undefinedを生成しない。片側・両側の有効idと既存優先関係、013のoptional metadata処理を維持する。明示undefined/null/空/不正型/不一致/accessorは既存normalizerとcontrollerで拒否され、APIやfixtureのwireへidを追加しない。

成功済み認証dataのbackground refetch中は、同じsnapshotで許可された一覧の取得済みrowsを保持する。権限再確認中の日英copyで値の意味を示し、Worker queryのpartial/stale/background refresh通知を別に保持する。初回未取得、認証error、確定した読取り権限喪失ではcacheを表示根拠にしない。既存のquery・hasPermission・controllerは不変であり、auth fetching中の再起動evaluate/open/submitは引き続き拒否する。

`UI-WORKER-WIRE-014`は既存service_id-only factoryから実merge→normalizer→controllerへ接続し、/nodesなしを含む6結合形、同一identity、非破壊、GET/POST0のopen、fresh確認とPOST1のsubmit、重複・pending・権限・refresh拒否を検査する。`UI-WORKER-REFRESH-014`は実WorkersView/renderUIと同じQueryClientのidle→fetching→idleを日英・3読取り権限全8組合せで検査する。権限減少、実auth取得失敗、初回・denied・session終了、partial/stale、既存action拒否を含む。実宣言から作るrows消失・cache優先・auth gate緩和の変異も拒否する。両testは既存入口に一度だけ追加し、旧testとassertionを保持する。

同じ固定package/lockと所有exact依存で、変更sourceと実import closureのstrict193＋継承6root、product typecheck、warning0 lint、既存suite、source/binding/assembly/LOCを照合する。局所実行と再利用はprivate RESULTのhash対応で区別し、途中FAILを保存する。Worker244を含む全4,144条件のID・object・順序・分母、旧35とstatus-only40は不変であり、計画条件を実browser PASSへ換算しない。

013のVisual label、Node Dialog/focus、datetime有限観測とpending、Refresh selector、D013診断、protected733と歴史authorityを保持する。既存の未解決・未証明と旧FAILは継続する。全9repo cleanupは **NOT_STARTED**。014の到達点は非署名・未commit candidateと局所証拠であり、公式CI成功・実UI受入・Bundle10 COMPLETEを意味しない。
<!-- ui-regression correction 015 -->

015は既存restart controllerのprivate `permissionSnapshot` だけで、fetching→refreshingの後にauth queryの存在とstatus=successを必要条件とする。error/pending/不存在を残存permissionsでreadyにせず、既存copyStringArray、正規空配列のdenied、共通gate、submit順序・fingerprint・duplicate lock・POST/409/ambiguous処理は維持する。014のWorkersViewと013の各補正は変更しない。

`UI-WORKER-AUTH-015`は既存service_id-only factory→014の実merge/normalizer→実restart controllerと、実WorkersView.submitRestart callbackを接続する。実QueryClient.fetchQueryのreject後もdataが残る状態で、Worker GET前のauth errorはGET0/POST0、保留GET中のauth errorはGET1/POST0となる。onMutationStart・invalidate・成功noticeは発生せず、既存revalidation-unavailableへ戻る。GET/POSTは注入spyであり実サーバーへ送信しない。

fetching継続、cacheなしerror、pending、query除去、malformed/missing permissions、denied、success/wildcardを区別する。finallyの同一Worker lock解放と別Workerの独立性、回復後の新しい明示操作によるPOST1を検査し、自動再送は認めない。実関数本体を使うstatus確認削除・GET後の再判定省略・error時cache優先の変異も、同じ実callbackと保留GET経路で拒否する。旧22個のliteral testと全assertionを保持し、新しいtestを既存入口へ一度だけ追加する。

014原sourceでGET1/POST1となるbefore FAILと修正後の原ログを分離保存する。同じ固定package/lock・454manifest・HLS1.7.0の所有exact環境で、strict193＋継承6rootの実closure、product typecheck、warning0 lint、必要既存回帰、source/binding/assembly/LOCを候補へ対応付ける。再利用は動的readerを含むhash対応を示し、新実行へ加算しない。旧35、全4,144条件とstatus-only40は不変。

このcontrollerは既存226変更pathの外の既存fileであり、累計集合の増加を実差分として記録する。旧FAIL、D013実原因、52到達性/12focus位置、native200%、未対応履歴、UA keyboard、Preview再生、新UI画像等の未証明は保持する。全9repo整理は **NOT_STARTED**。非署名・未commit候補と局所証拠の提出で停止し、公式CI成功・UI受入・Bundle10 COMPLETEとは扱わない。


## Correction 017 — focus lifecycle and scoped responsive bounds

- Continued signed016 `8b2dd33718dd55ad7cd40ecd4bf9e2359687f303`, tree `ecbf0945dfc9a4f691a7040459b2fa4c4489fe41`, all 1,344 source/mode/blob records. This correction is an unsigned, uncommitted candidate; no delivery authorization is reused.
- B10-R11-001: the actual StreamsView hash effect connects/releases its existing focus lease by setup revision, document and route. Replay can reconnect the same lease before token-checked disposal; true unmount, permission/session loss, replacement and another generation cannot focus. Existing accepted-close post-commit intent, guard, hash, save acknowledgement and Page origin remain unchanged. UI-CREATE-REPLAY-017/018/019 execute actual effects and close callbacks at a controlled React lifecycle boundary, not a real-browser StrictMode acceptance run.
- B10-R11-002: only the datetime-local Input data-slot gains a focus-within outline and forced-colors Highlight counterpart. UI-DATETIME-CSS-017 uses actual Input SSR and generated CSS. The existing real exercise/socket retains both-direction escape, unchanged values, 16 host / 128 total limits and UA_DATETIME_SEGMENT_IDENTITY_PENDING.
- B10-R11-003: focusExpression accepts a tall active tabpanel only with the same Tabs owner, unique reciprocal IDs, one selected enabled visible tab, visible focus edges, ancestor clip and hit checks. Ordinary controls retain the full-rect rule. UI-TABPANEL-017 and UI-TABPANEL-KEYBOARD-017 cover emitted observer positives/negatives and actual ordered Tab/ShiftTab exercise/socket.
- B10-R11-004: the existing Worker browser literal now proves actual read-loss denial, absent data cells/triggers and no mutation/read requests during the observed denied interval; it waits for one additional legitimate auth response restoring read authority before the unchanged refreshing/pending/denied/fresh-GET/POST/403/409/ambiguous/focus steps. An empty DataTable results row is not a retained Worker row. UI-WORKER-READ-ACTION-017 separately proves action-only WKR-01 through the real QueryClient/controller/cache, explicit GET/POST spies and duplicate lock; UI-WORKER-SCENARIO-017 executes the actual browser predicate against actual denied-view SSR. Product Worker view/controller/auth/queries are unchanged.
- B10-R11-005/006: only specified Account profile/Email, Archive Select/ARC-03, Metrics range, Resource/Audit tabs/filter, Preview/Events and Host Settings controls gain intrinsic min/max-width or wrapping. ArchiveActionConfirmation receives an optional typed className with unchanged defaults/owners. The same StreamDetailsDialogContent uses `max-h-[calc(100%-2rem)]` with its existing internal overflow-y-auto. UI-BOUNDS-017 and UI-STREAM-BOUNDS-017 connect actual JSX/SSR, full option/provider names, emitted CSS and the unchanged close callback. Controlled percentage-height calculations are not evidence of 52 real-screen reachability cases or native zoom.
- D017: the existing Account English one-click path captures the actual scenario-marked target, planned rect/point and the same native call's captured mousedown/up/click trust/hit/identity; selected tab/panel and arrived versus explicitly settled GET/PUT counts remain distinct. Capture listeners and private observation owner are disposed in finally. D013 log compatibility is retained with D017-ACCOUNT detail, fixed fields, bounded numbers and <=4KiB; primary/cause and diagnostic/output failures remain observable, with one failure record and no success output. UI-D017-ACCOUNT is controlled boundary coverage; the official Account cause remains unknown.
- Same package/lock, owned exact 454 manifests, HLS 1.7.0. New evidence and all intermediate raw FAIL logs are recorded in the private 017 RESULT; overlapping suites/direct probes are not added together. Required final entry points: direct regressions, strict193+6 actual closure/noEmit/skipLibCheck:false, product typecheck, closure lint warning0, components, browser contracts, parity, current206/history3, harness247 and source/binding/assembly/LOC. The old35 literal IDs/registration/purposes, all4,144 IDs/order/denominator, status-only40, protected733 and original209/54patch/96 targets remain fixed.
- Supplied official016 evidence is 3,612 PASS /176 FAIL /356 NOT_PROVEN; old35 is31 PASS/4 FAIL (3 scenarios plus parent). Its new fixed-history run reached41 comparisons with0 differing pixels. Earlier InvalidInterceptionId failures and their unreached counts remain historical records; they are not the new history result. No live CI query or operation was performed here.
- Unproved: all356 official pending conditions, final datetime pending count, native200%, Navigation API absent same-document history, UA-internal keyboard identity, Preview playback, new UI images and independent UI acceptance. The 52 reachability/12 focus observations and Account cause await exact-candidate official/browser evidence. No local browser, production build, PNG acceptance or CI success is claimed. All9repo cleanup: NOT_STARTED. No Bundle10 COMPLETE claim.


## Correction 019 — panel scroll clearance and Worker denial phases

- Continued signed018 `a62a1aa5f90bc4c5019282699bab95d337f6f311`, tree `4a681fb76f0983be952d2399ab42d9702df78281`, with all 1,344 source/mode/blob records. Only the ten existing correction paths are eligible; this is an unsigned, uncommitted candidate, without pin changes or reused signing authorization.
- B10-R12-001: only Account security TabsContent and Audit's existing TabsContent gain `scroll-mt-20`. Its generated 5rem scroll margin leaves the existing 4.5rem sticky TopBar plus outline clearance. Role, reciprocal IDs, selected/value, Tab stop, children and handlers remain unchanged; common Tabs/TopBar/globals and the observer are read-only. UI-PANEL-SCROLL-019 verifies actual JSX/SSR and Tailwind/PostCSS output. UI-PANEL-TAB-019 connects the unchanged observer/exercise/native Tab socket in both directions, including next-control order, Japanese/English, mobile/desktop, normal/forced colors, short/tall content and CSS scale. Zero/insufficient margin, ordinary oversized controls, inactive/hidden/inert/covered/clipped panels, wrong or duplicate references and missing indicators still fail. Controlled geometry is not official acceptance of the twelve remaining focus conditions or native zoom.
- B10-R12-002: the existing Worker scenario's unknown, denied and revoked wait predicates retain disabled and require their original expected reason. Original assert.match checks remain. The existing auth GET settlement wait after revoked response arrival precedes the same fifteen-second UI wait; neither arrival nor settlement alone establishes rendered denial. UI-WORKER-DENIAL-019 executes the actual predicates and waitForWorkerAction against interim unknown, final denial, permanent wrong states and a broad-predicate mutant, retaining original assertions, target and deadline. Product permission/controller/view, request generation, read-loss absence, action-only independence, fresh GET/POST, duplicate/409/ambiguous/focus behavior and old35 registration are unchanged.
- D019-ACCOUNT-BOOTSTRAP: the existing diagnostic owner records mirror-written, before-navigate and the exact after-navigate snapshot used by the original immediate assertion. Root values are restricted to the existing twelve themes/three modes; mirror classification, readyState, document-change boolean, fixed bootstrap script/preload/queue/resource counts and scan bounds are closed. Fetched/queued never means executed. GET/PUT arrivals and last explicit settlement include batch identity; no new request or early DB wait is introduced. A local catch inside the actual child callback forwards the original assertion error, including when t.test absorbs its rejection. D013 prefix, one failure record, <=4KiB JSON, zero success output, primary/cause identity, diagnostic/output/cleanup failures and D017 one native input/finally cleanup are covered by UI-D019-ACCOUNT-CALLER/BOUNDS. The bootstrap's product cause is UNDETERMINED; no ThemeProvider/bootstrap/locale/DB fix is inferred.
- Same fixed package/lock and owned exact 454 manifests, HLS 1.7.0. Final local entries bind the new source vector: strict200 roots (193 plus inherited six plus Worker scenario), strict/noEmit/skipLibCheck:false; product typecheck; changed closure lint warning0; browser contracts, components, parity, current206/history-local3, original harness247, source/binding/assembly/LOC. Dynamic scenario readers are rerun, not assigned an old PASS. Original test bodies/IDs remain; new direct tests are included in existing suites, without duplicate counting. Raw intermediate failures and corrected runs are kept in the private RESULT.
- Original protected733, all4,144 condition objects/IDs/order/denominator and status-only40 remain fixed. Ordered-ID SHA256 is `1b7e2fe90b78c8f7e7ea78fa1bc4bae7cd407340f671cc0e1db9511ee0951501`. Only B9-M016's two existing Account/Worker scenario destinations follow their actual hashes/LOC/function symbols; original records, classifications, limits, current-copy-bindings and surfaces remain unchanged.
- Supplied official018 evidence belongs to the old source: 3,664 PASS /12 FAIL /468 NOT_PROVEN, old35 32 PASS/3 FAIL (two scenarios plus parent), cross-route focus PASS and all previous52 reachability conditions PASS. The twelve focus-position failures and Account bootstrap cause still need exact-candidate official evidence. No live CI query or operation was made here.
- Fixed history018 recurred at before archive-1440 / InvalidInterceptionId, before8/after0/comparison unreached. Its original failure is retained separately from official016's 41 comparisons/zero differing pixels. Local history3 does not replace either official result. All468 pending (128 UA media, 228 activation, 112 datetime segment identity), native200%, Navigation API absent history, Preview playback and independent image/UI acceptance remain unproved. No local browser/production build/PNG or official CI success is claimed. All9repo cleanup: NOT_STARTED; no Bundle10 COMPLETE claim.
