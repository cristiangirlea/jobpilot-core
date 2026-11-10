const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const ts=require("typescript");
const React=require("react");
const {renderToStaticMarkup}=require("react-dom/server");

// Server rendering of invented data only: no browser, API or account.
function review(proposal){
  const source=fs.readFileSync(`${__dirname}/ProfileRepairReview.tsx`,"utf8");
  const code=ts.transpileModule(source,{compilerOptions:{jsx:ts.JsxEmit.ReactJSX,module:ts.ModuleKind.CommonJS}}).outputText;
  const exports={};
  vm.runInNewContext(code,{exports,require});
  return renderToStaticMarkup(React.createElement(exports.ProfileRepairReview,{proposal}));
}

const proposal={id:"synthetic-proposal",status:"pending",proposed_by:"browser",
  source:{id:"synthetic-cv",filename:"synthetic.txt",approved:false,parsed:{summary:"Source facts <script>bad()</script>"}},
  source_sha256:"exact-source-hash",before_fingerprint:"exact-profile-hash",
  warnings:["Synthetic parser warning","Role 1 has unknown dates"],
  changes:[
    {field:"skills",before:[{name:"Bogus <b>skill</b>"}],after:[{name:"Go",years:4}]},
    {field:"summary",before:"Scrambled",after:"Source summary"},
    {field:"languages",before:[],after:["English","German"]},
    {field:"blacklist",before:[{company_name:"Explicit exclusion"}],after:[{company_name:"Explicit exclusion"},{company_name:"Merged old company",reason:"Employer exclusion retained by profile repair"}]},
  ],
  before:{profile:{experiences:[{company:"Merged old company",title:"Wrong title",current:false,description:"Old source facts"}]},blacklist:[{company_name:"Explicit exclusion"}]},
  after:{profile:{experiences:[{company:"Source company",title:"Engineer",current:true,description:"Correct role facts"}]},blacklist:[{company_name:"Explicit exclusion"},{company_name:"Merged old company"}]}};

test("the review shows the source, warnings, every change and both role lists",()=>{
  const html=review(proposal);
  for(const text of ["exact-source-hash","exact-profile-hash","Synthetic parser warning","Role 1 has unknown dates",
    "Current / removed roles","Merged old company","Wrong title","Source company","Engineer","Unknown start date","Unknown end date","Present",
    "Go (4 years)","English, German","Explicit exclusion","Employer exclusion retained by profile repair",
    "Complete before-state snapshot","Pay, preferences, authorization, notice and projects stay unchanged",
    "(not approved when this was proposed)","Proposed in the web app."]){
    assert.ok(html.includes(text),`Missing review evidence: ${text}`);
  }
});

test("text from the CV and the profile is shown as text, never as markup",()=>{
  const html=review(proposal);
  assert.ok(!html.includes("<script>")&&!html.includes("<b>skill</b>"),"CV text rendered as markup");
  assert.ok(html.includes("&lt;script&gt;")&&html.includes("Bogus &lt;b&gt;skill&lt;/b&gt;"),"CV text must stay readable");
});

test("a proposal an agent asked for is marked",()=>{
  const html=review({...proposal,proposed_by:"agent",source:{...proposal.source,approved:true}});
  assert.ok(html.includes("An agent token asked for this proposal"));
  assert.ok(html.includes("(approved when this was proposed)")&&!html.includes("Proposed in the web app."));
  const unknown=review({...proposal,proposed_by:""});
  assert.ok(!unknown.includes("Proposed in the web app.")&&!unknown.includes("An agent token"),"an unknown proposer must not be named");
});

test("a proposal without warnings or changes says so",()=>{
  const html=review({...proposal,warnings:[],changes:[]});
  assert.ok(html.includes("No parse warnings recorded"));
  assert.ok(html.includes("No field changes"));
});
