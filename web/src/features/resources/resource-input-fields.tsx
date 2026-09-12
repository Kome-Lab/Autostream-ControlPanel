"use client";

import { type ReactNode, useMemo } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { type SelectOption } from "./resource-form-types";
import { toggleListValue } from "./resource-values";
import { permissionGroupLabel } from "./resource-permissions";

export function Field({ label, description, children }: { label: string; description?: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <div className="text-sm font-medium">{label}</div>
      {children}
      {description ? <p className="text-xs text-muted-foreground">{description}</p> : null}
    </div>
  );
}

export function TextField({
  label,
  value,
  onChange,
  description,
  placeholder,
  type = "text",
  required,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  description?: string;
  placeholder?: string;
  type?: string;
  required?: boolean;
}) {
  return (
    <Field label={label} description={description}>
      <Input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} type={type} required={required} />
    </Field>
  );
}

export function NumberField({
  label,
  value,
  onChange,
  min,
  required,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  min?: number;
  required?: boolean;
}) {
  return (
    <Field label={label}>
      <Input value={value} onChange={(event) => onChange(event.target.value)} type="number" min={min} required={required} />
    </Field>
  );
}

export function SelectField({ label, value, onChange, options, disabled = false }: { label: string; value: string; onChange: (value: string) => void; options: SelectOption[]; disabled?: boolean }) {
  const selected = options.find((option) => option.value === value);
  return (
    <Field label={label}>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger className="w-full">
          <span className="min-w-0 truncate">{selected?.label || <SelectValue />}</span>
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value} textValue={option.label}>
              <span className="min-w-0 truncate">{option.label}</span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selected?.description ? <p className="text-xs text-muted-foreground">{selected.description}</p> : null}
    </Field>
  );
}

export function SwitchField({ label, checked, onCheckedChange }: { label: string; checked: boolean; onCheckedChange: (checked: boolean) => void }) {
  return (
    <label className="flex items-center justify-between gap-3 rounded-md border bg-background px-3 py-2 text-sm">
      <span className="font-medium">{label}</span>
      <Switch checked={checked} onCheckedChange={(value) => onCheckedChange(Boolean(value))} />
    </label>
  );
}

export function CheckboxList({
  label,
  values,
  onChange,
  items,
  emptyText = "選択肢がありません。",
  disabled = false,
}: {
  label: string;
  values: string[];
  onChange: (values: string[]) => void;
  items: SelectOption[];
  emptyText?: string;
  disabled?: boolean;
}) {
  return (
    <Field label={label}>
      {items.length === 0 ? (
        <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">{emptyText}</div>
      ) : (
        <div className="grid gap-2 md:grid-cols-2">
          {items.map((item) => (
            <label key={item.value} className="flex min-w-0 items-start gap-2 rounded-md border bg-background p-3 text-sm">
              <Checkbox disabled={disabled} checked={values.includes(item.value)} onCheckedChange={(checked) => onChange(toggleListValue(values, item.value, Boolean(checked)))} />
              <span className="min-w-0">
                <span className="block break-words font-medium">{item.label}</span>
                {item.description ? <span className="block text-xs text-muted-foreground">{item.description}</span> : null}
              </span>
            </label>
          ))}
        </div>
      )}
    </Field>
  );
}

export function GroupedCheckboxList(props: { label: string; values: string[]; onChange: (values: string[]) => void; items: SelectOption[]; emptyText?: string }) {
	const groups = useMemo(() => {
		const grouped = new Map<string, SelectOption[]>();
		for (const item of props.items) {
			const group = item.group || permissionGroupLabel(item.value === "*" ? "all" : item.value.split(".")[0] || "other");
			grouped.set(group, [...(grouped.get(group) || []), item]);
		}
		return [...grouped.entries()];
  }, [props.items]);

  return (
    <Field label={props.label}>
      {props.items.length === 0 ? (
        <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">{props.emptyText || "選択肢がありません。"}</div>
      ) : (
        <div className="space-y-3">
          {groups.map(([group, items]) => (
            <div key={group} className="space-y-2">
              <div className="text-xs font-medium uppercase text-muted-foreground">{group}</div>
              <div className="grid gap-2 md:grid-cols-2">
                {items.map((item) => (
                  <label key={item.value} className="flex min-w-0 items-start gap-2 rounded-md border bg-background p-3 text-sm">
                    <Checkbox checked={props.values.includes(item.value)} onCheckedChange={(checked) => props.onChange(toggleListValue(props.values, item.value, Boolean(checked)))} />
                    <span className="min-w-0">
                      <span className="block break-words font-medium">{item.label}</span>
                      {item.description ? <span className="block text-xs text-muted-foreground">{item.description}</span> : null}
                    </span>
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </Field>
  );
}

export function FormActions({ disabled, label = "作成" }: { disabled: boolean; label?: string }) {
  return (
    <div className="flex justify-end">
      <Button type="submit" size="sm" disabled={disabled}>
        {label}
      </Button>
    </div>
  );
}
