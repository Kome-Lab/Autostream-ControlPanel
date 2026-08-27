"use client";

import {
  createElement,
  useEffect,
  useRef,
  useState,
  type ComponentProps,
  type ReactElement,
  type ReactNode,
} from "react";

import { Button } from "@/components/ui/button";
import type { OneTimeSecretSnapshot } from "@/lib/foundation/secrets/contracts";
import type { TranslationKey, TranslationValues } from "@/lib/i18n";

export type OneTimeSecretRevealProps = Readonly<{
  snapshot: OneTimeSecretSnapshot;
  translate: (
    key: TranslationKey,
    values?: TranslationValues,
  ) => string;
  renderRevealedContent: () => ReactElement;
  canCopy?: boolean;
  onRevealIntent: () => void;
  onConcealIntent: () => void;
  onCopyIntent?: () => void;
  onAcknowledgeIntent: () => void;
  onDismissIntent: () => void;
  onUnmountIntent: () => void;
}>;

export function OneTimeSecretReveal({
  snapshot,
  translate,
  renderRevealedContent,
  canCopy = false,
  onRevealIntent,
  onConcealIntent,
  onCopyIntent,
  onAcknowledgeIntent,
  onDismissIntent,
  onUnmountIntent,
}: OneTimeSecretRevealProps): ReactNode {
  const unmountIntentRef = useRef(onUnmountIntent);
  const unmountCalledRef = useRef(false);
  const effectGenerationRef = useRef(0);
  const [focusIntent, setFocusIntent] = useState<Readonly<{
    generation: number;
    target: "concealed" | "revealed";
  }> | undefined>();

  useEffect(() => {
    unmountIntentRef.current = onUnmountIntent;
  }, [onUnmountIntent]);

  useEffect(() => {
    effectGenerationRef.current += 1;
    const effectGeneration = effectGenerationRef.current;
    const callUnmountIntent = () => {
      if (unmountCalledRef.current) return;
      unmountCalledRef.current = true;
      unmountIntentRef.current();
    };
    return () => {
      // Development Strict Mode probes an effect cleanup while the mounted DOM
      // remains connected. A microtask lets the matching setup cancel that probe;
      // real removal still invokes the idempotent owner cleanup exactly once.
      void Promise.resolve().then(() => {
        if (effectGenerationRef.current === effectGeneration) {
          callUnmountIntent();
        }
      });
    };
  }, []);

  const active = snapshot.phase === "concealed"
    || snapshot.phase === "revealed"
    || snapshot.phase === "copied";
  const exposed = snapshot.phase === "revealed" || snapshot.phase === "copied";
  const revealedContent = exposed ? renderRevealedContent() : null;
  const controls: ReactElement[] = [];

  if (snapshot.phase === "concealed") {
    const revealProps: ComponentProps<typeof Button> & Readonly<{
      "data-one-time-secret-reveal": string;
    }> = {
      autoFocus: focusIntent?.generation === snapshot.generation
        && focusIntent.target === "concealed",
      type: "button",
      variant: "outline",
      "data-one-time-secret-reveal": "",
      onClick: () => {
        setFocusIntent(Object.freeze({ generation: snapshot.generation, target: "revealed" }));
        onRevealIntent();
      },
    };
    controls.push(createElement(
      Button,
      { key: "reveal", ...revealProps },
      translate("oneTimeSecretReveal"),
    ));
  } else if (exposed) {
    const concealProps: ComponentProps<typeof Button> & Readonly<{
      "data-one-time-secret-conceal": string;
    }> = {
      autoFocus: focusIntent?.generation === snapshot.generation
        && focusIntent.target === "revealed",
      type: "button",
      variant: "outline",
      "data-one-time-secret-conceal": "",
      onClick: () => {
        setFocusIntent(Object.freeze({ generation: snapshot.generation, target: "concealed" }));
        onConcealIntent();
      },
    };
    controls.push(createElement(
      Button,
      { key: "conceal", ...concealProps },
      translate("oneTimeSecretConceal"),
    ));
    if (canCopy && onCopyIntent) {
      const copyProps: ComponentProps<typeof Button> & Readonly<{
        "data-one-time-secret-copy": string;
      }> = {
        type: "button",
        variant: "outline",
        "data-one-time-secret-copy": "",
        onClick: onCopyIntent,
      };
      controls.push(createElement(
        Button,
        { key: "copy", ...copyProps },
        translate("oneTimeSecretCopy"),
      ));
    }
  }

  if (active) {
    const acknowledgeProps: ComponentProps<typeof Button> & Readonly<{
      "data-one-time-secret-acknowledge": string;
    }> = {
      type: "button",
      "data-one-time-secret-acknowledge": "",
      onClick: onAcknowledgeIntent,
    };
    controls.push(createElement(
      Button,
      { key: "acknowledge", ...acknowledgeProps },
      translate("oneTimeSecretAcknowledge"),
    ));
    const dismissProps: ComponentProps<typeof Button> & Readonly<{
      "data-one-time-secret-dismiss": string;
    }> = {
      type: "button",
      variant: "outline",
      "data-one-time-secret-dismiss": "",
      onClick: onDismissIntent,
    };
    controls.push(createElement(
      Button,
      { key: "dismiss", ...dismissProps },
      translate("oneTimeSecretDismiss"),
    ));
  }

  return createElement(
    "section",
    {
      "data-one-time-secret-root": "",
    },
    active
      ? createElement("p", { "data-one-time-secret-ready": "" }, translate("oneTimeSecretReady"))
      : createElement(
          "p",
          { "data-one-time-secret-terminal": "" },
          terminalMessage(snapshot, translate),
        ),
    exposed
      ? createElement(
          "div",
          {
            tabIndex: -1,
            "data-one-time-secret-content": "",
          },
          revealedContent,
        )
      : null,
    exposed
      ? createElement(
          "p",
          { "data-one-time-secret-exposure-warning": "" },
          translate("oneTimeSecretExposureWarning"),
        )
      : null,
    snapshot.warningActive && active
      ? createElement(
          "p",
          {
            role: "status",
            "aria-live": "polite",
            "data-one-time-secret-warning": "",
          },
          translate("oneTimeSecretExpiringSoon"),
        )
      : null,
    snapshot.copyStatus === "copied" && exposed
      ? createElement(
          "p",
          {
            role: "status",
            "aria-live": "polite",
            "data-one-time-secret-copy-status": "",
          },
          translate("oneTimeSecretCopied"),
        )
      : snapshot.copyStatus === "failed" && exposed
        ? createElement(
            "p",
            {
              role: "status",
              "aria-live": "polite",
              "data-one-time-secret-copy-status": "",
            },
            translate("oneTimeSecretCopyFailed"),
          )
        : null,
    controls.length > 0
      ? createElement("div", { "data-one-time-secret-controls": "" }, ...controls)
      : null,
  );
}

function terminalMessage(
  snapshot: OneTimeSecretSnapshot,
  translate: OneTimeSecretRevealProps["translate"],
) {
  if (snapshot.phase === "acknowledged" || snapshot.clearReason === "acknowledged") {
    return translate("oneTimeSecretAcknowledged");
  }
  if (snapshot.clearReason === "expired") return translate("oneTimeSecretExpired");
  return translate("oneTimeSecretCleared");
}
