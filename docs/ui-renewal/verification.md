# UI検証の実行入口と証拠

この文書は現在の保守手順であり、未実行の検証をPASSとする報告ではありません。候補ごとの実commit、実行環境、ログ、結果と採否は別の受入記録を参照してください。

## 通常の開発

webディレクトリで、変更範囲に対応する既存入口を実行します。

```sh
npm run typecheck
npm run lint
npm run test:ui-regression:components
npm run test:ui-regression:parity
npm run test:ui-regression:browser-contracts
npm run typecheck:ui-regression
```

source整合性はリポジトリルートで次の入口を使います。検査対象は実際に存在するimmutable Git tree/commitです。

```sh
python3 -m unittest discover -s scripts/ci -p 'test_verify_source_integrity.py' -v
python3 scripts/ci/verify_source_integrity.py --repository Autostream-ControlPanel --tree "$(git rev-parse HEAD)"
```

source-origin-index.jsonは元対象の来歴、source-owner-map.jsonは現在の所有先です。元path/hashとcurrent destinationは同じものではありません。working treeが未commitなら、その変更をHEAD検査で検証済みと報告しないでください。

## 現在の実挙動

`test:ui-foundation-browser`は旧35目的を保持した実ブラウザ検査、`test:ui-foundation-operations`は現在の操作契約、`test:foundation-migrations`と`test:operation-witnesses`は既存の移行・操作検査です。名前が整理されても、permission、fresh評価、確認、単発POST、unknown/failureは従来の契約どおりです。

CIのcurrent UIは同じcommitのproduction exportを使用し、28画面群・4144条件を照合します。SSR・control I/O・CSS拡大の成功を、実ブラウザ・本物の200%ズーム・映像伝送E2Eへ読み替えません。PNGの取得と人による画像受入も区別します。

## source比較と歴史検査

`test:ui-source:contracts`／`test:ui-source:comparison`は比較器とsource比較用の入口です。現在UIと過去UIを無条件に同じ見た目へ戻すための基準ではありません。source pairと実行条件の要求を満たす場合だけ使用します。

`test:ui-regression:history`とCIのsource-historyは固定Git objectを読みます。旧characterization/compositionは固定b266の別checkoutで実行し、current treeに重複コピーを置きません。固定before/after41目的の比較と、現在UIの4144条件は別の証拠です。

## 保護sourceとartifact

protected.jsonの733原recordは保持します。currentから除去した歴史専用5件は、固定history objectとの一致とcurrent不存在を別に検査します。current名称変更はsource-normalizationで受入済みrawへ正確に戻し、その後で従来の限定変換を確認します。期待値を現在sourceへ合わせて検査を無効化しないでください。

UI export、各画面群の観測、history比較のartifactはworkflowと実SHA/run-attemptに対応させます。既存公開artifact/check名の一部は互換契約として保持されており、名前だけから古いsourceだと判断しません。body/secretをログへ追加しないでください。

## 履歴と完了境界

過去候補のFAIL、PENDING、局所検証、手動確認の経緯は、[旧検証記録](../history/ui-renewal-verification-through-047.md)に原文のまま保存しています。過去の未完了を現在の未完了として扱ったり、過去のPASSを新candidateの結果として扱ったりしません。

source整理の候補完成、署名配送、最終exact-SHA CI、独立受入、main統合、release/本番は別の状態です。保守作業を理由に既存鍵、hook、保護設定、署名履歴やNodeA3の有効化条件を変更しないでください。
