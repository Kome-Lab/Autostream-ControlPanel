"use client";

import Image from "next/image";
import { useState } from "react";
import type { CoverCandidate } from "./control-platform";

type Props = {
  candidates: readonly CoverCandidate[];
  locale: "ja" | "en";
  imageURL: (candidate: CoverCandidate) => string;
  startVariantID?: string;
  selectedVariantID?: string;
  appliedVariantID?: string;
  actions?: (candidate: CoverCandidate) => React.ReactNode;
};

export function CoverCandidateGrid({ candidates, locale, imageURL, startVariantID, selectedVariantID, appliedVariantID, actions }: Props) {
  return <div data-cover-candidate-grid className="grid min-w-0 gap-3" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 16rem), 1fr))" }}>
    {candidates.map((candidate) => <article key={candidate.variant_id} data-cover-candidate={candidate.variant_id} className="min-w-0 space-y-3 rounded-lg border bg-background p-3">
      <CoverCandidatePreview key={imageURL(candidate)} name={candidate.name} url={imageURL(candidate)} locale={locale} />
      <details className="min-w-0 text-sm" data-cover-candidate-name>
        <summary className="min-h-10 cursor-pointer rounded px-1 py-2 [overflow-wrap:anywhere] focus-visible:outline-2 focus-visible:outline-ring">
          <span className="line-clamp-2">{candidate.name}</span>
          <span className="text-xs text-muted-foreground">{locale === "ja" ? "ファイル名をすべて表示" : "Show full file name"}</span>
        </summary>
        <p className="mt-1 rounded bg-muted/40 p-2 [overflow-wrap:anywhere]">{candidate.name}</p>
      </details>
      <div className="flex min-h-6 flex-wrap gap-1 text-xs" aria-live="polite">
        {candidate.variant_id === startVariantID ? <span className="rounded bg-primary/10 px-2 py-1 font-medium text-primary">{locale === "ja" ? "開始時の画像" : "Start image"}</span> : null}
        {candidate.variant_id === selectedVariantID ? <span className="rounded bg-muted px-2 py-1">{locale === "ja" ? "切替対象" : "Selected for switching"}</span> : null}
        {candidate.variant_id === appliedVariantID ? <span className="rounded bg-muted px-2 py-1">{locale === "ja" ? "Encoderで反映確認済み" : "Confirmed by Encoder"}</span> : null}
      </div>
      {actions ? <div data-cover-candidate-actions className="grid min-w-0 gap-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 9rem), 1fr))" }}>{actions(candidate)}</div> : null}
    </article>)}
  </div>;
}

function CoverCandidatePreview({ name, url, locale }: { name: string; url: string; locale: "ja" | "en" }) {
  const [failed, setFailed] = useState(false);
  return <div className="relative aspect-video min-w-0 overflow-hidden rounded-md border bg-muted/40">
    {url && !failed ? <Image unoptimized loading="eager" width={1920} height={1080} src={url} alt={locale === "ja" ? `${name} の蓋画像プレビュー` : `Cover preview: ${name}`} onError={() => setFailed(true)} className="absolute inset-0 size-full object-contain" />
      : <p role="status" className="absolute inset-0 grid place-content-center p-3 text-center text-xs text-muted-foreground">{locale === "ja" ? "プレビューを読み込めません。候補の設定は保持しています。" : "The preview is unavailable. The candidate is retained."}</p>}
  </div>;
}
