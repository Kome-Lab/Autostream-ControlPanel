import Link from "next/link";
import { Button } from "@/components/ui/button";

export default function HomePage() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="w-full max-w-md space-y-5 rounded-lg border bg-card p-6 shadow-sm">
        <div>
          <p className="text-sm text-muted-foreground">AutoStream</p>
          <h1 className="text-2xl font-semibold">Control Panel</h1>
        </div>
        <p className="text-sm text-muted-foreground">
          サーバー運用時のルートアクセスは、初期作成・ログイン・管理画面へ自動的に振り分けられます。
        </p>
        <Button asChild className="w-full">
          <Link href="/admin/">管理画面を開く</Link>
        </Button>
      </div>
    </main>
  );
}
