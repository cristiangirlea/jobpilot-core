const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");

// The page is called as a function with its state supplied, and its buttons are
// found in the element tree it returns: no browser, API or account.
// State order in the page: items, cvs, source, open, busy, error, notice, reviewed.
function page(state,respond){
  const calls=[],sets=[],effects=[];
  let slot=0;
  const react={
    useState:initial=>{const i=slot++;return [i in state?state[i]:initial,value=>sets.push([i,value])];},
    useEffect:fn=>effects.push(fn),
  };
  const api=(path,options)=>{calls.push({path,method:options?.method||"GET",body:options?.body?JSON.parse(options.body):undefined});return Promise.resolve(respond?respond(path):{});};
  const source=fs.readFileSync(`${__dirname}/page.tsx`,"utf8");
  const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports={};
  vm.runInNewContext(code,{exports,JSON,Promise,Date,require(name){
    if(name==="react")return react;
    if(name==="@/lib/api")return {api};
    if(name.startsWith("@/"))return new Proxy({},{get:(_,key)=>key});
    return require(name);
  }});
  const tree=exports.default();
  const text=node=>node==null||node===false?"":typeof node==="string"||typeof node==="number"?String(node):Array.isArray(node)?node.map(text).join(""):text(node.props?.children);
  const buttons=[];
  (function walk(node){
    if(Array.isArray(node))return node.forEach(walk);
    if(!node||typeof node!=="object")return;
    if(node.type==="button")buttons.push({label:text(node),disabled:!!node.props.disabled,click:node.props.onClick});
    walk(node.props?.children);
  })(tree);
  return {calls,sets,effects,buttons,text:text(tree),button:label=>buttons.find(b=>b.label.includes(label))};
}

const pending={id:"repair-1",status:"pending",before_fingerprint:"fp-before"};
const applied={id:"repair-1",status:"applied",before_fingerprint:"fp-before",applied_fingerprint:"fp-applied"};
const settle=()=>new Promise(resolve=>setImmediate(resolve));

test("apply waits for the review tick and sends the fingerprint the page showed",async()=>{
  const unticked=page({3:pending});
  assert.equal(unticked.button("Apply reviewed repair").disabled,true);
  assert.equal(unticked.button("Reject proposal").disabled,false);
  const ticked=page({3:pending,7:true},()=>applied);
  assert.equal(ticked.button("Apply reviewed repair").disabled,false);
  ticked.button("Apply reviewed repair").click();
  await settle();
  assert.deepEqual(ticked.calls[0],{path:"/profile/repairs/repair-1/decision",method:"POST",body:{decision:"apply",expected_fingerprint:"fp-before"}});
  assert.ok(ticked.sets.some(([i,value])=>i===3&&value===applied),"the page must show the applied proposal");
  assert.ok(ticked.sets.some(([i,value])=>i===7&&value===false),"the review tick must be cleared after a decision");
});

test("reject sends the proposal's fingerprint",async()=>{
  const p=page({3:pending});
  p.button("Reject proposal").click();
  await settle();
  assert.deepEqual(p.calls[0].body,{decision:"reject",expected_fingerprint:"fp-before"});
});

test("restore is offered only for an applied repair and sends the applied fingerprint",async()=>{
  const p=page({3:applied,7:true});
  assert.equal(p.button("Apply reviewed repair"),undefined);
  p.button("Restore full previous profile").click();
  await settle();
  assert.deepEqual(p.calls[0].body,{decision:"restore",expected_fingerprint:"fp-applied"});
  assert.equal(page({3:applied}).button("Restore full previous profile").disabled,true);
});

test("a rejected or restored repair offers no decision",()=>{
  for(const status of ["rejected","restored"]){
    const p=page({3:{...pending,status}});
    assert.equal(p.button("Apply"),undefined);
    assert.equal(p.button("Reject"),undefined);
    assert.equal(p.button("Restore"),undefined);
  }
});

test("proposing posts the chosen CV and nothing else",async()=>{
  assert.equal(page({}).button("Create review proposal").disabled,true);
  const p=page({2:"cv-1"},()=>pending);
  p.button("Create review proposal").click();
  await settle();
  assert.deepEqual(p.calls[0],{path:"/profile/repairs",method:"POST",body:{source_cv_id:"cv-1"}});
});

test("only base CVs are offered as a source, and agent proposals are marked in the list",async()=>{
  const listed=page({0:[{id:"r1",status:"pending",created_at:"2026-01-02T03:04:05Z",proposed_by:"agent"},{id:"r2",status:"applied",created_at:"2026-01-02T03:04:05Z",proposed_by:"browser"}]});
  assert.ok(listed.buttons.some(b=>b.label.includes("pending")&&b.label.includes("by an agent")));
  assert.ok(listed.buttons.some(b=>b.label.includes("applied")&&!b.label.includes("by an agent")));
  const p=page({},path=>path==="/cv"?[{id:"a",kind:"base"},{id:"b",kind:"tailored"}]:[]);
  p.effects[0]();
  await settle();
  const cvs=p.sets.find(([i])=>i===1);
  assert.deepEqual(cvs[1].map(c=>c.id),["a"]);
});
