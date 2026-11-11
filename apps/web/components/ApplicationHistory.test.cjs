const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");
const {createElement}=require("react");
const {renderToStaticMarkup}=require("react-dom/server");

function load(){
 const source=fs.readFileSync(`${__dirname}/ApplicationHistory.tsx`,"utf8");
 const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
 const exports={};
 vm.runInNewContext(code,{exports,require(name){
  if(name==="next/link")return {default:props=>createElement("a",props)};
  return require(name);
 }});
 return exports;
}
const mount=history=>renderToStaticMarkup(load().ApplicationHistory({history}));
const entry={id:"history-1",employer:"Example Ltd",title:"Engineer",requisition:"REQ-1",applied_at:"2026-08-10",outcome_at:"2026-09-01",status:"rejected",source:"synthetic archive",evidence_kind:"rejection",evidence_ref:"archive:1",relation:"same_role",title_changed:false,in_window:true,date_unknown:false};
const history={mode:"warn",lookback:"all",blocked:false,entries:[entry]};

test("no history or empty entries render nothing",()=>{
 assert.equal(mount(),"");
 assert.equal(mount({...history,entries:[]}),"");
});
test("history states distinguish blocked, warning and outside the lookback",()=>{
 assert.match(mount({...history,blocked:true}),/Excluded by your application-history rule\./);
 assert.match(mount(history),/You applied to this company before\./);
 const outside=mount({...history,entries:[{...entry,in_window:false}]});
 assert.match(outside,/Earlier applications are outside your lookback\./);
 assert.match(outside,/Outside the lookback/);
 assert.doesNotMatch(outside,/You applied to this company before/);
 assert.match(mount({...history,entries:[{...entry,in_window:false},{...entry,id:"history-2"}]}),/You applied to this company before\./);
});
test("entry facts, relations and unknown dates remain explicit",()=>{
 for(const [relation,label] of Object.entries({same_role:"Same role",different_role:"Different role",uncertain_role:"Role not certain"})){
  const html=mount({...history,entries:[{...entry,relation,title:"",title_changed:true,date_unknown:true}]});
  for(const text of [label,"Role unknown","title changed since","Unknown date",entry.employer,entry.status,entry.outcome_at,entry.source,entry.evidence_kind,entry.evidence_ref,entry.requisition])assert.ok(html.includes(text),text);
 }
 assert.match(mount(history),/Applied: 2026-08-10/);
 assert.match(mount({...history,entries:[{...entry,applied_at:""}]}),/Unknown date/);
 assert.match(mount(history),/JobPilot has not checked any mailbox\. This list holds only what was imported or recorded here\./);
});
test("CV shows the recorded file and date, historical label, or exact unknown sentence",()=>{
 const unknown="CV used: Unknown. The previous application still counts.";
 assert.ok(mount(history).includes(unknown));
 const labelled=mount({...history,entries:[{...entry,cv_label:"archive-v1"}]});
 assert.match(labelled,/CV used: archive-v1/);
 assert.ok(!labelled.includes(unknown));
 const recorded=mount({...history,entries:[{...entry,cv:{document_id:"cv-1",filename:"resume.pdf",version_at:"2026-07-03T10:20:00Z"},cv_label:"ignored label"}]});
 assert.match(recorded,/CV used: resume.pdf \(2026-07-03\)/);
 assert.doesNotMatch(recorded,/ignored label|Unknown/);
 const deleted=mount({...history,entries:[{...entry,cv:{document_id:"cv-1"}}]});
 assert.match(deleted,/CV used: a file no longer stored \(document cv-1\)<\/p>/);
 assert.ok(mount({...history,entries:[{...entry,cv:{}}]}).includes(unknown));
});
test("recorded jobs link internally while imported URLs and hostile strings remain text",()=>{
 const url="https://example.test/jobs/previous";
 const html=mount({...history,entries:[{...entry,job_id:"job-1",source:"JobPilot"},{...entry,id:"import-1",employer:"<script>alert(1)</script>",canonical_url:url}]});
 assert.match(html,/href="\/jobs\/job-1"/);
 assert.equal((html.match(/href=/g)||[]).length,1);
 assert.ok(html.includes(url));
 assert.ok(html.includes("&lt;script&gt;alert(1)&lt;/script&gt;"));
 assert.doesNotMatch(html,/<script/);
});
test("a job card tells an excluded job, the same role and the same company apart",()=>{
 const chip=load().historyChip;
 assert.equal(chip(),"");
 assert.equal(chip({...history,entries:[]}),"");
 assert.equal(chip({...history,blocked:true}),"Excluded by history");
 assert.equal(chip(history),"Applied to this role before");
 assert.equal(chip({...history,entries:[{...entry,relation:"different_role"},{...entry,id:"history-2",relation:"uncertain_role"}]}),"Applied to this company before");
 assert.equal(chip({...history,entries:[{...entry,relation:"same_role",in_window:false},{...entry,id:"history-2",relation:"different_role"}]}),"Applied to this company before");
 assert.equal(chip({...history,entries:[{...entry,in_window:false}]}),"");
});
