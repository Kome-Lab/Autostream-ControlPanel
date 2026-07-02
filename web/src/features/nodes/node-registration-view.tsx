"use client";

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Check, Copy, KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { apiPost } from "@/lib/api/client";
import { hasAnyPermission } from "@/lib/auth/permissions";
import { useCurrentUser } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { NodeRegistrationResponse } from "@/types/domain";

const nodeTypes = [
  { value: "worker", label: "Worker" },
  { value: "encoder_recorder", label: "Encoder / Recorder" },
  { value: "discord_bot", label: "Discord Bot" },
  { value: "observability", label: "Observability" },
];

export function NodeRegistrationView() {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const [nodeType, setNodeType] = useState("worker");
  const [nodeID, setNodeID] = useState("worker-tokyo-01");
  const [name, setName] = useState("東京本社 Worker 01");
  const [publicURL, setPublicURL] = useState("https://worker-tokyo-01.example.jp");
  const [version, setVersion] = useState("1.2.0");
  const [capabilities, setCapabilities] = useState("overlay_events\ncaption_events\narchive_upload");
  const [allowRuntimeSecrets, setAllowRuntimeSecrets] = useState(false);
  const [allowRemediation, setAllowRemediation] = useState(false);
  const [copied, setCopied] = useState("");

  const allowed = hasAnyPermission(currentUser.data, ["api_tokens.create"]);
  const createToken = useMutation({
    mutationFn: () =>
      apiPost<NodeRegistrationResponse>("/nodes/registration-tokens", {
        node_type: nodeType,
        node_id: nodeID,
        name,
        public_url: publicURL,
        version,
        capabilities: capabilityMap(capabilities),
        allow_runtime_secrets: allowRuntimeSecrets,
        allow_remediation: allowRemediation,
      }),
  });

  const copyValue = async (key: string, value?: string) => {
    if (!value) return;
    await navigator.clipboard.writeText(value);
    setCopied(key);
    window.setTimeout(() => setCopied(""), 1200);
  };

  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(360px,0.9fr)_minmax(0,1.1fr)]">
      <Card className="min-w-0">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="size-5" />
            {t("nodeRegistration")}
          </CardTitle>
          <CardDescription>Pterodactyl Panel と Wings のように、Control Panel でNodeを事前登録してからNode側で接続します。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("nodeType")}</label>
            <Select value={nodeType} onValueChange={setNodeType}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {nodeTypes.map((type) => (
                  <SelectItem key={type.value} value={type.value}>
                    {type.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("nodeId")}</label>
            <Input value={nodeID} onChange={(event) => setNodeID(event.target.value)} />
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("name")}</label>
            <Input value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("publicUrl")}</label>
            <Input value={publicURL} onChange={(event) => setPublicURL(event.target.value)} />
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("version")}</label>
            <Input value={version} onChange={(event) => setVersion(event.target.value)} />
          </div>
          <div className="grid gap-2">
            <label className="text-sm font-medium">{t("capabilities")}</label>
            <Textarea value={capabilities} onChange={(event) => setCapabilities(event.target.value)} rows={5} />
          </div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={allowRuntimeSecrets} onCheckedChange={(value) => setAllowRuntimeSecrets(value === true)} />
            {t("runtimeSecrets")}
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={allowRemediation} onCheckedChange={(value) => setAllowRemediation(value === true)} />
            {t("remediation")}
          </label>
          <Button className="w-full" disabled={!allowed || createToken.isPending} onClick={() => createToken.mutate()}>
            {t("createToken")}
          </Button>
          {!allowed ? <p className="text-sm text-red-600">{t("roleLimited")}</p> : null}
        </CardContent>
      </Card>

      <Card className="min-w-0">
        <CardHeader>
          <CardTitle>{t("oneTimeToken")}</CardTitle>
          <CardDescription>この値は作成直後だけ表示されます。Node側へ設定したら保管せず画面を閉じてください。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {createToken.data ? (
            <>
              <SecretBlock label="Token" value={createToken.data.token} copied={copied === "token"} onCopy={() => copyValue("token", createToken.data?.token)} />
              <SecretBlock
                label={t("configureCommand")}
                value={createToken.data.configure_command}
                copied={copied === "command"}
                onCopy={() => copyValue("command", createToken.data?.configure_command)}
              />
              <div className="rounded-md border bg-muted/40 p-3 text-sm">
                <div className="font-medium">Scopes</div>
                <div className="mt-2 flex flex-wrap gap-2">
                  {createToken.data.scopes.map((scope) => (
                    <span key={scope} className="rounded-md bg-background px-2 py-1 text-xs">
                      {scope}
                    </span>
                  ))}
                </div>
              </div>
            </>
          ) : (
            <div className="rounded-md border border-dashed p-8 text-center text-sm text-muted-foreground">
              Node情報を入力して登録トークンを作成すると、ここに一度だけ表示されます。
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function SecretBlock({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <label className="text-sm font-medium">{label}</label>
        <Button variant="outline" size="sm" onClick={onCopy}>
          {copied ? <Check /> : <Copy />}
          {copied ? "コピー済み" : "コピー"}
        </Button>
      </div>
      <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted p-3 text-xs leading-relaxed">{value}</pre>
    </div>
  );
}

function capabilityMap(value: string) {
  return Object.fromEntries(
    value
      .split(/\r?\n|,/)
      .map((item) => item.trim())
      .filter(Boolean)
      .map((item) => [item, true]),
  );
}
