"use client";
import Link from "next/link";
import {useParams,useRouter} from "next/navigation";
import {useEffect,useState} from "react";
import {JobDescription} from "@/components/JobDescription";
import {OpenApplyButton} from "@/components/OpenApplyButton";
import {Shell} from "@/components/Shell";
import {CopyButton,ErrorMessage,Header,Notice,Score} from "@/components/UI";
import {api,jobHref} from "@/lib/api";
import {Application,Documents,JobMatch,JobReport} from "@/lib/types";

const labels:Record<string,string>={title_match:"Title",skill_match:"Skills",seniority_match:"Seniority",location_remote_match:"Location / remote",salary_match:"Compensation",domain_industry_match:"Industry",freshness:"Freshness"};
const maximum=(key:string)=>key==="title_match"?25:key==="skill_match"?30:key==="seniority_match"?15:key==="location_remote_match"||key==="salary_match"?10:5;

export default function JobDetail(){
  // The address holds a slug, or an id from a link made before slugs existed;
  // either finds the job, and every action below uses its id.
  const {id:ref}=useParams<{id:string}>(),router=useRouter();
  const [data,setData]=useState<JobMatch|null>(null),[docs,setDocs]=useState<Documents|null>(null),[error,setError]=useState(""),[notice,setNotice]=useState(""),[busy,setBusy]=useState("");
  useEffect(()=>{api<JobMatch>(`/jobs/${ref}`).then(x=>{setData(x);if(x.job.slug&&x.job.slug!==ref)window.history.replaceState(null,"",jobHref(x.job))}).catch(e=>setError(e.message))},[ref]);
  const id=data?.job.id;
  async function generate(){setBusy("generate");try{setDocs(await api<Documents>(`/jobs/${id}/generate`,{method:"POST"}))}catch(e){setError(e instanceof Error?e.message:"Generation failed")}finally{setBusy("")}}
  async function prepare(){setBusy("prepare");try{await api(`/jobs/${id}/compose`,{method:"POST",body:"{}"});router.push("/outbox")}catch(e){setError(e instanceof Error?e.message:"Unable to prepare this application")}finally{setBusy("")}}
  // A report hides the job from this user at once; the API says whether it
  // also closed for everyone, and the page reloads to show which. When it
  // only hid the job, the reported banner already says so.
  async function report(){setBusy("report");setError("");try{const r=await api<JobReport>(`/jobs/${id}/report`,{method:"POST",body:"{}"});setNotice(r.closed?r.message:"");setData(await api<JobMatch>(`/jobs/${id}`))}catch(e){setError(e instanceof Error?e.message:"Unable to report this posting")}finally{setBusy("")}}
  async function withdraw(){setBusy("report");setError("");try{await api(`/jobs/${id}/report`,{method:"DELETE"});setNotice("Your report is withdrawn.");setData(await api<JobMatch>(`/jobs/${id}`))}catch(e){setError(e instanceof Error?e.message:"Unable to withdraw the report")}finally{setBusy("")}}
  async function createApplication(){setBusy("application");try{const a=await api<Application>("/applications",{method:"POST",body:JSON.stringify({job_id:id})});router.push(`/applications/${a.id}`)}catch(e){setError(e instanceof Error?e.message:"Unable to create application")}finally{setBusy("")}}
  if(!data)return <Shell><ErrorMessage message={error}/><div className="card text-ink/50">Loading job evaluation…</div></Shell>;
  const {job,match}=data;
  const companyBlocked=match.rejection_reasons.some(reason=>/employer|company blacklist|company domain/i.test(reason));
  return <Shell>
    <Header eyebrow={match.rejected?"Rejected at import":job.discovered_at?"Discovered job":"Job evaluation"} title={job.title} description={`${job.company} · ${job.location||"Location unstated"} · ${job.remote_type||"Arrangement unstated"}`} action={<div className="flex gap-2">{!match.rejected&&!job.closed_at&&job.apply_url&&<OpenApplyButton jobId={job.id} className="btn-secondary"/>}<Link href="/jobs" className="btn-secondary">All jobs</Link></div>}/>
    {job.closed_at&&<section className="mb-5 rounded-2xl border border-slate-200 bg-slate-50 p-4 text-sm text-slate-700"><strong>This posting has closed</strong> ({new Date(job.closed_at).toLocaleDateString()}): {job.closed_reason||"it is no longer available"}. It stays here for your records, but it can no longer be applied to.</section>}
    <Notice message={notice}/>
    {!job.closed_at&&(data.reported
      ?<section className="mb-5 rounded-2xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900"><strong>You reported this posting as no longer available.</strong> It is hidden from your lists, and closes for everyone once its link confirms it or someone else reports it too. <button type="button" className="font-semibold underline" disabled={busy==="report"} onClick={withdraw}>Undo</button></section>
      :<p className="mb-5 text-sm text-ink/50">Posting gone or already filled? <button type="button" className="font-semibold underline" disabled={busy==="report"} onClick={report}>Report it as no longer available</button></p>)}
    {job.referral_disclosure&&<div className="mb-4 rounded-xl bg-blue-50 px-4 py-3 text-xs text-blue-800">{job.referral_disclosure}</div>}
    {job.source_name&&<div className="mb-4 text-xs text-ink/50">Discovered via {job.source_provider==="remotive"?<a className="font-semibold text-moss" href="https://remotive.com/remote-jobs" target="_blank" rel="noreferrer">Remotive</a>:job.source_name}.
      {job.employer_url&&!job.closed_at&&<> Applies on the employer's own page at <span className="font-semibold text-ink/70">{hostOf(job.employer_url)}</span>, where the apply button takes you.</>}</div>}
    <ErrorMessage message={error}/>
    {match.rejected&&<section className="mb-5 rounded-2xl border border-red-100 bg-red-50 p-5"><h2 className="font-semibold text-red-800">{companyBlocked?"Application locked: excluded company":"This role was hard-rejected"}</h2><p className="mt-1 text-sm text-red-700">{companyBlocked?"You can inspect this job, but JobPilot will not open its apply URL, generate materials, create an application, or mark one as applied.":"Application actions are unavailable while this job is rejected."}</p><ul className="mt-3 space-y-1 text-sm text-red-700">{match.rejection_reasons.map(x=><li key={x}>• {x}</li>)}</ul></section>}
    {match.warnings.length>0&&<section className="mb-5 rounded-2xl border border-amber-100 bg-amber-50 p-4 text-sm text-amber-800"><strong>Check before applying:</strong> {match.warnings.join(" · ")}</section>}
    <div className="grid gap-5 lg:grid-cols-[1.2fr_.8fr]">
      <div className="space-y-5">
        <section className="card"><div className="flex items-start gap-5"><Score value={match.score} rejected={match.rejected}/><div><h2 className="font-semibold">{match.rejected?"Score retained for audit":"Why this matches"}</h2><ul className="mt-2 space-y-1 text-sm text-ink/65">{match.reasons.map(x=><li key={x}>• {x}</li>)}</ul></div></div><div className="mt-5 grid grid-cols-2 gap-2 sm:grid-cols-3">{Object.entries(match.breakdown).map(([key,value])=><div key={key} className="rounded-xl bg-paper p-3"><p className="text-lg font-semibold">{value}<span className="text-xs text-ink/40"> / {maximum(key)}</span></p><p className="text-xs text-ink/50">{labels[key]||key}</p></div>)}</div></section>
        <section className="card"><h2 className="font-semibold">Job description</h2><JobDescription text={job.description}/></section>
      </div>
      <aside className="space-y-5">
        <section className="card"><h2 className="font-semibold">Skill evidence</h2><p className="mt-3 text-xs font-bold uppercase tracking-wider text-emerald-700">Matched</p><div className="mt-2 flex flex-wrap gap-1">{match.matched_skills.length?match.matched_skills.map(x=><span className="rounded-full bg-emerald-50 px-2 py-1 text-xs text-emerald-800" key={x}>{x}</span>):<span className="muted">No explicit matches</span>}</div><p className="mt-4 text-xs font-bold uppercase tracking-wider text-amber-700">Missing</p><div className="mt-2 flex flex-wrap gap-1">{match.missing_skills.length?match.missing_skills.map(x=><span className="rounded-full bg-amber-50 px-2 py-1 text-xs text-amber-800" key={x}>{x}</span>):<span className="muted">None identified</span>}</div></section>
        <section className="card"><h2 className="font-semibold">Red flags</h2><ul className="mt-2 space-y-2 text-sm text-ink/65">{match.red_flags.length?match.red_flags.map(x=><li key={x}>• {x}</li>):<li>No automated red flags.</li>}</ul></section>
        {!match.rejected&&!job.closed_at&&<section className="card"><h2 className="font-semibold">Move this role forward</h2><p className="muted mt-1">Preparing an application assembles the CV, cover letter and every question on the employer's form, then holds it in the outbox for you to review. Nothing is sent.</p><div className="mt-4 grid gap-2"><button className="btn-primary" disabled={!!busy} onClick={prepare}>{busy==="prepare"?"Preparing…":"Prepare application"}</button><button className="btn" disabled={!!busy} onClick={generate}>{busy==="generate"?"Writing…":"Generate application text"}</button><button className="btn-secondary" disabled={!!busy} onClick={createApplication}>{busy==="application"?"Creating…":"Create application record"}</button></div></section>}
      </aside>
    </div>
    {docs&&<section className="mt-7"><p className="eyebrow">Deterministic templates</p><h2 className="mb-4 mt-1 text-2xl font-semibold">Application materials</h2><div className="space-y-4"><Document title="Recruiter message" text={docs.recruiter_message}/><Document title="Custom profile summary" text={docs.candidate_summary}/><Document title="Cover letter" text={docs.cover_letter}/><article className="card"><h3 className="font-semibold">Common questions</h3><div className="mt-4 divide-y divide-black/5">{Object.entries(docs.answers).map(([q,a])=><div className="py-4 first:pt-0" key={q}><div className="flex justify-between gap-3"><p className="text-sm font-semibold">{q}</p><CopyButton text={a}/></div><p className="mt-2 text-sm leading-6 text-ink/70">{a}</p></div>)}</div></article></div></section>}
  </Shell>
}
function Document({title,text}:{title:string;text:string}){return <article className="card"><div className="flex justify-between gap-3"><h3 className="font-semibold">{title}</h3><CopyButton text={text}/></div><p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-ink/70">{text}</p></article>}

function hostOf(link:string){try{return new URL(link).hostname.replace(/^www\./,"")}catch{return link}}
