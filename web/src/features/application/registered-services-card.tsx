"use client";

import { Fragment } from "react";
import { GitCommit, ServerCog } from "lucide-react";
import { StatusBadge } from "@/components/admin/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { createUpdaterActionController, type UpdaterActionAuthority } from "@/features/application/updater-action-policy";
import type { SystemUpdateRequestState } from "@/lib/system-update-target-policy";
import type { AppVersion, SystemUpdateAgentStatus, SystemUpdateJob, SystemUpdatePortReconfigureCreateRequest, SystemUpdateTarget, WorkerNode } from "@/types/domain";
import { type PortReconfigureAuthorityContext, type RegisteredServiceOperation } from "./application-operation-types";
import { nodeIdentity } from "./registered-services-model";
import { SectionLabel, ServiceEndpointSummary } from "./service-endpoint-summary";
import { serviceTypeLabel, shortCommit, formatOptionalDate, UpdateStatusBadge, nodeUpdateState, serviceUpdateForNode } from "./service-update-presentation";
import { PortReconfigureControl } from "./port-reconfigure-control";
import { portControlKey } from "./port-reconfigure-authority";

export function RegisteredServicesCard({
  canViewNodeInfo,
  nodesError,
  nodesLoading,
  nodeRows,
  timezone,
  appVersion,
  targets,
  updaters,
  jobsByTarget,
  canExecuteSystemUpdates,
  updaterActionController,
  updaterAuthorityPermission,
  updaterAuthorityFreshness,
  portRequestTargetID,
  ambiguousPortTargetID,
  onPortReconfigure,
  onRefreshPortAuthority,
  onRefresh,
}: {
  canViewNodeInfo: boolean;
  nodesError: boolean;
  nodesLoading: boolean;
  nodeRows: WorkerNode[];
  timezone?: string;
  appVersion?: AppVersion;
  targets: SystemUpdateTarget[];
  updaters: SystemUpdateAgentStatus[];
  jobsByTarget: Map<string, SystemUpdateJob>;
  canExecuteSystemUpdates: boolean;
  updaterActionController: ReturnType<typeof createUpdaterActionController>;
  updaterAuthorityPermission: UpdaterActionAuthority["permission"];
  updaterAuthorityFreshness: UpdaterActionAuthority["freshness"];
  portRequestTargetID?: string;
  ambiguousPortTargetID?: string;
  onPortReconfigure: (request: SystemUpdatePortReconfigureCreateRequest) => Promise<void>;
  onRefreshPortAuthority: (context: PortReconfigureAuthorityContext) => Promise<UpdaterActionAuthority>;
  onRefresh: () => void;
}) {
  const nodeOperation = (node: WorkerNode) => {
    const nodeID = nodeIdentity(node);
    const target = targets.find((candidate) => candidate.target_id === nodeID);
    const updater = target?.updater_id
      ? updaters.find((candidate) => candidate.updater_id === target.updater_id)
      : undefined;
    const requestState: SystemUpdateRequestState = portRequestTargetID === nodeID
      ? "pending"
      : ambiguousPortTargetID === nodeID
        ? "ambiguous"
        : "idle";
    return {
      target,
      updater,
      latestJob: target ? jobsByTarget.get(target.target_id) : undefined,
      requestState,
    };
  };
  const operationalRows = nodeRows.filter((node) => node.service_type !== "update_agent");
  const updaterRows = nodeRows.filter((node) => node.service_type === "update_agent");
  return (
    <Card className="min-w-0">
      <CardHeader><CardTitle className="flex items-center gap-2"><ServerCog className="size-5" />登録済みサービス</CardTitle><CardDescription>報告バージョンと、希望・適用・Node報告のendpointを区別して表示します。</CardDescription></CardHeader>
      <CardContent>
        {!canViewNodeInfo ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">登録済みNodeの情報を確認する権限がありません。管理者にNode情報の閲覧権限を依頼してください。</div>
          : nodesError ? <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"><span>登録済みNodeの情報を取得できませんでした。通信状態とControl Panelのログを確認してください。</span><Button variant="outline" size="sm" onClick={onRefresh}>再試行</Button></div>
            : nodesLoading ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">読み込み中</div>
              : nodeRows.length === 0 ? <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">登録済みNodeがありません。Node登録ページで作成したNodeがある場合は、ページを更新してください。</div>
                 : <>
                      <div className="grid gap-4 2xl:hidden">
                        <div className="space-y-2">
                          <SectionLabel title="Nodeサービス" description="Worker、Encoder / Recorder、その他のサービス" />
                          {operationalRows.length > 0 ? (
                            <div className="grid gap-3">
                              {operationalRows.map((node) => (
                                <RegisteredServiceMobileCard
                                  key={node.service_id || node.id}
                                  node={node}
                                  operation={nodeOperation(node)}
                                  timezone={timezone}
                                  appVersion={appVersion}
                                   canExecute={canExecuteSystemUpdates}
                                   onRequest={onPortReconfigure}
                                   updaterActionController={updaterActionController}
                                   updaterAuthorityPermission={updaterAuthorityPermission}
                                   updaterAuthorityFreshness={updaterAuthorityFreshness}
                                   onRefreshAuthority={onRefreshPortAuthority}
                                />
                              ))}
                            </div>
                          ) : <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">Nodeサービスは登録されていません。</div>}
                        </div>
                        {updaterRows.length > 0 ? (
                          <div className="space-y-2">
                            <SectionLabel title="Updater / Host Agent" description="ホスト単位の更新専用。通常のNode endpointとは別に管理します。" />
                            <div className="grid gap-3">
                              {updaterRows.map((node) => (
                                <RegisteredServiceMobileCard
                                  key={node.service_id || node.id}
                                  node={node}
                                  operation={nodeOperation(node)}
                                  timezone={timezone}
                                  appVersion={appVersion}
                                   canExecute={canExecuteSystemUpdates}
                                   onRequest={onPortReconfigure}
                                   updaterActionController={updaterActionController}
                                   updaterAuthorityPermission={updaterAuthorityPermission}
                                   updaterAuthorityFreshness={updaterAuthorityFreshness}
                                   onRefreshAuthority={onRefreshPortAuthority}
                                />
                              ))}
                            </div>
                          </div>
                        ) : null}
                      </div>
                     <div className="hidden overflow-x-auto rounded-md border 2xl:block">
                       <Table className="min-w-[980px]">
                         <TableHeader>
                           <TableRow>
                             <TableHead>サービス</TableHead><TableHead>種別 / バージョン</TableHead><TableHead>状態</TableHead><TableHead className="min-w-80">Endpoint / ポート変更</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                           {nodeRows.map((node, index) => {
                             const operation = nodeOperation(node);
                             return (
                               <Fragment key={node.service_id || node.id}>
                               {(index === 0 || (nodeRows[index - 1]?.service_type === "update_agent") !== (node.service_type === "update_agent")) ? (
                                 <TableRow className="bg-muted/25 hover:bg-muted/25">
                                   <TableCell colSpan={4}>
                                     <SectionLabel title={node.service_type === "update_agent" ? "Updater / Host Agent" : "Nodeサービス"} description={node.service_type === "update_agent" ? "ホスト単位の更新専用" : "Worker、Encoder / Recorder、その他のサービス"} />
                                   </TableCell>
                                 </TableRow>
                               ) : null}
                               <TableRow>
                                 <TableCell>
                                  <div className="font-medium">{node.service_name || node.service_id || "-"}</div>
                                  <div className="mt-1 font-mono text-xs text-muted-foreground">{node.service_id || node.id}</div>
                                </TableCell>
                                <TableCell>
                                  <div>{serviceTypeLabel(node.service_type)}</div>
                                  <div className="mt-1">{node.reported_version || node.version || "未報告"}</div>
                                  <div className="mt-1 inline-flex items-center gap-1 font-mono text-xs text-muted-foreground"><GitCommit className="size-3.5" />{shortCommit(node.reported_commit)}</div>
                                  <div className="mt-1 text-xs text-muted-foreground">{formatOptionalDate(node.reported_build_date, timezone)}</div>
                                </TableCell>
                                <TableCell className="space-y-2">
                                  <StatusBadge status={node.health_status || node.status || "-"} />
                                  <div><UpdateStatusBadge state={nodeUpdateState(node, serviceUpdateForNode(node, appVersion))} /></div>
                                </TableCell>
                                <TableCell>
                                  <ServiceEndpointSummary node={node} />
                                  <PortReconfigureControl
                                    key={portControlKey(node, operation.target)}
                                    node={node}
                                    target={operation.target}
                                    updater={operation.updater}
                                    latestJob={operation.latestJob}
                                    requestState={operation.requestState}
                                     canExecute={canExecuteSystemUpdates}
                                     onRequest={onPortReconfigure}
                                     updaterActionController={updaterActionController}
                                     updaterAuthorityPermission={updaterAuthorityPermission}
                                     updaterAuthorityFreshness={updaterAuthorityFreshness}
                                     onRefreshAuthority={onRefreshPortAuthority}
                                  />
                                </TableCell>
                               </TableRow>
                               </Fragment>
                             );
                           })}
                         </TableBody>
                       </Table>
                     </div>
                     <p className="mt-3 text-xs text-muted-foreground">「現在適用中」が実際の接続先です。「希望」は未適用の値を含み、Node報告と異なる場合があります。</p>
                   </>}
      </CardContent>
    </Card>
  );
}

function RegisteredServiceMobileCard({
  node,
  operation,
  timezone,
  appVersion,
  canExecute,
  onRequest,
  updaterActionController,
  updaterAuthorityPermission,
  updaterAuthorityFreshness,
  onRefreshAuthority,
}: {
  node: WorkerNode;
  operation: RegisteredServiceOperation;
  timezone?: string;
  appVersion?: AppVersion;
  canExecute: boolean;
  onRequest: (request: SystemUpdatePortReconfigureCreateRequest) => Promise<void>;
  updaterActionController: ReturnType<typeof createUpdaterActionController>;
  updaterAuthorityPermission: UpdaterActionAuthority["permission"];
  updaterAuthorityFreshness: UpdaterActionAuthority["freshness"];
  onRefreshAuthority: (context: PortReconfigureAuthorityContext) => Promise<UpdaterActionAuthority>;
}) {
  return (
    <article className="min-w-0 overflow-hidden rounded-md border bg-background/60 p-3">
      <div className="min-w-0">
        <div className="break-words font-medium">{node.service_name || node.service_id || "-"}</div>
        <div className="mt-1 break-all font-mono text-xs text-muted-foreground">{node.service_id || node.id}</div>
      </div>
      <div className="mt-3 grid min-w-0 gap-3 sm:grid-cols-2">
        <div className="min-w-0">
          <div className="text-xs text-muted-foreground">種別 / バージョン</div>
          <div className="mt-1 break-words">{serviceTypeLabel(node.service_type)}</div>
          <div className="mt-1 break-words">{node.reported_version || node.version || "未報告"}</div>
          <div className="mt-1 break-all font-mono text-xs text-muted-foreground"><GitCommit className="mr-1 inline-block size-3.5" />{shortCommit(node.reported_commit)}</div>
          <div className="mt-1 text-xs text-muted-foreground">{formatOptionalDate(node.reported_build_date, timezone)}</div>
        </div>
        <div className="min-w-0 space-y-2">
          <div className="text-xs text-muted-foreground">状態</div>
          <StatusBadge status={node.health_status || node.status || "-"} />
          <div><UpdateStatusBadge state={nodeUpdateState(node, serviceUpdateForNode(node, appVersion))} /></div>
        </div>
      </div>
      <div className="mt-3 min-w-0 rounded-md border bg-muted/10 p-3">
        <div className="text-xs font-medium">Endpoint / ポート変更</div>
        <div className="mt-2 min-w-0">
          <ServiceEndpointSummary node={node} />
          <PortReconfigureControl
            node={node}
            target={operation.target}
            updater={operation.updater}
            latestJob={operation.latestJob}
            requestState={operation.requestState}
            canExecute={canExecute}
            onRequest={onRequest}
            updaterActionController={updaterActionController}
            updaterAuthorityPermission={updaterAuthorityPermission}
            updaterAuthorityFreshness={updaterAuthorityFreshness}
            onRefreshAuthority={onRefreshAuthority}
          />
        </div>
      </div>
    </article>
  );
}
