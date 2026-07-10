import Image from "next/image";
import { cn } from "@/lib/utils";

type AccountAvatarProps = {
  name: string;
  src?: string;
  alt?: string;
  className?: string;
  sizes?: string;
};

export function AccountAvatar({ name, src, alt = "", className, sizes = "96px" }: AccountAvatarProps) {
  return (
    <span
      data-slot="account-avatar"
      className={cn("relative flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-full border bg-primary/12 text-sm font-semibold text-primary", className)}
    >
      {src ? <Image src={src} alt={alt} fill sizes={sizes} unoptimized className="object-cover" /> : <span aria-hidden="true">{userInitial(name)}</span>}
    </span>
  );
}

function userInitial(name: string) {
  return String(name || "?").trim().charAt(0).toLocaleUpperCase() || "?";
}
