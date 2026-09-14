
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { updaterSettingsTargetOptions, updaterSettingsTargetRequiresDatabase, updaterSettingsTargetRequiresLocalListenPort } from "@/lib/updater-settings-model";
import type { SystemUpdateTarget, UpdaterSettings, UpdaterSettingsTarget } from "@/types/domain";
import { serviceTypeLabel, selectOptionsWithCurrent, deploymentModes } from "./updater-settings-form-model";
import { Field } from "./updater-settings-field";



export function UpdaterTargetsSettingsSection({
  formID,
  executionHostID,
  canEdit,
  canAddRegisteredTarget,
  nextTargetHostID,
  targets,
  availableTargets,
  hostOptions,
  transportMode,
  addTarget,
  updateTarget,
  selectTarget,
  removeTarget,
}: {
  formID: string;
  executionHostID: string;
  canEdit: boolean;
  canAddRegisteredTarget: boolean;
  nextTargetHostID: string;
  targets: UpdaterSettingsTarget[];
  availableTargets: SystemUpdateTarget[];
  hostOptions: {value: string; label: string}[];
  transportMode: UpdaterSettings["transport_mode"];
  addTarget: () => void;
  updateTarget: (index: number, patch: Partial<UpdaterSettingsTarget>) => void;
  selectTarget: (index: number, targetID: string, serviceType: string) => void;
  removeTarget: (index: number) => void
}) {
  const uiText = useUICopy();
  return (
<section className="space-y-3" aria-labelledby={`${formID}-targets-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 id={`${formID}-targets-heading`} className="font-medium">{uiText("更新するサービス")}</h3>
              <p className="text-xs text-muted-foreground">
                {uiText("実行ホスト {0} 上で管理するAutoStreamサービスを指定します。ホスト割り当てはControl Panelが管理します。", executionHostID || uiText("未割り当て"))}
              </p>
            </div>
            {canEdit ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!canAddRegisteredTarget}
                title={!nextTargetHostID
                  ? uiText("先にホストを設定してください。")
                  : !canAddRegisteredTarget ? uiText("追加できる未使用の登録サービスがありません。") : undefined}
                onClick={addTarget}
              >
                <Plus className="size-4" />
                {uiText("サービスを追加")}</Button>
            ) : null}
          </div>

          {targets.length === 0 ? (
            <div className="rounded-md border border-dashed p-5 text-sm text-muted-foreground">{uiText("更新対象サービスはまだありません。")}</div>
          ) : (
            <div className="space-y-3">
              {targets.map((target, index) => {
                const targetOptions = updaterSettingsTargetOptions(availableTargets, targets, index);
                const selectedTargetID = String(target.service_id || target.target_id || "").trim();
                return (
                  <div key={index} className="grid gap-3 rounded-md border p-4 sm:grid-cols-2 lg:grid-cols-[1.2fr_1fr_1fr_1fr_auto] lg:items-end">
                    <Field label={uiText("NodeサービスID")} htmlFor={`${formID}-target-${index}-id`}>
                      <Select
                        value={selectedTargetID}
                        onValueChange={(value) => {
                          const selected = targetOptions.find((option) => option.value === value);
                          if (!selected || selected.stale) return;
                          selectTarget(index, selected.value, selected.serviceType);
                        }}
                        disabled={!canEdit || !targetOptions.some((option) => !option.stale)}
                      >
                        <SelectTrigger id={`${formID}-target-${index}-id`}><SelectValue placeholder={uiText("登録サービスを選択")} /></SelectTrigger>
                        <SelectContent>
                          {targetOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label={uiText("実行ホスト（サーバー管理）")} htmlFor={`${formID}-target-${index}-host`}>
                      <Select value={target.host_id || executionHostID} onValueChange={(value) => updateTarget(index, { host_id: value })} disabled>
                        <SelectTrigger id={`${formID}-target-${index}-host`}><SelectValue placeholder={uiText("ホストを選択")} /></SelectTrigger>
                        <SelectContent>
                          {hostOptions.map((host) => <SelectItem key={host.value} value={host.value}>{host.label}</SelectItem>)}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label={uiText("サービス種別（自動）")} htmlFor={`${formID}-target-${index}-service`}>
                      <Input
                        id={`${formID}-target-${index}-service`}
                        value={serviceTypeLabel(target.service_type)}
                        readOnly
                        aria-readonly="true"
                      />
                    </Field>
                    <Field label={uiText("配備方式")} htmlFor={`${formID}-target-${index}-mode`}>
                      <Select value={target.deployment_mode || "systemd"} onValueChange={(value) => updateTarget(index, { deployment_mode: value })} disabled={!canEdit}>
                        <SelectTrigger id={`${formID}-target-${index}-mode`}><SelectValue /></SelectTrigger>
                        <SelectContent>
                          {selectOptionsWithCurrent(deploymentModes, target.deployment_mode).map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
                        </SelectContent>
                      </Select>
                    </Field>
                    {canEdit ? (
                      <Button type="button" variant="ghost" size="icon-sm" onClick={() => removeTarget(index)} aria-label={uiText("サービス {0} を削除", index + 1)}>
                        <Trash2 className="size-4" />
                      </Button>
                    ) : null}
                    {updaterSettingsTargetRequiresDatabase(transportMode, target) ? (
                      <div className="sm:col-span-2 lg:col-span-full">
                        <Field
                          label={uiText("MariaDBデータベース名")}
                          htmlFor={`${formID}-target-${index}-database-name`}
                          hint={uiText("このサービスが実際に使用しているデータベース名です。ユーザー名・パスワード・DSNは入力しません。")}
                        >
                          <Input
                            id={`${formID}-target-${index}-database-name`}
                            value={target.database_name || ""}
                            onChange={(event) => updateTarget(index, { database_name: event.target.value })}
                            disabled={!canEdit}
                            maxLength={64}
                            spellCheck={false}
                            autoCapitalize="none"
                            placeholder={target.service_type === "control_panel"
                              ? "autostream_control_panel"
                              : "autostream_observability"}
                          />
                        </Field>
                      </div>
                    ) : null}
                    {updaterSettingsTargetRequiresLocalListenPort(transportMode, target) ? (
                      <div className="sm:col-span-2 lg:col-span-full">
                        <Field
                          label={uiText("ローカル待受ポート")}
                          htmlFor={`${formID}-target-${index}-local-listen-port`}
                          hint={uiText("systemdサービスがこのホストの127.0.0.1で実際に待ち受けるポートです。Cloudflare Tunnelなどの公開HTTPSポート443とは分けて指定します。")}
                        >
                          <Input
                            id={`${formID}-target-${index}-local-listen-port`}
                            type="number"
                            min={1024}
                            max={65535}
                            value={target.local_listen_port ?? ""}
                            onChange={(event) => updateTarget(index, {
                              local_listen_port: event.target.value === ""
                                ? undefined
                                : Number(event.target.value),
                            })}
                            disabled={!canEdit}
                            inputMode="numeric"
                            placeholder={target.service_type === "observability" ? "8082" : "8084"}
                          />
                        </Field>
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          )}
        </section>
  );
}
