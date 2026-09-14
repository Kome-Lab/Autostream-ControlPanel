"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";

export function SecretBlock({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
  const uiText = useUICopy();
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <label className="text-sm font-medium">{label}</label>
        <Button variant="outline" size="sm" onClick={onCopy}>
          {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
          {copied ? uiText("コピー済み") : uiText("コピー")}
        </Button>
      </div>
      <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted p-3 text-xs leading-relaxed">{value}</pre>
    </div>
  );
}
