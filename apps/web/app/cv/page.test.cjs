const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");
const {renderToStaticMarkup}=require("react-dom/server");

function loadModule(path,resolve,extra=""){
  const source=fs.readFileSync(`${__dirname}/${path}`,"utf8")+extra;
  const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports={};
  vm.runInNewContext(code,{exports,process,require:resolve});
  return exports;
}

function detail(item){
  let state=0;
  const page=loadModule("page.tsx",name=>{
    if(name==="react")return {useState:initial=>[state++===0?item:initial,()=>{}],useEffect:()=>{}};
    if(name==="@/components/TailoredPreview")return loadModule("../../components/TailoredPreview.tsx",dep=>
      dep==="@/lib/types"?loadModule("../../lib/types.ts",require):require(dep));
    if(name.startsWith("@/"))return new Proxy({},{get:()=>()=>null});
    return require(name);
  },"\nexport {CVDetail};");
  return renderToStaticMarkup(page.CVDetail({item,busy:false}));
}

test("tailored review shows blockers and every keyword status before approval",()=>{
  const html=detail({id:"synthetic",kind:"tailored",filename:"synthetic.pdf",parsed:{tailored:{},
    blockers:["PDF text layer: phone is missing."],text_layer:{keywords:[
      {technology:"go",status:"covered",present:true},
      {technology:"rust",status:"gap",present:false},
      {technology:"postgresql",status:"missing",present:false},
      {technology:"kubernetes",status:"unsupported_present",present:true},
    ]}}});
  assert.match(html,/bg-red-50/);
  assert.match(html,/PDF text layer: phone is missing\./);
  for(const word of ["go","covered","rust","gap","postgresql","missing","kubernetes","unsupported"]){assert.ok(html.includes(word),word);}
});

test("base parse warnings remain visible",()=>{
  assert.match(detail({id:"base",kind:"base",filename:"synthetic.pdf",parsed:{warnings:["Email not found."]}}),/Email not found\./);
});

test("tailored review identifies the backup model's summary",()=>{
  const html=detail({id:"synthetic",kind:"tailored",filename:"synthetic.pdf",parsed:{
    tailored:{summary:"I build Go services.",summary_source:"ai_backup"},
  }});
  assert.match(html,/written by the backup AI model; read it closely/);
});

test("a follow-up keeps its backup label when the refreshed list omits the source",async()=>{
  const oldDraft={id:"old",created_at:"2026-11-01",body:"Earlier draft"};
  const newDraft={id:"new",created_at:"2026-11-02",body:"New draft"};
  const detail={application:{id:"app",status:"applied",created_at:"2026-11-01"},documents:{},follow_ups:[oldDraft]};
  const calls=[];
  async function api(path,options){
    calls.push([path,options?.method||"GET"]);
    if(options?.method==="POST")return {...newDraft,source:"ai_backup"};
    return {...detail,follow_ups:[oldDraft,newDraft]};
  }
  let slot=0;
  const state=[detail];
  const react={
    useEffect:()=>{},
    useState:initial=>{
      const i=slot++;
      if(!(i in state))state[i]=initial;
      return [state[i],value=>{state[i]=typeof value==="function"?value(state[i]):value;}];
    },
  };
  const page=loadModule("../applications/[id]/page.tsx",name=>{
    if(name==="react")return react;
    if(name==="next/navigation")return {useParams:()=>({id:"app"})};
    if(name==="@/lib/api")return {api};
    if(name.startsWith("@/"))return new Proxy({},{get:()=>({children})=>children??null});
    return require(name);
  });

  function findDraftButton(node){
    if(node?.type==="button"&&node.props.children==="Draft follow-up")return node;
    for(const child of [node?.props?.children].flat(Infinity)){
      if(child?.props){
        const button=findDraftButton(child);
        if(button)return button;
      }
    }
  }
  await findDraftButton(page.default()).props.onClick();
  assert.deepEqual(calls,[["/applications/app/follow-up","POST"],["/applications/app","GET"]]);
  slot=0;
  const html=renderToStaticMarkup(page.default());
  const articles=html.match(/<article\b[\s\S]*?<\/article>/g);
  assert.doesNotMatch(articles[0],/Written by/);
  assert.match(articles[1],/Follow-up 2[\s\S]*Written by the backup AI model; read it closely before you use it\.[\s\S]*New draft/);
});
