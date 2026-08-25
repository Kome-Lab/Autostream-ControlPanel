"use client";

import Link from "next/link";
import { Plus } from "lucide-react";
import { buttonVariants } from "@/components/ui/button";
import { SheetClose } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";

export function StreamCreateAction({ pathname, mobile = false, className }: { pathname: string; mobile?: boolean; className?: string }) {
  const link = (
    <Link
      href="/admin/streams/#create-stream"
      className={cn(buttonVariants({ size: "sm" }), mobile && "min-h-11 w-full", className)}
      onClick={(event) => {
        if (pathname.startsWith("/admin/streams")) {
          event.preventDefault();
          window.location.hash = "create-stream";
        }
      }}
    >
      <Plus className="size-4" aria-hidden="true" />
      配信枠を作成
    </Link>
  );

  return mobile ? <SheetClose asChild>{link}</SheetClose> : link;
}
