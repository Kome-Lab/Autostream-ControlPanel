// Minimal DOM boundary for executing the production observer expressions.
// This is direct checker coverage, never a real-browser or layout-engine verdict.
import { runInNewContext } from "node:vm";
export class Element extends EventTarget {
  document?: { activeElement: Element };
  children: Element[] = []; parentElement: Element | null = null;
  attributes: Record<string, string> = {}; dataset: Record<string, string> = {};
  disabled = false; type = ""; value = ""; labels: Element[] = [];
  open = false; focusVisible = true;
  animations: { playState: string; currentTime: number; effect: { getComputedTiming: () => { endTime: number } }; finished?: Promise<unknown> }[] = [];
  scrollLeft = 0; scrollTop = 0; scrollWidth = 140; clientWidth = 140; scrollHeight = 24; clientHeight = 24;
  hidden = false; className = ""; style = { visibility:"visible", display:"block", opacity:"1", position:"static", pointerEvents:"auto", clip:"auto", overflow:"visible", animationName:"none", animationDuration:"0s", transitionDuration:"0s", outlineStyle:"solid", outlineWidth:"2px", outlineColor:"rgb(0,0,255)", boxShadow:"none", overflowX:"visible", overflowY:"visible", getPropertyValue: () => "" };
  rect = {left: 10, right: 150, top: 10, bottom: 34, width: 140, height: 24};
  classList = { contains: (value: string) => this.className.split(" ").includes(value) };
  tagName: string; ownText: string;
  constructor(tagName: string, ownText = "") {super();this.tagName=tagName;this.ownText=ownText;}
  focus() {const document=this.closest('html')?.document;if(document)document.activeElement=this;}
  get textContent(): string { return this.ownText + this.children.map(child=>child.textContent).join(" "); }
  get outerHTML(): string { return "<"+this.tagName+">"+this.textContent+this.children.map(child=>child.outerHTML).join("")+"</"+this.tagName+">"; }
  get id() { return this.attributes.id || ""; } set id(value: string) {this.attributes.id=value;}
  get htmlFor() { return this.attributes.for || ""; } set htmlFor(value: string) {this.attributes.for=value;}
  get tabIndex() {return this.attributes.tabindex===undefined?(["BUTTON","INPUT","SELECT","TEXTAREA","A","SUMMARY"].includes(this.tagName)?0:-1):Number(this.attributes.tabindex);}
  get isConnected(): boolean {return this.tagName==="HTML"||!!this.parentElement?.isConnected;}
  getAnimations() {return this.animations;}
  add(child: Element) {child.parentElement=this;this.children.push(child);return child;}
  setAttribute(name: string,value: string) {this.attributes[name]=value;}
  getAttribute(name: string) {return this.attributes[name]??null;}
  removeAttribute(name: string) {delete this.attributes[name];}
  getClientRects() { if(this.hidden||this.style.display==='none')return [];for(let e=this.parentElement;e;e=e.parentElement)if(e.hidden||e.style.display==='none')return [];return [this.rect]; }
  getBoundingClientRect() {return this.rect;}
  all(): Element[] {return this.children.flatMap(child=>[child,...child.all()]);}
  matches(selector: string): boolean {return selector.split(",").some(part=>{
    part=part.trim(); if(part==="*")return true;
    const chain=part.split(/\s+(?![^[]*\])/);
    if(chain.length>1)return this.matches(chain.at(-1)!)&&(chain.at(-2)==='>'?!!this.parentElement?.matches(chain.slice(0,-2).join(' ')):!!this.parentElement?.closest(chain.slice(0,-1).join(' ')));
    for(const match of part.matchAll(/\.([a-zA-Z][\w-]*)/g))if(!this.className.split(' ').includes(match[1]))return false;
    const hadClass=part.startsWith('.');part=part.replace(/\.([a-zA-Z][\w-]*)/g,'');if(hadClass&&!part)return true;
    if(part.includes(":focus-visible")){if(!this.focusVisible)return false;part=part.replace(":focus-visible","");if(!part)return true;}
    for(const excluded of part.matchAll(/:not\(([^)]+)\)/g))if(this.matches(excluded[1]))return false;
    part=part.replace(/:not\([^)]+\)/g,"");
    if(part.includes(":first-child")&&this.parentElement?.children[0]!==this)return false;
    if(part.includes(":first-of-type")&&this.parentElement?.children.find(e=>e.tagName===this.tagName)!==this)return false;
    part=part.replace(/:first-(child|of-type)/g,"");
    const tag=part.match(/^[a-z][a-z0-9-]*/i)?.[0];if(tag&&tag.toUpperCase()!==this.tagName)return false;
    if(part.startsWith("#"))return this.id===part.slice(1);
    for(const match of part.matchAll(/\[([^\]=]+)(?:=["']?([^\]"']*)["']?)?\]/g)){const key=match[1].replace(/\$$/,'');if(!(key in this.attributes)||match[2]!==undefined&&(match[1].endsWith('$')?!this.attributes[key].endsWith(match[2]):this.attributes[key]!==match[2]))return false;}
    return Boolean(tag||part.startsWith("["));
  });}
  querySelectorAll(selector: string): Element[] {return [...new Set(selector.split(",").flatMap(single=>{
    const parts=single.trim().split(/\s+(?![^[]*\])/),final=parts.at(-1)!;
    return this.all().filter(e=>e.matches(final)&&(parts.length===1||parts.at(-2)==='>'?parts.length===1||!!e.parentElement?.matches(parts.slice(0,-2).join(' ')):Boolean(e.parentElement?.closest(parts.slice(0,-1).join(' ')))));
  }))];}
  querySelector(selector: string) {return this.querySelectorAll(selector)[0]||null;}
  closest(selector: string): Element|null {return this.matches(selector)?this:this.parentElement?.closest(selector)||null;}
  contains(other: Element): boolean {return this===other||this.all().includes(other);}
  scrollIntoView() {for(let p=this.parentElement;p;p=p.parentElement){p.scrollTop=400;p.scrollLeft=20;}}
}
export function observerDOM() {
  const html=new Element("HTML"),body=html.add(new Element("BODY")),main=body.add(new Element("MAIN"));
  html.attributes.lang="en";html.attributes["data-theme"]="autostream";
  Object.defineProperty(html,"lang",{get:()=>html.attributes.lang});
  main.add(new Element("H1","Stream input"));
  const label=main.add(new Element("LABEL","Name"));label.htmlFor="stream-name";
  const input=main.add(new Element("INPUT"));input.id="stream-name";input.labels=[label];
  const button=main.add(new Element("BUTTON","Open"));
  const document={documentElement:html,body,activeElement:input,elementFromPoint:(x:number,y:number):Element|null=>Number.isFinite(x)&&Number.isFinite(y)?button:null,querySelectorAll:(selector:string)=>html.querySelectorAll(selector),querySelector:(selector:string)=>html.querySelector(selector),getElementById:(id:string)=>html.all().find(e=>e.id===id)||null};
  html.document=document;
  const context={document,innerWidth:1024,innerHeight:900,scrollX:8,scrollY:60,
    location:{pathname:"/admin/streams/",search:"",hash:""},localStorage:{},sessionStorage:{},
    getComputedStyle:(e:Element)=>e.style,matchMedia:()=>({matches:false}),
    scrollTo:({left,top}:{left:number;top:number})=>{context.scrollX=left;context.scrollY=top;}};
  return {html,body,main,label,input,button,document,context,run:<T=unknown,>(expression:string)=>JSON.parse(JSON.stringify(runInNewContext(expression,context))) as T};
}
