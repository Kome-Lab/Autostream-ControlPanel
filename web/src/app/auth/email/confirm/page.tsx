import { EmailConfirmCard } from "@/components/auth/auth-card";

export default function EmailConfirmPage({ searchParams }: { searchParams?: { token?: string | string[] } }) {
  const token = Array.isArray(searchParams?.token) ? searchParams?.token[0] : searchParams?.token;
  return <EmailConfirmCard token={token} />;
}
