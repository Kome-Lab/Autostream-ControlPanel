"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { type ReactNode, useMemo } from "react";
import { Field as LabeledField, FieldGroup } from "@/components/forms/field";
import { FormFooter } from "@/components/forms/form-footer";
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
    <FieldGroup title={label} description={description}><div className="min-w-0 sm:col-span-2">{children}</div></FieldGroup>
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
    <LabeledField label={label} description={description} required={required}>
      <Input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} type={type} required={required} />
    </LabeledField>
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
    <LabeledField label={label} required={required}>
      <Input value={value} onChange={(event) => onChange(event.target.value)} type="number" min={min} required={required} />
    </LabeledField>
  );
}

export function SelectField({ label, value, onChange, options, disabled = false }: { label: string; value: string; onChange: (value: string) => void; options: SelectOption[]; disabled?: boolean }) {
  const selected = options.find((option) => option.value === value);
  return (
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <LabeledField label={label} description={selected?.description}>
        <SelectTrigger className="w-full">
          <span className="min-w-0 truncate">{selected?.label || <SelectValue />}</span>
        </SelectTrigger>
        </LabeledField>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value} textValue={option.label}>
              <span className="min-w-0 truncate">{option.label}</span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
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
  emptyText,
  disabled = false,
}: {
  label: string;
  values: string[];
  onChange: (values: string[]) => void;
  items: SelectOption[];
  emptyText?: string;
  disabled?: boolean;
}) {
  const uiText = useUICopy();
  return (
    <Field label={label}>
      {items.length === 0 ? (
        <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">{emptyText ?? uiText("選択肢がありません。")}</div>
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
  const uiText = useUICopy();
	const groups = useMemo(() => {
		const grouped = new Map<string, SelectOption[]>();
		for (const item of props.items) {
			const group = item.group || permissionGroupLabel(item.value === "*" ? "all" : item.value.split(".")[0] || "other", uiText);
			grouped.set(group, [...(grouped.get(group) || []), item]);
		}
		return [...grouped.entries()];
  }, [props.items, uiText]);

  return (
    <Field label={props.label}>
      {props.items.length === 0 ? (
        <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">{props.emptyText || uiText("選択肢がありません。")}</div>
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

export function FormActions({ disabled, label }: { disabled: boolean; label?: string }) {
  const uiText = useUICopy();
  return (
    <FormFooter>
      <Button type="submit" size="sm" disabled={disabled}>
        {label ?? uiText("作成")}
      </Button>
    </FormFooter>
  );
}
