import {ProfileRepair} from "@/lib/types";

const display=(v:unknown)=>typeof v==="string"?(v||"(empty)"):JSON.stringify(v,null,2);
const fieldNames:Record<string,string>={current_title:"Professional title",years_experience:"Overall experience (years)",experiences:"Work experience",skills:"Skills",blacklist:"Excluded companies",summary:"Professional summary",linkedin:"LinkedIn",github:"GitHub"};
function ChangeValue({field,value}:{field:string;value:unknown}){
 if(Array.isArray(value)){
  if(field==='experiences')return <p className="rounded bg-black/5 p-3 text-sm">{value.length} roles. Compare the full roles and dates below.</p>;
  if(field==='skills')return <ul className="flex flex-wrap gap-2 rounded bg-black/5 p-3 text-sm">{value.map((s,i)=><li key={i} className="rounded border bg-white px-2 py-1">{s.name}{s.years>0?` (${s.years} years)`:''}</li>)}</ul>;
  if(field==='blacklist')return <ul className="space-y-2 rounded bg-black/5 p-3 text-sm">{value.map((s,i)=><li key={i}>{s.company_name||s.company_domain}{s.reason?` — ${s.reason}`:''}</li>)}</ul>;
  if(value.every(x=>typeof x==='string'))return <p className="whitespace-pre-wrap rounded bg-black/5 p-3 text-sm">{value.join(', ')||'(empty)'}</p>;
 }
 return <pre className="whitespace-pre-wrap break-words rounded bg-black/5 p-3 text-sm">{display(value)}</pre>;
}
export function ProfileRepairReview({proposal:r}:{proposal:ProfileRepair}){
 return <div className="space-y-5">
  <p>Status: <b>{r.status}</b>. Source CV: <b>{r.source.label||r.source.filename}</b> ({r.source.approved?'approved':'not approved'} when this was proposed).</p>
  {r.proposed_by==='agent'&&<p className="rounded bg-amber-50 p-3 text-sm">An agent token asked for this proposal. Check that the source is the CV you expect before applying.</p>}
  {r.proposed_by==='browser'&&<p className="text-sm">Proposed in the web app.</p>}
  <details><summary>Source and version details</summary><p className="break-all text-xs">Document: {r.source.filename} ({r.source.id})<br/>Source SHA-256: {r.source_sha256}<br/>Profile fingerprint: {r.before_fingerprint}</p></details>
  <p className="text-sm">Only present CV facts replace fields. Roles and skills below replace their entire current lists when the CV has those lists. Pay, preferences, authorization, notice and projects stay unchanged. Removed employers remain excluded as shown below. CV approval is separate.</p>
  <section><h2 className="font-semibold">Parse warnings</h2>{r.warnings.length?<ul className="list-disc pl-5">{r.warnings.map((v,i)=><li key={i}>{v}</li>)}</ul>:<p>No parse warnings recorded. Check all rows and dates.</p>}</section>
  <section><h2 className="font-semibold">Before / after changes</h2>{r.changes.length===0&&<p>No field changes.</p>}{r.changes.map(c=><div key={c.field} className="mt-3"><h3 className="font-semibold">{fieldNames[c.field]||c.field.replaceAll('_',' ')}</h3><div className="grid gap-2 md:grid-cols-2"><div><p>Current / removed values</p><ChangeValue field={c.field} value={c.before}/></div><div><p>Proposed values</p><ChangeValue field={c.field} value={c.after}/></div></div></div>)}</section>
  <section className="grid gap-4 md:grid-cols-2">{([['Current / removed roles',r.before.profile.experiences],['Proposed roles',r.after.profile.experiences]] as const).map(([label,rows])=><div key={label}><h2 className="font-semibold">{label} ({rows.length})</h2>{rows.map((x,i)=><article key={i} className="mt-2 rounded border p-3 text-sm [overflow-wrap:anywhere]"><p><b>{x.company}</b> — {x.title}{x.current?' (current)':''}</p><p>{x.start_date||'Unknown start date'} – {x.current?'Present':x.end_date||'Unknown end date'}</p>{x.company_domain&&<p>{x.company_domain}</p>}<details className="mt-2"><summary>Experience details</summary><p className="whitespace-pre-wrap">{x.description||'No description provided.'}</p></details></article>)}</div>)}</section>
  <details><summary>Complete before-state snapshot (retained for restore)</summary><pre className="whitespace-pre-wrap break-words text-xs">{display(r.before)}</pre></details>
  <details><summary>Complete proposed state and exclusions</summary><pre className="whitespace-pre-wrap break-words text-xs">{display(r.after)}</pre></details>
  <details><summary>Stored source facts (the CV's text is not kept here)</summary><pre className="whitespace-pre-wrap break-words text-xs">{display(r.source)}</pre></details>
 </div>
}
