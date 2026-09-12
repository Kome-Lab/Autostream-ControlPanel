"use client";

import { useMemo, useState } from "react";
import { Switch } from "@/components/ui/switch";
import { useCurrentUser } from "@/features/queries";
import { hasPermission } from "@/lib/auth/permissions";
import { type SubmitResource, type ResourceRow } from "./resource-form-types";
import { useResourceOptions, useResourceRows } from "./resource-form-queries";
import { stringListSetting, rowValue, rowString, resourceRowID } from "./resource-values";
import { TextField, CheckboxList, FormActions, GroupedCheckboxList } from "./resource-input-fields";
import { permissionOptionFromRow } from "./resource-permissions";

export function UserForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const editing = Boolean(initial);
  const currentUser = useCurrentUser();
  const roles = useResourceOptions("/roles", ["id"], ["name", "id"], ["permissions"]);
  const initialRoleIDs = stringListSetting(rowValue(row, ["role_ids"]));
  const initialRoleNames = stringListSetting(rowValue(row, ["roles"]));
  const [username, setUsername] = useState(() => rowString(row, ["username"]) || "operator");
  const [email, setEmail] = useState(() => rowString(row, ["email"]) || "operator@example.jp");
  const [temporaryPassword, setTemporaryPassword] = useState("");
  const [roleIDs, setRoleIDs] = useState<string[]>(() => initialRoleIDs);
  const [rolesChanged, setRolesChanged] = useState(false);
  const [sendWelcomeEmail, setSendWelcomeEmail] = useState(false);
  const canAssignRoles = hasPermission(currentUser.data, "roles.assign");
  const editingSelf = editing && resourceRowID(row) === currentUser.data?.user.id;
  const roleSelectionUnavailable = editing && initialRoleNames.length > 0 && initialRoleIDs.length === 0;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        if (editing) {
          const payload: Record<string, unknown> = { username: username.trim(), email: email.trim() };
          if (rolesChanged && canAssignRoles && !editingSelf && !roleSelectionUnavailable) payload.role_ids = roleIDs;
          submit(payload);
          return;
        }
        const payload: Record<string, unknown> = { username: username.trim(), email: email.trim(), temporary_password: temporaryPassword, send_welcome_email: sendWelcomeEmail };
        if (canAssignRoles && roleIDs.length > 0) payload.role_ids = roleIDs;
        submit(payload, { onSensitiveDispatched: () => setTemporaryPassword("") });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="ユーザー名" value={username} onChange={setUsername} required />
        <TextField label="メールアドレス" value={email} onChange={setEmail} type="email" description="登録完了メールと本人確認用の連絡先です。" required />
        {!editing ? <TextField label="初期パスワード" value={temporaryPassword} onChange={setTemporaryPassword} type="password" required description="ログイン後に変更してもらう一時パスワードです。" /> : null}
      </div>
      {!editing ? (
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={sendWelcomeEmail} onCheckedChange={setSendWelcomeEmail} />
          登録完了メールを送る
        </label>
      ) : null}
      <CheckboxList
        label="付与するロール"
        values={roleIDs}
        onChange={(values) => {
          setRoleIDs(values);
          setRolesChanged(true);
        }}
        items={roles}
        emptyText="ロールがありません。"
        disabled={!canAssignRoles || editingSelf || roleSelectionUnavailable}
      />
      {!canAssignRoles ? <p className="text-xs text-muted-foreground">ロールの変更には「ロールを割り当て」権限が必要です。ユーザー名とメールアドレスは更新できます。</p> : null}
      {editingSelf ? <p className="text-xs text-muted-foreground">ログイン中のユーザー自身のロールは変更できません。ユーザー名とメールアドレスは更新できます。</p> : null}
      {roleSelectionUnavailable ? <p className="text-xs text-muted-foreground">既存ロール: {initialRoleNames.join("、")}。ロールIDを取得できないため、ロール変更は無効です。</p> : null}
      <FormActions label={submitLabel} disabled={disabled || username.trim() === "" || email.trim() === "" || (!editing && temporaryPassword === "")} />
    </form>
  );
}

export function RoleForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const permissionRows = useResourceRows("/permissions");
  const permissionOptions = useMemo(() => permissionRows.map(permissionOptionFromRow).filter((option) => option.value), [permissionRows]);
  const [name, setName] = useState(() => rowString(row, ["name"]) || "operator");
  const [permissions, setPermissions] = useState<string[]>(() => stringListSetting(rowValue(row, ["permissions"])).length ? stringListSetting(rowValue(row, ["permissions"])) : ["streams.read", "streams.start", "streams.stop"]);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({ name, permissions });
      }}
    >
      <TextField label="ロール名" value={name} onChange={setName} required />
      <GroupedCheckboxList label="許可する操作" values={permissions} onChange={setPermissions} items={permissionOptions} emptyText="権限一覧を取得できませんでした。" />
      <FormActions label={submitLabel} disabled={disabled || permissions.length === 0} />
    </form>
  );
}
