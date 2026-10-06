# UI Foundation action inventory

`ui-foundation-action-inventory.jsonl` and its `.meta.json` retain the exact
100-action authority at `34e32c276ddd6d235167112598a5ec851305ad8d`. Its 47,486
bytes and SHA-256
`eb5f0c21f9a9895a9fef23353d33ed81043640c646e55a1047d9b4d2ef862f37`
remain unchanged, as does the historical `ui-regression/actions.json` projection.

Part5 UI 174 makes the existing Discord target preset and video cover preset
CRUD APIs reachable through the Resource action controller. The six operations
`RES-41` through `RES-46` therefore extend the same action inventory schema in
`ui-foundation-action-inventory-v2.jsonl`, with migration wave `v2`. These are
current user actions and are not automatic-operation exclusions. The original
authority does not claim provenance for the new six rows.

`helpers/ui-foundation-action-inventory.mts` combines both inventories in ID
order. The current acceptance denominator is **106 unique actions**, including
**46 Resource actions**. Final coverage, Resource descriptor parity, and current
action parity consume this combined inventory; original-byte checks continue to
verify the frozen 100 rows separately. `preset-resource-actions.test.mts` is a
required operations suite, so its API, confirmation, revalidation, and revision
CAS checks cannot disappear from the blocking runner.

Each extension row records the actual mutation owner: `CreateResourceForm`,
`EditResourceButton`, or `DeleteResourceButton`. Preset update/delete use fresh
revision authority and consequence confirmation; all six actions have no
automatic retry. Uploaded cover image data remains governed by the existing
authenticated media API. These preset rows reference immutable asset IDs and do
not introduce a secret-input capability or a new upload contract.
