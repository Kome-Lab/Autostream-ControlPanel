import { register } from "node:module";

// Use the installed TypeScript compiler and the real production modules.
register(new URL("./typescript-loader.mjs", import.meta.url), {
  parentURL: import.meta.url,
  data: { webRoot: new URL("../../", import.meta.url).href, compiler: import.meta.resolve("typescript") },
});
