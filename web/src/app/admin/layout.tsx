import { AdminShell } from "@/components/admin/admin-shell";
import { GoogleAnalytics } from "@/components/admin/google-analytics";

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <GoogleAnalytics />
      <AdminShell>{children}</AdminShell>
    </>
  );
}
