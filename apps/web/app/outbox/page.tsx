"use client";

import Link from "next/link";
import {useEffect,useState} from "react";
import {api} from "@/lib/api";
import {CVDocument,OutboxEntry} from "@/lib/types";
import {Shell} from "@/components/Shell";
import {Empty,ErrorMessage,Header,Notice} from "@/components/UI";

const statusTone:Record<string,string>={
  needs_review:"bg-amber-50 text-amber-700",ready:"bg-sky-50 text-sky-700",
  approved:"bg-sage text-moss",sent:"bg-emerald-50 text-emerald-700",
  failed:"bg-red-50 text-red-700",cancelled:"bg-black/5 text-ink/50",
};

const sourceLabel:Record<string,string>={
  profile:"from your profile",bank:"reused answer",bank_similar:"reused from a similar role",
  ai:"drafted by AI",template:"deterministic template",unanswered:"needs you",
  cv:"CV attached",cover_letter:"letter attached",
};

export default function OutboxPage(){
  const [items,setItems]=useState<OutboxEntry[]>([]);
  const [open,setOpen]=useState<OutboxEntry|null>(null);
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const [busy,setBusy]=useState(false);

  const load=async()=>{try{setItems(await api<OutboxEntry[]>("/outbox"))}catch(e){setError((e as Error).message)}};
  useEffect(()=>{load()},[]);

  const act=async(entry:OutboxEntry,action:"approve"|"cancel")=>{
    setBusy(true);setError("");setNotice("");
    try{
      const updated=await api<OutboxEntry>(`/outbox/${entry.id}/${action}`,{method:"POST",body:"{}"});
      setOpen(updated);await load();
      setNotice(action==="approve"
        ?"Approved. It is queued for sending; nothing has been sent yet."
        :"Removed from the queue.");
    }catch(e){setError((e as Error).message)}finally{setBusy(false)}
  };

  // Approving the CV alone leaves its blocker on the stored entry, so the
  // application is prepared again: the approved CV is then reused as it is.
  const approveCV=async(entry:OutboxEntry)=>{
    if(!entry.cv_document_id)return;
    setBusy(true);setError("");setNotice("");
    try{
      await api(`/cv/${entry.cv_document_id}/approve`,{method:"POST",body:JSON.stringify({approved:true})});
      const updated=await api<OutboxEntry>(`/jobs/${entry.job_id}/compose`,{method:"POST",body:"{}"});
      setOpen(updated);await load();
      setNotice(`CV approved, and the application was prepared again with it.${updated.cover_letter_source==="ai"?" The cover letter was drafted again, so read it before approving.":""}`);
    }catch(e){setError((e as Error).message)}finally{setBusy(false)}
  };

  const waiting=items.filter(item=>item.status==="needs_review").length;

  return <Shell>
    <Header eyebrow="Applications" title="Outbox"
      description="Every application is composed in full and held here. You see exactly what an employer would receive before anything is released."/>
    <ErrorMessage message={error}/><Notice message={notice}/>

    {items.length===0
      ? <Empty title="Nothing queued" body="Open a matched job and choose Prepare application. The CV, cover letter and every form answer are assembled here for you to review."
          action={<Link className="btn-primary" href="/matches">Go to matches</Link>}/>
      : <>
        {waiting>0&&<p className="muted mb-4 text-sm">{waiting} of {items.length} {waiting===1?"application needs":"applications need"} something from you before it can be approved.</p>}
        <div className="grid gap-4 lg:grid-cols-[360px_1fr]">
          <div className="space-y-2">
            {items.map(item=>
              <button key={item.id} onClick={()=>setOpen(item)}
                className={`card w-full text-left !py-3 ${open?.id===item.id?"ring-2 ring-moss":""}`}>
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-semibold">{item.job?.title||"Job"}</p>
                    <p className="muted truncate text-xs">{item.job?.company}</p>
                  </div>
                  <span className={`shrink-0 rounded-lg px-2 py-1 text-[10px] font-bold ${statusTone[item.status]||"bg-black/5"}`}>
                    {item.status.replace("_"," ").toUpperCase()}</span>
                </div>
                <p className="muted mt-1.5 text-xs">
                  {item.channel==="ats_form"?`${item.ats_provider} form`:item.channel==="email"?"email":"apply by hand"}
                  {item.blockers.length>0&&` · ${item.blockers.length} to fix`}</p>
              </button>)}
          </div>
          {open&&<Detail entry={open} busy={busy} onAct={act} onApproveCV={approveCV}/>}
        </div>
      </>}
  </Shell>;
}

function Detail({entry,busy,onAct,onApproveCV}:{entry:OutboxEntry;busy:boolean;
  onAct:(entry:OutboxEntry,action:"approve"|"cancel")=>void;onApproveCV:(entry:OutboxEntry)=>void}){
  const blocked=entry.blockers.length>0;
  const done=entry.status==="sent"||entry.status==="cancelled";
  return <div className="card space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 className="text-lg font-semibold">{entry.job?.title}</h2>
        <p className="muted text-sm">{entry.job?.company}{entry.job?.location?` · ${entry.job.location}`:""}</p>
      </div>
      <div className="flex gap-2">
        {!done&&<button className="btn-secondary !py-2" disabled={busy} onClick={()=>onAct(entry,"cancel")}>Remove</button>}
        {!done&&<button className="btn-primary !py-2" disabled={busy||blocked} title={blocked?"Clear the items below first":""}
          onClick={()=>onAct(entry,"approve")}>{entry.status==="approved"?"Approved":"Approve"}</button>}
      </div>
    </div>

    {blocked&&<div className="rounded-xl bg-amber-50 px-4 py-3">
      <p className="text-sm font-semibold text-amber-900">Needs you before this can be approved</p>
      <ul className="mt-1 space-y-1 text-sm text-amber-800">{entry.blockers.map(b=><li key={b}>· {b}</li>)}</ul>
    </div>}

    {entry.notices.length>0&&<div className="rounded-xl bg-black/[0.03] px-4 py-3">
      <p className="text-sm font-semibold">Worth knowing</p>
      <ul className="muted mt-1 space-y-1 text-sm">{entry.notices.map(n=><li key={n}>· {n}</li>)}</ul>
    </div>}

    {entry.status==="approved"&&<div className="rounded-xl bg-sage px-4 py-3 text-sm text-moss">
      Approved and queued. Sending is not built yet, so nothing has left your machine.
    </div>}

    {entry.cv_document_id&&<CVCard entry={entry} busy={busy} done={done} onApprove={onApproveCV}/>}

    <div>
      <div className="flex items-baseline justify-between">
        <h3 className="font-semibold">Cover letter</h3>
        <span className="muted text-xs">{entry.cover_letter_source==="ai"?"written by AI from your stored facts":"deterministic template"}</span>
      </div>
      <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap rounded-xl bg-black/[0.03] p-4 text-sm leading-relaxed">{entry.cover_letter}</pre>
    </div>

    {entry.answers.length>0&&<div>
      <div className="flex items-baseline justify-between">
        <h3 className="font-semibold">Application form</h3>
        <span className="muted text-xs">{entry.answers.filter(a=>!a.needs_review).length} of {entry.answers.length} resolved</span>
      </div>
      <div className="mt-2 divide-y divide-black/5">
        {entry.answers.map(answer=>
          <div key={answer.field} className="flex flex-wrap items-start gap-x-4 gap-y-1 py-2">
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium">{answer.label}{answer.required&&<span className="text-red-600"> *</span>}</p>
              <p className={`text-sm ${answer.answer?"":"italic text-amber-700"}`}>{answer.answer||"no answer yet"}</p>
            </div>
            <span className={`shrink-0 rounded-lg px-2 py-0.5 text-[10px] font-semibold ${answer.needs_review?"bg-amber-50 text-amber-700":"bg-black/5 text-ink/50"}`}>
              {sourceLabel[answer.source]||answer.source}</span>
          </div>)}
      </div>
      <p className="muted mt-3 text-xs">
        Answer anything marked “needs you” on the <Link className="underline" href="/answers">Answers</Link> page, then prepare this application again. Approved answers are reused for similar roles.
      </p>
    </div>}
  </div>;
}

const decisionLabel:Record<string,string>={
  base:"Your base CV, sent as it is",
  reused:"A tailored CV you approved for a similar role",
  generated:"Tailored for this role",
};

// CVCard names the CV this application carries and how well it covers the
// job, with a way to read it in full and, when it is still a draft, approve it.
function CVCard({entry,busy,done,onApprove}:{entry:OutboxEntry;busy:boolean;done:boolean;onApprove:(entry:OutboxEntry)=>void}){
  const [cv,setCV]=useState<CVDocument|null>(null);
  useEffect(()=>{
    setCV(null);
    api<CVDocument>(`/cv/${entry.cv_document_id}`).then(setCV).catch(()=>{});
  },[entry.cv_document_id,entry.updated_at]);
  const tailored=cv?.parsed?.tailored;
  const missing=tailored?.missing??[];
  return <div className="rounded-xl border border-black/10 px-4 py-3">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h3 className="font-semibold">CV</h3>
        <p className="muted text-sm">{decisionLabel[entry.cv_decision||""]||"Attached CV"}{cv&&` · ${cv.filename}`}</p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {cv&&<span className={`rounded-lg px-2 py-1 text-[10px] font-bold ${cv.approved?"bg-sage text-moss":"bg-amber-50 text-amber-700"}`}>
          {cv.approved?"APPROVED":"NEEDS REVIEW"}</span>}
        <Link className="btn-secondary !py-2" href={`/cv?open=${entry.cv_document_id}`}>Preview</Link>
        {cv&&!cv.approved&&!done&&<button className="btn-primary !py-2" disabled={busy} onClick={()=>onApprove(entry)}>Approve CV</button>}
      </div>
    </div>
    {tailored&&<p className="muted mt-2 text-xs">
      Covers {Math.round(tailored.alignment*100)}% of the job's technologies.
      {missing.length>0&&` Not on your CV, so not added: ${missing.join(", ")}.`}</p>}
  </div>;
}
