import { Suspense } from "react";
import { EmailConfirmCard } from "@/components/auth/auth-card";

export default function EmailConfirmPage() {
  return (
    <Suspense fallback={<EmailConfirmFallback />}>
      <EmailConfirmCard />
    </Suspense>
  );
}

function EmailConfirmFallback() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <section className="w-full max-w-md rounded-lg border border-border bg-card p-6 text-card-foreground shadow-sm">
        <div className="space-y-2">
          <div className="text-lg font-semibold">メールアドレス変更確認</div>
          <p className="text-sm text-muted-foreground">確認情報を読み込んでいます。</p>
        </div>
      </section>
    </main>
  );
}
