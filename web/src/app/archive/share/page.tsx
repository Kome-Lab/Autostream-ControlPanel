import { Suspense } from "react";
import { ArchiveSharePlayerView } from "@/features/archive/archive-share-player-view";
import { Skeleton } from "@/components/ui/skeleton";

export default function ArchiveSharePage() {
  return (
    <Suspense fallback={<ArchiveShareFallback />}>
      <ArchiveSharePlayerView />
    </Suspense>
  );
}

function ArchiveShareFallback() {
  return (
    <main className="min-h-screen bg-background px-4 py-6 text-foreground md:px-8">
      <div className="mx-auto max-w-6xl">
        <Skeleton className="min-h-[50vh] w-full" />
      </div>
    </main>
  );
}
