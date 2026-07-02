"use client";

import { useEffect } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";

export function RedirectPage({ to }: { to: string }) {
  useEffect(() => {
    window.location.replace(to);
  }, [to]);

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <Button asChild>
        <Link href={to}>移動する</Link>
      </Button>
    </main>
  );
}
