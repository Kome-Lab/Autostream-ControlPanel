import { notificationChannelTestFeedback } from "@/lib/notification-channel";
import { japaneseCopy, type UICopy } from "./copy";
import { presentationKeys } from "./presentation-keys";
import { oauthAccountDisplayName, oauthProviderTypeLabel } from "@/lib/oauth-account";
import { streamServiceAssignmentOption } from "@/lib/stream-create";

// Only callers returning fixed, already-safe presenter text use this adapter.
// API errors still enter their existing sanitizer, never the copy dictionary.
// Unrecognized values and parameters are preserved; no user fields are passed here.
export function fixedPresentationText(text: string, uiText: UICopy = japaneseCopy): string {
  const exact = presentationKeys.find(key => key === text);
  if (exact) return uiText(exact);
  const prefix = presentationKeys.filter(key => key.endsWith("。")).find(key => text.startsWith(key+" ") || text.startsWith(key+"\n"));
  if (prefix) return uiText(prefix)+text.slice(prefix.length).replace(/^ 詳細: /,uiText(" 詳細: "));
  return text;
}
export function oauthAccountName(account: Record<string, unknown>, uiText: UICopy = japaneseCopy) {
  const original=oauthAccountDisplayName(account);
  if ([account.account_label,account.display_name].some(value=>typeof value==="string"&&value.trim()===original)) return original;
  const provider=typeof account.provider_type==="string"?oauthProviderTypeLabel(account.provider_type):"OAuth";
  const providerName=typeof account.provider_name==="string"?account.provider_name.trim():"";
  const suffix=" (表示名未設定)";
  let value=original.endsWith(suffix)?original.slice(0,-suffix.length)+" ("+uiText("表示名未設定")+")":original;
  if ((!providerName||!original.startsWith(providerName+" (")) && value.startsWith(provider+"アカウント")) value=uiText("{0}アカウント",provider)+value.slice((provider+"アカウント").length);
  return value;
}
export function serviceAssignmentPresentation(service: Parameters<typeof streamServiceAssignmentOption>[0], editingStreamID?: string, uiText: UICopy = japaneseCopy) {
  const option=streamServiceAssignmentOption(service,editingStreamID);
  return {...option,label:option.disabled?uiText("{0}（別の配信枠で使用中）",service.label):option.label,
    description:option.description?uiText("現在の配信枠を停止し、割り当てを解除してから選択してください。"):undefined};
}

// The existing formatter sanitizes targets/errors first. Only its fixed status
// words and fixed safe messages enter copy; provider payloads never do.
export function notificationFeedback(response: unknown, uiText: UICopy = japaneseCopy) {
  const safe = notificationChannelTestFeedback(response);
  const prefix = safe.message.startsWith("テスト送信に成功しました。") ? "テスト送信に成功しました。"
    : safe.message.startsWith("テスト送信に失敗しました。") ? "テスト送信に失敗しました。" : null;
  if (!prefix) return { ...safe, message: fixedPresentationText(safe.message, uiText) };
  const details = safe.message.slice(prefix.length).trim().split(" | ").map(detail => detail.split(" / ").map((part, index) => {
    if (index === 0 && (part === "成功" || part === "失敗")) return uiText(part);
    if (index === 1 && part.startsWith("送信先 ")) return uiText("送信先 {0}", part.slice(4));
    return fixedPresentationText(part, uiText);
  }).join(" / ")).join(" | ");
  return { ...safe, message: `${uiText(prefix)} ${details}` };
}
