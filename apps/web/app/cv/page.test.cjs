const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");
const {renderToStaticMarkup}=require("react-dom/server");

function detail(item){
  let state=0;
  const source=fs.readFileSync(`${__dirname}/page.tsx`,"utf8")+"\nexport {CVDetail};";
  const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports={};
  vm.runInNewContext(code,{exports,process,require(name){
    if(name==="react")return {useState:initial=>[state++===0?item:initial,()=>{}],useEffect:()=>{}};
    if(name.startsWith("@/"))return new Proxy({},{get:()=>()=>null});
    return require(name);
  }});
  return renderToStaticMarkup(exports.CVDetail({item,busy:false}));
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
