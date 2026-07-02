import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

export function MetricCard({
  title,
  value,
  detail,
  tone = "default",
}: {
  title: string;
  value: string | number;
  detail: string;
  tone?: "default" | "ok" | "warning" | "danger";
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium text-muted-foreground">{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <div
          className={cn(
            "text-3xl font-semibold tracking-normal",
            tone === "ok" && "text-emerald-700 dark:text-emerald-300",
            tone === "warning" && "text-amber-700 dark:text-amber-300",
            tone === "danger" && "text-red-700 dark:text-red-300",
          )}
        >
          {value}
        </div>
        <p className="mt-1 text-sm text-muted-foreground">{detail}</p>
      </CardContent>
    </Card>
  );
}
