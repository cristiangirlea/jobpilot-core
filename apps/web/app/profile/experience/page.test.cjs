const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");

// The page is called as a function with its state supplied, and its cards are
// found in the element tree it returns: no browser, API or account.
// State order in the page: items, form, error.
function page(items,respond){
  let slot=0;
  const sets=[],calls=[];
  const react={
    useState:initial=>{const i=slot++;return [i===0?items:initial,value=>sets.push([i,value])];},
    useEffect:()=>{},
  };
  const api=(path,options)=>{calls.push({path,method:options?.method||"GET"});return respond?respond(path,options):Promise.resolve([]);};
  const source=fs.readFileSync(`${__dirname}/page.tsx`,"utf8");
  const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports={};
  vm.runInNewContext(code,{exports,JSON,Promise,Error,require(name){
    if(name==="react")return react;
    if(name==="@/lib/api")return {api};
    if(name.startsWith("@/"))return new Proxy({},{get:(_,key)=>key});
    return require(name);
  }});
  const text=node=>node==null||node===false?"":typeof node==="string"||typeof node==="number"?String(node):Array.isArray(node)?node.map(text).join(""):text(node.props?.children);
  const cards=[];
  (function walk(node){
    if(Array.isArray(node))return node.forEach(walk);
    if(!node||typeof node!=="object")return;
    if(node.type==="article"){
      const row=node.props.children;
      const [facts,remove]=[].concat(row.props.children);
      const lines=[].concat(facts.props.children).filter(c=>c&&typeof c==="object");
      cards.push({row:row.props.className,facts:facts.props.className,remove:remove.props,lines:lines.map(l=>({cls:l.props.className,text:text(l)})),text:text(node)});
      return;
    }
    walk(node.props?.children);
  })(exports.default());
  return {cards,sets,calls};
}
const cards=items=>page(items).cards;
const role=extra=>({id:"1",company:"Acme",title:"Engineer",...extra});
// A card is title, company, an optional period, then the description.
const period=card=>card.lines.length===4?card.lines[2].text:undefined;
const settle=()=>new Promise(resolve=>setImmediate(resolve));

test("a card shows the role's period, and none when no date is stored",()=>{
  const [both,current,startOnly,endOnly,none,currentBare]=cards([
    role({start_date:"Apr 2021",end_date:"Jan 2024"}),
    role({start_date:"2011-02",current:true,end_date:"2011-03"}),
    role({start_date:"2019"}),
    role({end_date:"2020"}),
    role({}),
    role({current:true}),
  ]);
  assert.equal(period(both),"Apr 2021 – Jan 2024");
  assert.equal(period(current),"2011-02 – Present","a current role ends in Present whatever end date is stored");
  assert.equal(period(startOnly),"2019");
  assert.equal(period(endOnly),"2020");
  assert.equal(period(none),undefined,"no stored date draws no period line");
  assert.equal(period(currentBare),undefined,"no stored date draws no period line, current or not");
  for(const c of [both,current,startOnly,endOnly,none,currentBare])assert.ok(!c.text.includes("undefined"),c.text);
});

test("a current role with only an end date stored shows Present",()=>{
  // The form clears the end date when Current is ticked, but the API stores one sent with it.
  const [endOnly,startOnly]=cards([role({current:true,end_date:"Jan 2024"}),role({current:true,start_date:"2023"})]);
  assert.equal(period(endOnly),"Present");
  assert.ok(!endOnly.text.includes("Jan 2024"),"a current role never shows an end date");
  assert.equal(period(startOnly),"2023 – Present");
});

test("a description keeps its line breaks",()=>{
  const [card,empty]=cards([role({description:"Built the API.\nWrote the onboarding guide."}),role({})]);
  const description=card.lines.at(-1);
  assert.equal(description.text,"Built the API.\nWrote the onboarding guide.");
  assert.match(description.cls,/\bwhitespace-pre-line\b/);
  assert.equal(empty.lines.at(-1).text,"No description added.");
});

test("Remove stays at the top of the card, and long text wraps beside it",()=>{
  const [card]=cards([role({start_date:"x".repeat(200),description:"line\n".repeat(40)})]);
  assert.match(card.row,/\bitems-start\b/);
  assert.match(card.remove.className,/\bshrink-0\b/);
  // A flex child keeps its content's width unless allowed to shrink and break words.
  assert.match(card.facts,/\bmin-w-0\b/);
  // break-word would not shrink the column's minimum width; anywhere does.
  assert.ok(card.facts.split(" ").includes("[overflow-wrap:anywhere]"),card.facts);
});

test("a failed removal says why and keeps the card",async()=>{
  const p=page([role({})],(path,options)=>options?.method==="DELETE"?Promise.reject(new Error("Not found")):Promise.resolve([]));
  p.cards[0].remove.onClick();
  await settle();
  assert.deepEqual(p.calls,[{path:"/experiences/1",method:"DELETE"}],"no reload after a failed removal");
  assert.deepEqual(p.sets.filter(([i])=>i===2).map(([,v])=>v),["","Not found"]);
});

test("a removal that succeeds reloads the list",async()=>{
  const p=page([role({})],()=>Promise.resolve([]));
  p.cards[0].remove.onClick();
  await settle();
  assert.deepEqual(p.calls.map(c=>`${c.method} ${c.path}`),["DELETE /experiences/1","GET /experiences"]);
  assert.deepEqual(p.sets.filter(([i])=>i===2).map(([,v])=>v),[""],"no error is shown");
});
