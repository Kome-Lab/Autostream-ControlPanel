"use client";

import { type ResourceDefinition } from "@/features/resources/resource-config";
import { type SubmitResource, type ResourceRow } from "./resource-form-types";
import { EncoderProfileForm, DiscordConfigForm, DiscordTargetPresetForm, YouTubeOutputForm } from "./resource-stream-forms";
import { CaptionProfileForm } from "./resource-caption-form";
import { OverlayProfileForm } from "./resource-overlay-form";
import { ArchiveProfileForm, DriveDestinationForm } from "./resource-archive-forms";
import { OAuthProviderForm, OAuthAccountRenameForm, OAuthAccountConnectForm } from "./resource-oauth-forms";
import { UserForm, RoleForm } from "./resource-access-forms";
import { NotificationChannelForm } from "./resource-notification-form";

export function ResourceFormFields({ resource, disabled, submit, initial, submitLabel }: { resource: ResourceDefinition; disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  switch (resource.form) {
    case "encoder-profile":
      return <EncoderProfileForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "discord-config":
      return <DiscordConfigForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "discord-target-preset":
      return <DiscordTargetPresetForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "youtube-output":
      return <YouTubeOutputForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "caption-profile":
      return <CaptionProfileForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "overlay-profile":
      return <OverlayProfileForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "archive-profile":
      return <ArchiveProfileForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "drive-destination":
      return <DriveDestinationForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "oauth-provider":
      return <OAuthProviderForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "oauth-account-connect":
      return initial ? <OAuthAccountRenameForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} /> : <OAuthAccountConnectForm disabled={disabled} submit={submit} />;
    case "user":
      return <UserForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "role":
      return <RoleForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "notification-channel":
      return <NotificationChannelForm disabled={disabled} submit={submit} initial={initial} submitLabel={submitLabel} />;
    case "security-settings":
      return null;
    default:
      return <p className="text-sm text-muted-foreground">このリソースは一覧確認のみ対応しています。</p>;
  }
}
