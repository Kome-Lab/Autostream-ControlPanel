import ts from "typescript";
import {existsSync} from "node:fs";
export async function renderedSource(source: string, originalURL: URL) {
  const adjusted=source.replace(/(from\s+["'])(\.[^"']+)(["'])/g,(_,before:string,specifier:string,after:string)=>{
    const url=new URL(specifier,originalURL);
    const resolved=[url,new URL(url.href+".ts"),new URL(url.href+".tsx")].find(candidate=>existsSync(candidate));
    if(!resolved)throw Error("actual source import missing: "+specifier);
    return before+resolved.href+after;
  });
  const output=ts.transpileModule(adjusted,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext,jsx:ts.JsxEmit.ReactJSX}}).outputText;
  const resolvedOutput=output.replace(/(from\s+["'])([^"']+)(["'])/g,(_,before:string,specifier:string,after:string)=>before+(specifier.startsWith("@/")||specifier.startsWith("file:")?specifier:import.meta.resolve(specifier))+after);
  return import("data:text/javascript;base64,"+Buffer.from(resolvedOutput).toString("base64"));
}
