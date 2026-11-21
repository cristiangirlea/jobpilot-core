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
