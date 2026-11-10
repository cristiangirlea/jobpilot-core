const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");
const {renderToStaticMarkup}=require("react-dom/server");

// mount runs the component once over the given state and a fake API client.
function mount({items=[],next="",error="",busy=false,api=async()=>({items:[],next:""})}={}){
  const state=[items,next,error,busy],sets=[[],[],[],[]],effects=[];let n=0;
  const source=fs.readFileSync(`${__dirname}/ExternalHistory.tsx`,"utf8");
  const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports={};
  vm.runInNewContext(code,{exports,process,Error,require(name){
    if(name==="react")return {useState:()=>{const i=n++;return [state[i],value=>sets[i].push(value)]},useEffect:fn=>effects.push(fn)};
    if(name==="@/lib/api")return {api};
    if(name==="@/components/UI")return {ErrorMessage:({message})=>message||null};
    return require(name);
  }});
  const tree=exports.ExternalHistory();
  return {html:renderToStaticMarkup(tree),tree,effects,items:sets[0],next:sets[1],error:sets[2],busy:sets[3]};
}
function find(node,type){
  if(!node||typeof node!=="object")return null;
  if(Array.isArray(node)){for(const child of node){const hit=find(child,type);if(hit)return hit}return null}
  return node.type===type?node:find(node.props&&node.props.children,type);
}
const settle=()=>new Promise(resolve=>setImmediate(resolve));
const record={id:"1",external_key:"synthetic:receipt:1",employer:"Synthetic <script>alert(1)</script> Ltd",title:"",requisition:"REQ-1",
  canonical_url:"javascript:alert(1)",applied_at:"2024-02-02",outcome_at:"",status:"applied",source:"synthetic archive",
  evidence_kind:"application_receipt",evidence_ref:"archive:receipt-1",cv_version:"",imported_at:"2026-01-01T00:00:00Z"};

test("history shows the stored facts as text, with no link and no action",()=>{
  const {html}=mount({items:[record]});
  for(const text of ["Synthetic &lt;script&gt;alert(1)&lt;/script&gt; Ltd","Role unknown","applied","synthetic:receipt:1","REQ-1","javascript:alert(1)","2024-02-02","synthetic archive","application_receipt","archive:receipt-1","2026-01-01T00:00:00Z"]){assert.ok(html.includes(text),text);}
  assert.equal(html.split("<dd>Unknown</dd>").length-1,2,"the unknown outcome date and CV version say so");
  for(const markup of ["<script","<a ","href=","<button","<form"]){assert.ok(!html.includes(markup),markup);}
});

test("every stored fact sits under its own label",()=>{
  const full={...record,employer:"Synthetic Example Ltd",title:"Staff Engineer",status:"rejected",outcome_at:"2024-03-01",cv_version:"sha256:synthetic-v1",canonical_url:"https://example.test/jobs/1"};
  const {html}=mount({items:[full]});
  assert.ok(html.includes(">Synthetic Example Ltd · Staff Engineer · rejected</summary>"),html);
  for(const [label,value] of Object.entries({"External key":full.external_key,"Requisition":full.requisition,"Canonical URL (reference only)":full.canonical_url,
    "Original applied date":full.applied_at,"Original outcome date":full.outcome_at,"Source":full.source,"Evidence kind":full.evidence_kind,
    "Evidence reference":full.evidence_ref,"CV version (historical reference)":full.cv_version,"Imported at":full.imported_at})){
    assert.ok(html.includes(`<dt class="font-semibold">${label}</dt><dd>${value}</dd>`),label);
  }
  assert.ok(!html.includes("Unknown"),"nothing known is shown as unknown");
});

test("an empty ledger says so, but not while loading or after a failure",()=>{
  assert.match(mount().html,/No external history imported\./);
  assert.doesNotMatch(mount({busy:true}).html,/No external history imported\./);
  const failed=mount({error:"history is unavailable"}).html;
  assert.match(failed,/history is unavailable/);
  assert.doesNotMatch(failed,/No external history imported\./);
});

test("the first load replaces the list and Load more adds the next page",async()=>{
  const calls=[],page={items:[{...record,id:"2"}],next:"cursor-2"};
  const view=mount({items:[record],next:"cursor 1",api:async path=>{calls.push(path);return page}});
  assert.equal(view.effects.length,1);
  view.effects[0]();await settle();
  assert.equal(calls[0],"/application-history?limit=50&after=");
  assert.deepEqual(view.items[0]([record]),page.items);
  assert.deepEqual(view.next,["cursor-2"]);
  const button=find(view.tree,"button");
  assert.match(renderToStaticMarkup(button),/Load more history/);
  button.props.onClick();await settle();
  assert.equal(calls[1],"/application-history?limit=50&after=cursor%201");
  assert.deepEqual(Array.from(view.items[1]([record])),[record,...page.items]);
  assert.deepEqual(view.busy,[true,false,true,false]);
});

test("a failed load shows its reason and leaves Load more usable",async()=>{
  const view=mount({items:[record],next:"cursor-1",api:async()=>{throw new Error("history is unavailable")}});
  find(view.tree,"button").props.onClick();await settle();
  assert.deepEqual(view.error,["history is unavailable"]);
  assert.deepEqual(view.items,[]);
  assert.deepEqual(view.busy,[true,false]);
});
