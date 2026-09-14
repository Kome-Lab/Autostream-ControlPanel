let webRoot;
let compiler;

export function initialize(data) {
  webRoot = data.webRoot;
  compiler = data.compiler;
}

export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith("@/")) {
    const target = new URL("src/" + specifier.slice(2), webRoot);
    for (const extension of [".ts", ".tsx"]) {
      try { return await nextResolve(target.href + extension, context); } catch (error) {
        if (error.code !== "ERR_MODULE_NOT_FOUND") throw error;
      }
    }
  }
  if (["next/link", "next/navigation", "next/image", "next/script"].includes(specifier)) {
    return nextResolve(specifier + ".js", context);
  }
  if (specifier.startsWith(".") && context.parentURL?.startsWith(webRoot) && !/\.[cm]?[jt]sx?$/.test(specifier)) {
    for (const extension of [".ts", ".tsx"]) {
      try { return await nextResolve(specifier + extension, context); } catch (error) {
        if (error.code !== "ERR_MODULE_NOT_FOUND") throw error;
      }
    }
  }
  return nextResolve(specifier, context);
}

export async function load(url, context, nextLoad) {
  if (!url.startsWith(webRoot) || !url.endsWith(".tsx")) return nextLoad(url, context);
  const { readFile } = await import("node:fs/promises");
  const ts = (await import(compiler)).default;
  const source = await readFile(new URL(url), "utf8");
  return {
    format: "module", shortCircuit: true,
    source: ts.transpileModule(source, {
      compilerOptions: { jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
    }).outputText,
  };
}
