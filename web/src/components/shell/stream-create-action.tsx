"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const streamCreateHref = "/admin/streams/#create-stream";

export function StreamCreateAction({
  pathname,
  mobile = false,
  className,
  onNavigateAfterClose,
}: {
  pathname: string;
  mobile?: boolean;
  className?: string;
  onNavigateAfterClose?: (navigate: () => void) => void;
}) {
  const router = useRouter();
  const { t } = useI18n();
  const sameRoute = pathname.startsWith("/admin/streams");
  const navigate = () => {
    if (sameRoute) {
      window.location.hash = "create-stream";
      return;
    }
    router.push(streamCreateHref);
  };

  return (
    <Link
      href={streamCreateHref}
      className={cn(buttonVariants({ size: "sm" }), mobile && "min-h-11 w-full min-w-0 whitespace-normal text-center", className)}
      onClick={(event) => {
        if (mobile && onNavigateAfterClose) {
          event.preventDefault();
          onNavigateAfterClose(navigate);
          return;
        }
        if (!sameRoute) return;
        event.preventDefault();
        navigate();
      }}
    >
      <Plus className="size-4" aria-hidden="true" />
      {t("createStream")}
    </Link>
  );
}
