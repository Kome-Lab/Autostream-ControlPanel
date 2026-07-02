"use client";

import { createContext, useContext, useEffect, useMemo, useState } from "react";

const themeStorageKey = "autostream.theme";

type ThemeContextValue = {
  dark: boolean;
  toggleTheme: () => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [dark, setDark] = useState(() => {
    if (typeof window === "undefined") return false;
    const stored = window.localStorage.getItem(themeStorageKey);
    return stored ? stored === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
  });

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
  }, [dark]);

  const value = useMemo<ThemeContextValue>(() => {
    const toggleTheme = () => {
      setDark((current) => {
        const next = !current;
        window.localStorage.setItem(themeStorageKey, next ? "dark" : "light");
        document.documentElement.classList.toggle("dark", next);
        return next;
      });
    };
    return { dark, toggleTheme };
  }, [dark]);

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) throw new Error("useTheme must be used inside ThemeProvider.");
  return context;
}
