import { Suspense } from "react";
import { LoginCard } from "@/components/auth/auth-card";

export default function LoginPage() {
  return (
    <Suspense fallback={<LoginFallback />}>
      <LoginCard />
    </Suspense>
  );
}

function LoginFallback() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <section className="w-full max-w-md rounded-md border border-border bg-card p-6 text-card-foreground shadow-sm">
        <div className="space-y-2">
          <div className="text-lg font-semibold">ログイン</div>
          <p className="text-sm text-muted-foreground">ログイン情報を読み込んでいます。</p>
        </div>
      </section>
    </main>
  );
}
