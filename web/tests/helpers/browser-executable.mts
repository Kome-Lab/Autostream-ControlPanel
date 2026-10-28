import { existsSync } from "node:fs";

const defaultBrowserCandidates = process.platform === "win32"
  ? [
      "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
      "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
      "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
      "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
    ]
  : process.platform === "darwin"
    ? [
        "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
        "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
      ]
    : ["/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser"];

export function resolveBrowserPath() {
  const configuredPath = process.env.AUTOSTREAM_BROWSER_PATH;
  if (configuredPath) {
    if (!existsSync(configuredPath)) throw new Error("Configured Chrome/Chromium executable was not found");
    return configuredPath;
  }
  const browserPath = defaultBrowserCandidates.find((candidate) => existsSync(candidate));
  if (!browserPath) throw new Error("Chrome/Chromium was not found; set AUTOSTREAM_BROWSER_PATH");
  return browserPath;
}
