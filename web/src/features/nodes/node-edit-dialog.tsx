import type { Dispatch, SetStateAction } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useI18n } from "@/components/admin/i18n-provider";
import type { WorkerNode } from "@/types/domain";
import { type NodeEditForm, nodeIdentity, editNodeApiURL } from "./node-registration-model";



export function NodeEditDialog({
  editingNode,
  setEditingNode,
  editForm,
  setEditForm,
  editingPullHostAgent,
  allowed,
  editFormValid,
  updatePending,
  submitEditNode,
  t,
}: {
  editingNode: WorkerNode | null;
  setEditingNode: (node: WorkerNode | null) => void;
  editForm: NodeEditForm;
  setEditForm: Dispatch<SetStateAction<NodeEditForm>>;
  editingPullHostAgent: boolean;
  allowed: boolean;
  editFormValid: boolean;
  updatePending: boolean;
  submitEditNode: () => void;
  t: ReturnType<typeof useI18n>["t"]
}) {
  return (
<Dialog open={Boolean(editingNode)} onOpenChange={(open) => (!open ? setEditingNode(null) : undefined)}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Nodeを編集</DialogTitle>
            <DialogDescription>Node IDとNode typeは変更できません。接続先を変えた場合は必要に応じてNode Agent側の設定も更新してください。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <label className="text-sm font-medium">Node ID</label>
              <Input value={editingNode ? nodeIdentity(editingNode) : ""} disabled />
            </div>
            <div className="grid gap-2">
              <label className="text-sm font-medium">{t("name")}</label>
              <Input value={editForm.service_name} onChange={(event) => setEditForm((current) => ({ ...current, service_name: event.target.value }))} />
            </div>
            {!editingPullHostAgent ? (
              <>
                <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_120px]">
                  <div className="grid gap-2">
                    <label className="text-sm font-medium">Host / FQDN / IP</label>
                    <Input value={editForm.host} onChange={(event) => setEditForm((current) => ({ ...current, host: event.target.value }))} />
                  </div>
                  <div className="grid gap-2">
                    <label className="text-sm font-medium">Port</label>
                    <Input type="number" inputMode="numeric" min={1024} max={65535} value={editForm.port} onChange={(event) => setEditForm((current) => ({ ...current, port: event.target.value }))} />
                  </div>
                </div>
                <label className="flex items-center gap-2 text-sm">
                  <Checkbox checked={editForm.ssl_enabled} onCheckedChange={(value) => setEditForm((current) => ({ ...current, ssl_enabled: value === true }))} />
                  SSLを有効化してHTTPSを使用
                </label>
                <div className="rounded-md border bg-muted/40 p-3 text-sm">
                  <div className="font-medium">Node Agent API URL</div>
                  <div className="mt-1 break-all text-muted-foreground">{editNodeApiURL(editForm) || "Hostと1024〜65535のPortを入力してください"}</div>
                </div>
              </>
            ) : (
              <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
                Host Pull Agentは受信endpointを持ちません。Execution Host IDとtransport ownershipは別の移行操作で管理されます。
              </div>
            )}
            <div className="grid gap-2">
              <label className="text-sm font-medium">説明</label>
              <Textarea value={editForm.description} onChange={(event) => setEditForm((current) => ({ ...current, description: event.target.value }))} rows={3} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditingNode(null)}>
              {t("cancel")}
            </Button>
            <Button onClick={submitEditNode} disabled={!allowed || !editFormValid || updatePending}>
              {updatePending ? "保存中" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
  );
}
