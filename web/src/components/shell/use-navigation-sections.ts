"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  createNavigationSectionsState,
  navigationSectionsStorageKey,
  restoreNavigationSectionsState,
  serializeNavigationSectionsState,
  synchronizeNavigationSectionsState,
  toggleNavigationSection,
} from "@/lib/navigation-section-state";
import { navigationSectionKeys } from "@/lib/navigation";

export function useNavigationSections(activeSectionKey: string | null) {
  const initialActiveSectionKey = useRef(activeSectionKey);
  const [navigationSectionsState, setNavigationSectionsState] = useState(() =>
    createNavigationSectionsState(navigationSectionKeys, navigationSectionKeys[0] || null, activeSectionKey),
  );
  const [loaded, setLoaded] = useState(false);
  const synchronizedNavigationSectionsState = useMemo(
    () => synchronizeNavigationSectionsState(navigationSectionsState, activeSectionKey),
    [activeSectionKey, navigationSectionsState],
  );

  if (synchronizedNavigationSectionsState !== navigationSectionsState) {
    setNavigationSectionsState(synchronizedNavigationSectionsState);
  }

  useEffect(() => {
    let serialized: string | null = null;
    try {
      serialized = window.localStorage.getItem(navigationSectionsStorageKey);
    } catch {
      // Browser privacy settings can disable storage. The in-memory state remains usable.
    }
    setNavigationSectionsState(
      restoreNavigationSectionsState(
        navigationSectionKeys,
        serialized,
        navigationSectionKeys[0] || null,
        initialActiveSectionKey.current,
      ),
    );
    setLoaded(true);
  }, []);

  useEffect(() => {
    if (!loaded) return;
    try {
      window.localStorage.setItem(
        navigationSectionsStorageKey,
        serializeNavigationSectionsState(synchronizedNavigationSectionsState, navigationSectionKeys),
      );
    } catch {
      // Keep navigation usable for this page lifecycle when storage is unavailable.
    }
  }, [loaded, synchronizedNavigationSectionsState]);

  const toggleNavigationSectionByKey = (sectionKey: string) => {
    setNavigationSectionsState((state) =>
      toggleNavigationSection(synchronizeNavigationSectionsState(state, activeSectionKey), sectionKey),
    );
  };

  return { synchronizedNavigationSectionsState, toggleNavigationSectionByKey };
}
