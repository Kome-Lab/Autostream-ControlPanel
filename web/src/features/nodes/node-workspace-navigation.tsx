"use client";

import Link from "next/link";
import { useI18n } from "@/components/admin/i18n-provider";

export function NodeWorkspaceNavigation({ active, canRegister, canOperate }: {
  active: "workers" | "registration" | "registered"; canRegister: boolean; canOperate: boolean;
}) {
  const { locale } = useI18n();
  const ja = locale === "ja";
  const entries = [
    ...(canOperate ? [{ key: "workers", href: "/admin/workers/", label: ja ? "稼働・担当" : "Operations / assignments" }] : []),
    ...(canRegister ? [
      { key: "registration", href: "/admin/nodes/", label: ja ? "Node登録" : "Register node" },
      { key: "registered", href: "/admin/registered-nodes/", label: ja ? "登録済みNode" : "Registered nodes" },
    ] : []),
  ];
  return <nav aria-label={ja ? "Nodeワークスペース" : "Node workspace"} className="flex flex-wrap gap-x-5 gap-y-2 border-b text-sm">
    {entries.map((entry) => <Link key={entry.key} href={entry.href} aria-current={active === entry.key ? "page" : undefined}
      className="min-h-11 py-3 text-primary underline-offset-4 hover:underline aria-[current=page]:border-b-2 aria-[current=page]:border-primary">{entry.label}</Link>)}
  </nav>;
}
