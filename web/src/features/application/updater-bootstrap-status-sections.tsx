import { ServerCog } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { isUpdaterHostBootstrapJobActive, systemUpdateHostBootstrapStatusLabel, updaterHostBootstrapEligibility, updaterHostBootstrapEligibilityMessage } from "@/lib/updater-bootstrap";
import type { UpdaterSettingsHost } from "@/types/domain";
import { type Feedback, eligibilityStatus, bootstrapResultSafeMessage, bootstrapBadgeTone } from "./updater-bootstrap-presentation";
import { latestBootstrapResults } from "./updater-bootstrap-job-state";


export function BootstrapSetupHeading({
  canEdit,
  bootstrapStatusReady,
  bulkHostIDs,
  busy,
  openCredentialForm,
}: {
  canEdit: boolean;
  bootstrapStatusReady: boolean;
  bulkHostIDs: string[];
  busy: boolean;
  openCredentialForm: (hostIDs: string[], mode: "single" | "bulk") => void
}) {
  return (
<div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 font-medium"><ServerCog className="size-4" />helper自動セットアップ</div>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-muted-foreground">
            管理者SSH認証を今回だけ使用し、独立Updaterから制限付きhelperを導入して動作確認します。対象ホストで個別にインストールコマンドを実行する必要はありません。
            helperは更新時だけSSH経由で起動し、対象ホストに常駐service・listener・helper専用port・helper用env・Node Runtime Tokenは作成しません。
            bootstrap対象ホストはpull_v2の実行対象・所有権から独立して保存されます。自動セットアップは検証済みの標準Host Agent profileだけに対応し、実際のhealth・version応答まで確認します。カスタム構成は手動導入になります。
          </p>
        </div>
        {canEdit ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!bootstrapStatusReady || bulkHostIDs.length === 0 || busy}
            onClick={() => openCredentialForm(bulkHostIDs, "bulk")}
          >
            <ServerCog className="size-4" />
            未セットアップを一括セットアップ{bulkHostIDs.length ? ` (${bulkHostIDs.length})` : ""}
          </Button>
        ) : null}
      </div>
  );
}


export function BootstrapHostResults({
  currentHosts,
  latestResults,
  eligibilityByHostID,
  bootstrapStatusReady,
  canEdit,
  busy,
  openCredentialForm,
}: {
  currentHosts: UpdaterSettingsHost[];
  latestResults: ReturnType<typeof latestBootstrapResults>;
  eligibilityByHostID: Map<string, ReturnType<typeof updaterHostBootstrapEligibility>>;
  bootstrapStatusReady: boolean;
  canEdit: boolean;
  busy: boolean;
  openCredentialForm: (hostIDs: string[], mode: "single" | "bulk") => void
}) {
  return (
<div className="space-y-2">
        {currentHosts.map((host) => {
          const result = latestResults.get(host.host_id);
          const eligibility = eligibilityByHostID.get(host.host_id);
          const displayStatus = result?.status || eligibilityStatus(eligibility?.reason, bootstrapStatusReady);
          const buttonLabel = result?.status === "succeeded"
            ? "再セットアップ"
            : result?.status === "failed" || result?.status === "credential_expired"
              ? "再試行"
              : "セットアップ";
          const disabled = !canEdit || !bootstrapStatusReady || !eligibility?.ready || busy;
          return (
            <div key={host.host_id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-background/80 p-3 text-sm">
              <div className="min-w-0">
                <div className="truncate font-medium">{host.name || host.host_id}</div>
                <div className="mt-0.5 text-xs text-muted-foreground">{host.address || "接続先未入力"}:{host.port || 22}</div>
                {result ? <div className="mt-1 break-words text-xs text-muted-foreground">{bootstrapResultSafeMessage(result.status)}</div> : null}
              </div>
              <div className="flex items-center gap-2">
                <Badge variant={bootstrapBadgeTone(displayStatus)}>{systemUpdateHostBootstrapStatusLabel(displayStatus)}</Badge>
                {typeof result?.progress === "number" && isUpdaterHostBootstrapJobActive(result.status) ? (
                  <span className="text-xs text-muted-foreground">{Math.max(0, Math.min(100, Math.round(result.progress)))}%</span>
                ) : null}
                {canEdit ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={disabled}
                    title={updaterHostBootstrapEligibilityMessage(eligibility?.reason, bootstrapStatusReady)}
                    onClick={() => openCredentialForm([host.host_id], "single")}
                  >
                    {buttonLabel}
                  </Button>
                ) : null}
              </div>
            </div>
          );
        })}
      </div>
  );
}


export function BootstrapFeedback({ feedback }: { feedback: Feedback }) {
  return (
<div
          className={feedback.tone === "success"
            ? "rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-100"
            : feedback.tone === "pending"
              ? "rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"
              : "rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"}
          role={feedback.tone === "error" ? "alert" : "status"}
        >
          {feedback.message}
        </div>
  );
}
