const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");

// The page is called as a function with its state supplied, and its chips are
// found in the element tree it returns: no browser, API or account.
// State order in the page: items, adding, removing, suggestions, form, error.
function chips(state){
  let slot=0;
  const react={
    useState:initial=>{const i=slot++;return [i in state?state[i]:initial,()=>{}];},
    useEffect:()=>{},
    useMemo:fn=>fn(),
    useRef:current=>({current}),
  };
  const source=fs.readFileSync(`${__dirname}/page.tsx`,"utf8");
  const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports={};
  vm.runInNewContext(code,{exports,JSON,Promise,Object,Set,require(name){
    if(name==="react")return react;
    if(name.startsWith("@/"))return new Proxy({},{get:(_,key)=>key});
    return require(name);
  }});
  const text=node=>node==null||node===false?"":typeof node==="string"||typeof node==="number"?String(node):Array.isArray(node)?node.map(text).join(""):text(node.props?.children);
  const found=[];
  (function walk(node){
    if(Array.isArray(node))return node.forEach(walk);
    if(!node||typeof node!=="object")return;
    if(node.type==="div"&&String(node.props.className||"").includes("rounded-full")){
      // A chip is its name, an optional detail, and the remove button.
      const parts=[].concat(node.props.children).filter(c=>c&&c.type==="span").map(text);
      found.push({name:parts[0],detail:parts[1],text:text(node)});
      return;
    }
    walk(node.props?.children);
  })(exports.default());
  return found;
}

test("a chip shows the level and the years only where each is set",()=>{
  // The API leaves an empty level and zero years out of a skill, so the fields are missing.
  const got=chips({0:[
    {id:"1",name:"ABAC"},
    {id:"2",name:"AWS",level:"expert",years:6},
    {id:"3",name:"SQL",years:8},
    {id:"4",name:"Go",level:"advanced",years:0},
    {id:"5",name:"Lua",level:"",years:0.5},
  ]});
  const byName=Object.fromEntries(got.map(c=>[c.name,c.detail]));
  assert.deepEqual(byName,{ABAC:undefined,AWS:"expert · 6y",SQL:"8y",Go:"advanced",Lua:"0.5y"});
  for(const c of got)assert.ok(!c.text.includes("undefined"),`${c.name} must not show "undefined": ${c.text}`);
});

test("a skill on its way to the server says so instead of its detail",()=>{
  const got=chips({1:{rust:{name:"Rust",level:"expert",years:2}}});
  assert.equal(got.length,1);
  assert.equal(got[0].detail,"saving…");
});

// The page with the real API client over a fake fetch, and state that keeps
// what the handlers set: enough to run a save or a removal to its end.
function page(state,respond){
  const compile=file=>ts.transpileModule(fs.readFileSync(file,"utf8"),{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const client={};
  vm.runInNewContext(compile(`${__dirname}/../../../lib/api.ts`),{exports:client,process:{env:{}},Headers,Error,Object,JSON,Number,
    fetch:async(url,options={})=>{
      const answer=respond(options.method||"GET",url.replace(/^.*\/api/,""));
      if(answer instanceof Error)throw answer;
      return {ok:answer.status<400,status:answer.status,json:async()=>answer.body,headers:{get:()=>null}};
    }});
  const values={...state},refs=[];
  let slot=0;
  const react={
    useState:initial=>{const i=slot++;if(!(i in values))values[i]=initial;return [values[i],v=>{values[i]=typeof v==="function"?v(values[i]):v}];},
    useEffect:()=>{},
    useMemo:fn=>fn(),
    useRef:current=>{const ref={current};refs.push(ref);return ref;},
  };
  const exports={};
  vm.runInNewContext(compile(`${__dirname}/page.tsx`),{exports,JSON,Promise,Object,Set,Error,require(name){
    if(name==="react")return react;
    if(name==="@/lib/api")return client;
    if(name.startsWith("@/"))return new Proxy({},{get:(_,key)=>key});
    return require(name);
  }});
  const tree=exports.default();
  const find=match=>{let hit;(function walk(node){if(hit)return;if(Array.isArray(node))return node.forEach(walk);if(!node||typeof node!=="object")return;if(match(node))return void(hit=node);walk(node.props?.children)})(tree);return hit};
  // Refs in the page's order: the input, then the queue of saves.
  return {find,error:()=>values[5],done:()=>refs[1].current};
}
const notRescored={status:500,body:{error:"The change was saved, but your matches may not be re-scored. Save your profile again to re-score them.",code:"not_rescored"}};

test("a save the API reports as saved but not re-scored is worded by the API alone, even when the list cannot be read back",async()=>{
  const p=page({4:{name:"Kotlin",level:"",years:0}},(method)=>method==="POST"?notRescored:new Error("network down"));
  p.find(n=>n.type==="form").props.onSubmit({preventDefault(){}});
  await p.done();
  assert.equal(p.error(),`Kotlin: ${notRescored.body.error}`);
});

test("a removal the API reports as saved but not re-scored is worded by the API alone",async()=>{
  const p=page({0:[{id:"1",name:"Kotlin"}]},(method)=>method==="DELETE"?notRescored:new Error("network down"));
  p.find(n=>n.props?.["aria-label"]==="Remove Kotlin").props.onClick();
  await p.done();
  assert.equal(p.error(),`Kotlin: ${notRescored.body.error}`);
});

test("any other failed save still says the skill was not saved when the server does not list it",async()=>{
  const p=page({4:{name:"Kotlin",level:"",years:0}},(method)=>method==="POST"?{status:500,body:{error:"An internal error occurred."}}:{status:200,body:[]});
  p.find(n=>n.type==="form").props.onSubmit({preventDefault(){}});
  await p.done();
  assert.equal(p.error(),"Kotlin was not saved: An internal error occurred.");
});
