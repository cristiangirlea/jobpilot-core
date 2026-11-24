"use client";
import {useParams} from "next/navigation";
import {useEffect,useState} from "react";
import {OpenApplyButton} from "@/components/OpenApplyButton";
import {ApplicationHistory} from "@/components/ApplicationHistory";
import {Shell} from "@/components/Shell";
import {CopyButton,ErrorMessage,Field,Header} from "@/components/UI";
import {api} from "@/lib/api";
import {Application,Documents,FollowUpDraft} from "@/lib/types";

type Detail={application:Application;documents:Documents;follow_ups:FollowUpDraft[];blocked:boolean;block_reasons:string[]};
const statuses=["draft","ready","applied","interview","rejected","offer","withdrawn"];
export default function ApplicationDetail(){
  const {id}=useParams<{id:string}>();
  const [data,setData]=useState<Detail|null>(null),[status,setStatus]=useState("draft"),[notes,setNotes]=useState(""),[error,setError]=useState(""),[saved,setSaved]=useState("");
  const load=()=>api<Detail>(`/applications/${id}`).then(x=>{setData(x);setStatus(x.application.status);setNotes(x.application.notes)}).catch(e=>setError(e.message));
  useEffect(()=>{load()},[id]);
  async function save(){setError("");try{await api(`/applications/${id}`,{method:"PATCH",body:JSON.stringify({status,notes})});setSaved("Application updated.");load()}catch(e){setError(e instanceof Error?e.message:"Unable to update")}}
  const [drafting,setDrafting]=useState(false);
  const [draftSources,setDraftSources]=useState<Record<string,string|undefined>>({});
  async function draftFollowUp(){
    setError("");setDrafting(true);
    try{
      const draft=await api<FollowUpDraft>(`/applications/${id}/follow-up`,{method:"POST"});
      setDraftSources(previous=>({...previous,[draft.id]:draft.source}));
      setSaved("Follow-up saved as a draft. Nothing was sent.");
      load();
    }catch(e){setError(e instanceof Error?e.message:"Unable to draft follow-up")}finally{setDrafting(false)}
  }
  async function dismissQuiet(){setError("");try{await api(`/applications/${id}/dismiss-follow-up`,{method:"POST"});setSaved("Quiet application reminder dismissed.")}catch(e){setError(e instanceof Error?e.message:"Unable to dismiss reminder")}}
  if(!data)return <Shell><ErrorMessage message={error}/><div className="card">Loading application…</div></Shell>;
  const a=data.application,d=data.documents;
  return <Shell>
    <Header eyebrow="Application record" title={a.job?.title||"Application"} description={`${a.job?.company||""} · created ${new Date(a.created_at).toLocaleDateString()}`} action={!data.blocked&&!a.job?.closed_at&&a.job?.apply_url?<OpenApplyButton jobId={a.job_id}/>:undefined}/>
    <ApplicationHistory history={a.job?.application_history}/>
    {a.job?.closed_at&&<section className="mb-5 rounded-2xl border border-slate-200 bg-slate-50 p-4 text-sm text-slate-700"><strong>This posting has closed</strong> ({new Date(a.job.closed_at).toLocaleDateString()}){a.job.closed_reason?`: ${a.job.closed_reason}`:""}. The application stays in your history; other people no longer see the job.</section>}
    <ErrorMessage message={error}/>{saved&&<p className="mb-4 text-sm text-emerald-700">{saved}</p>}
    <section className="card mb-5 space-y-4"><h2 className="text-lg font-semibold">Follow-up drafts</h2><p className="muted text-sm">Drafts for you to review and copy. Nothing is sent. At most two drafts per application.</p>{(data.follow_ups||[]).map((draft,index)=><Doc key={draft.id} title={`Follow-up ${index+1} · ${new Date(draft.created_at).toLocaleDateString()}`} text={draft.body} source={draftSources[draft.id] ?? draft.source}/>)}{["applied","interview"].includes(a.status)&&<div className="flex flex-wrap gap-3"><button className="btn" onClick={draftFollowUp} disabled={drafting||data.blocked||(data.follow_ups||[]).length>=2}>{drafting?"Drafting…":"Draft follow-up"}</button><button className="btn" onClick={dismissQuiet}>Dismiss quiet reminder</button></div>}</section>
    {data.blocked&&<div className="mb-5 rounded-2xl border border-red-100 bg-red-50 p-4 text-sm text-red-800"><strong>Application locked.</strong> This job is now rejected, so its apply link is disabled and it cannot be newly marked as applied. Existing history and documents remain available.<div className="mt-1 text-xs">{data.block_reasons.join(" · ")}</div></div>}
    <div className="grid gap-5 lg:grid-cols-[.7fr_1.3fr]">
      <aside className="card h-fit"><h2 className="mb-4 font-semibold">Track progress</h2><Field label="Status"><select value={status} onChange={e=>setStatus(e.target.value)}>{statuses.map(x=><option key={x} disabled={data.blocked&&x==="applied"&&a.status!=="applied"}>{x}</option>)}</select></Field><Field label="Notes" className="mt-4"><textarea value={notes} onChange={e=>setNotes(e.target.value)} placeholder="Contacts, interview details, follow-ups…"/></Field><button className="btn mt-4 w-full" onClick={save}>Save application</button></aside>
      <section className="space-y-4"><h2 className="text-lg font-semibold">Saved materials</h2>{d.recruiter_message?<><Doc title="Recruiter message" text={d.recruiter_message}/><Doc title="Candidate summary" text={d.candidate_summary}/><Doc title="Cover letter" text={d.cover_letter}/><article className="card"><h3 className="font-semibold">Common answers</h3>{Object.entries(d.answers||{}).map(([q,v])=><div className="mt-4 border-t border-black/5 pt-4" key={q}><div className="flex justify-between gap-2"><p className="text-sm font-semibold">{q}</p><CopyButton text={v}/></div><p className="muted mt-2 leading-6">{v}</p></div>)}</article></>:<div className="card muted">No documents saved yet. Return to the job and generate application text.</div>}</section>
    </div>
  </Shell>
}
function Doc({title,text,source}:{title:string;text:string;source?:string}){return <article className="card"><div className="flex justify-between"><h3 className="font-semibold">{title}</h3><CopyButton text={text}/></div>
  {source==="ai_backup"&&<p className="muted text-sm">Written by the backup AI model; read it closely before you use it.</p>}
  {source==="ai"&&<p className="muted text-sm">Written by AI from your sent materials.</p>}
  <p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-ink/70">{text}</p></article>}
