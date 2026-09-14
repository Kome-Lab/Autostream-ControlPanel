// Minimal DOM boundary for executing the production observer expressions.
// This is direct checker coverage, never a real-browser or layout-engine verdict.
import { runInNewContext } from "node:vm";
export class Element {
  children: Element[] = []; parentElement: Element | null = null;
  attributes: Record<string, string> = {}; dataset: Record<string, string> = {};
  disabled = false; type = ""; value = ""; labels: Element[] = [];
  scrollLeft = 0; scrollTop = 0; scrollWidth = 140; clientWidth = 140; scrollHeight = 24; clientHeight = 24;
  hidden = false; className = ""; style = { visibility:"visible", display:"block", animationName:"none", animationDuration:"0s", transitionDuration:"0s", outlineStyle:"solid", boxShadow:"none", overflowX:"visible", overflowY:"visible" };
  rect = {left: 10, right: 150, top: 10, bottom: 34, width: 140, height: 24};
  classList = { contains: (value: string) => this.className.split(" ").includes(value) };
  tagName: string; ownText: string;
  constructor(tagName: string, ownText = "") {this.tagName=tagName;this.ownText=ownText;}
  get textContent(): string { return this.ownText + this.children.map(child=>child.textContent).join(" "); }
  get outerHTML(): string { return "<"+this.tagName+">"+this.textContent+this.children.map(child=>child.outerHTML).join("")+"</"+this.tagName+">"; }
  get id() { return this.attributes.id || ""; } set id(value: string) {this.attributes.id=value;}
  get htmlFor() { return this.attributes.for || ""; } set htmlFor(value: string) {this.attributes.for=value;}
  add(child: Element) {child.parentElement=this;this.children.push(child);return child;}
  setAttribute(name: string,value: string) {this.attributes[name]=value;}
  getAttribute(name: string) {return this.attributes[name]??null;}
  removeAttribute(name: string) {delete this.attributes[name];}
  getClientRects() { return this.hidden?[]:[this.rect]; }
  getBoundingClientRect() {return this.rect;}
  all(): Element[] {return this.children.flatMap(child=>[child,...child.all()]);}
  matches(selector: string): boolean {return selector.split(",").some(part=>{
    part=part.trim(); if(part==="*")return true;
    const tag=part.match(/^[a-z][a-z0-9-]*/i)?.[0];if(tag&&tag.toUpperCase()!==this.tagName)return false;
    if(part.startsWith("#"))return this.id===part.slice(1);
    for(const match of part.matchAll(/\[([^\]=]+)(?:=["']?([^\]"']*)["']?)?\]/g))if(!(match[1] in this.attributes)||match[2]!==undefined&&this.attributes[match[1]]!==match[2])return false;
    return Boolean(tag||part.startsWith("["));
  });}
  querySelectorAll(selector: string) {const parts=selector.trim().split(/\s+(?![^[]*\])/);const final=parts.at(-1)!;return this.all().filter(e=>e.matches(final)&& (parts.length===1||Boolean(e.parentElement?.closest(parts.slice(0,-1).join(" ")))));}
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
  const document={documentElement:html,body,activeElement:input,querySelectorAll:(selector:string)=>html.querySelectorAll(selector),querySelector:(selector:string)=>html.querySelector(selector),getElementById:(id:string)=>html.all().find(e=>e.id===id)||null};
  const context={document,innerWidth:1024,innerHeight:900,scrollX:8,scrollY:60,
    location:{pathname:"/admin/streams/",search:"",hash:""},localStorage:{},sessionStorage:{},
    getComputedStyle:(e:Element)=>e.style,matchMedia:()=>({matches:false}),
    scrollTo:({left,top}:{left:number;top:number})=>{context.scrollX=left;context.scrollY=top;}};
  return {html,body,main,label,input,button,document,context,run:<T=unknown,>(expression:string)=>JSON.parse(JSON.stringify(runInNewContext(expression,context))) as T};
}
