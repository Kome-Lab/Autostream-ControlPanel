import { APIError } from "@/lib/api/client";
import { lifecycleOutcomePresentation, unavailableLifecycleOutcome, type LifecycleOutcomePresentation } from "./lifecycle-outcome-presentation";

// Opt-in result lookup uses the real WS2 HTTP contract. It never selects the
// demo/mock transport or retries a mutation. Cover GET may record reconciliation
// and critical audit on the server; it is not a pure same-Attempt Inspect.
export async function readStreamLifecycleOutcome<T>(path: string, scope: string, signal?: AbortSignal): Promise<Readonly<{ value: T; presentation: LifecycleOutcomePresentation }>> {
  const response=await fetch(path+(path.includes("?")?"&":"?")+"lifecycle_outcome=1",{method:"GET",cache:"no-store",credentials:"same-origin",headers:{Accept:"application/json"},signal});
  if(!response.ok)throw new APIError("Result lookup unavailable.",response.status);
  if(!response.headers.get("content-type")?.includes("application/json"))throw new APIError("Result lookup protocol invalid.",response.status,"non_json_response");
  let value:T;
  try{value=await response.json() as T;}catch{throw new APIError("Result lookup protocol invalid.",response.status,"invalid_json_response");}
  const header=response.headers.get("X-AutoStream-Lifecycle-Outcome");
  let presentation=unavailableLifecycleOutcome();
  if(header){
    presentation=unavailableLifecycleOutcome("unknown");
    if(header.length<=32768 && /^[A-Za-z0-9_-]+$/.test(header)){
      try{
        const bytes=Uint8Array.from(atob(header.replaceAll("-","+").replaceAll("_","/")),char=>char.charCodeAt(0));
        presentation=lifecycleOutcomePresentation(JSON.parse(new TextDecoder("utf-8",{fatal:true}).decode(bytes)),scope);
      }catch{ /* Retain UNKNOWN; never echo an untrusted header. */ }
    }
  }
  return Object.freeze({value,presentation});
}
