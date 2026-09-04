(function () {
  var themes = ["autostream", "slate", "ocean", "cyan", "indigo", "violet", "magenta", "rose", "crimson", "amber", "emerald", "monochrome"];
  var modes = ["system", "light", "dark"];
  var theme = "autostream";
  var mode = "system";
  try {
    var raw = window.localStorage.getItem("autostream.ui_preference");
    if (raw) {
      var mirror = JSON.parse(raw);
      if (mirror && themes.indexOf(mirror.theme_id) !== -1) theme = mirror.theme_id;
      if (mirror && modes.indexOf(mirror.color_mode) !== -1) mode = mirror.color_mode;
    }
  } catch {
    theme = "autostream";
    mode = "system";
  }
  var systemDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.dataset.theme = theme;
  document.documentElement.dataset.colorMode = mode;
  document.documentElement.classList.toggle("dark", mode === "dark" || (mode === "system" && systemDark));
})();
