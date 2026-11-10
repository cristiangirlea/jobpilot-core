"use client";

import Link from "next/link";
import {useEffect,useState} from "react";
import {api} from "@/lib/api";
import {OutboxEntry} from "@/lib/types";
import {ApplicationHistory} from "@/components/ApplicationHistory";
import {Shell} from "@/components/Shell";
import {ApprovalChip,Empty,ErrorMessage,Header,Notice} from "@/components/UI";

const statusTone:Record<string,string>={
  needs_review:"bg-amber-50 text-amber-700",ready:"bg-sky-50 text-sky-700",
  approved:"bg-sage text-moss",sending:"bg-sage text-moss",sent:"bg-emerald-50 text-emerald-700",
  failed:"bg-red-50 text-red-700",cancelled:"bg-black/5 text-ink/50",
};

const sourceLabel:Record<string,string>={
  profile:"from your profile",bank:"reused answer",bank_similar:"reused from a similar role",
  ai:"drafted by AI",template:"deterministic template",unanswered:"needs you",
  cv:"CV attached",cover_letter:"letter attached",declaration:"you declare this",upload:"you upload this",personal:"only you choose this",
};

export default function OutboxPage(){
  const [items,setItems]=useState<OutboxEntry[]>([]);
  const [open,setOpen]=useState<OutboxEntry|null>(null);
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const [busy,setBusy]=useState(false);
  // Whether this server sends approved applications, as its health says.
  const [sending,setSending]=useState(false);

  const load=async()=>{try{setItems(await api<OutboxEntry[]>("/outbox"))}catch(e){setError((e as Error).message)}};
  useEffect(()=>{load();api<{sending?:boolean}>("/health").then(h=>setSending(!!h.sending)).catch(()=>{})},[]);

  const act=async(entry:OutboxEntry,action:"approve"|"cancel")=>{
    setBusy(true);setError("");setNotice("");
    try{
      const updated=await api<OutboxEntry>(`/outbox/${entry.id}/${action}`,{method:"POST",body:"{}"});
      setOpen(updated);await load();
      setNotice(action==="approve"
        ?(sending?"Approved. It goes to the employer's form in a few minutes; Remove it before then to stop it.":"Approved. This server does not send applications, so nothing has been sent.")
        :"Removed from the queue.");
    }catch(e){setError((e as Error).message)}finally{setBusy(false)}
  };

  // Approving a CV clears its blocker on every application carrying it, so the
  // entry is only read back, not prepared again.
  const approveCV=async(entry:OutboxEntry)=>{
    if(!entry.cv_document_id)return;
    setBusy(true);setError("");setNotice("");
    try{
      await api(`/cv/${entry.cv_document_id}/approve`,{method:"POST",body:JSON.stringify({approved:true})});
      setNotice("CV approved. Applications carrying it no longer wait on it.");
      setOpen(await api<OutboxEntry>(`/outbox/${entry.id}`));await load();
    }catch(e){setError((e as Error).message)}finally{setBusy(false)}
  };

  // A handed-back application was not sent: the user applies on the
  // employer's form and records it here.
  const appliedByHand=async(entry:OutboxEntry)=>{
    if(!entry.application_id)return;
    setBusy(true);setError("");setNotice("");
    try{
      await api(`/applications/${entry.application_id}`,{method:"PATCH",body:JSON.stringify({status:"applied"})});
      setNotice("Recorded as applied.");
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
          {open&&<Detail entry={open} busy={busy} sending={sending} onAct={act} onApproveCV={approveCV} onApplied={appliedByHand}/>}
        </div>
      </>}
  </Shell>;
}

function Detail({entry,busy,sending,onAct,onApproveCV,onApplied}:{entry:OutboxEntry;busy:boolean;sending:boolean;
  onAct:(entry:OutboxEntry,action:"approve"|"cancel")=>void;onApproveCV:(entry:OutboxEntry)=>void;onApplied:(entry:OutboxEntry)=>void}){
  const blocked=entry.blockers.length>0;
  // Sent, cancelled and in-flight entries take no action; a failed one is
  // prepared again from its job.
  const done=entry.status==="sent"||entry.status==="cancelled"||entry.status==="sending";
  // Feed links are not trusted: only a web page becomes a link.
  const link=entry.job?.employer_url||entry.job?.apply_url||"";
  const form=/^https?:\/\//i.test(link)?link:"";
  return <div className="card space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 className="text-lg font-semibold">{entry.job?.title}</h2>
        <p className="muted text-sm">{entry.job?.company}{entry.job?.location?` · ${entry.job.location}`:""}</p>
      </div>
      <div className="flex gap-2">
        {!done&&<button className="btn-secondary !py-2" disabled={busy} onClick={()=>onAct(entry,"cancel")}>Remove</button>}
        {!done&&entry.status!=="failed"&&<button className="btn-primary !py-2" disabled={busy||blocked} title={blocked?"Clear the items below first":""}
          onClick={()=>onAct(entry,"approve")}>{entry.status==="approved"?"Approved":"Approve"}</button>}
      </div>
    </div>

    <ApplicationHistory history={entry.job?.application_history}/>

    {blocked&&<div className="rounded-xl bg-amber-50 px-4 py-3">
      <p className="text-sm font-semibold text-amber-900">Needs you before this can be approved</p>
      <ul className="mt-1 space-y-1 text-sm text-amber-800">{entry.blockers.map(b=><li key={b}>· {b}</li>)}</ul>
    </div>}

    {entry.notices.length>0&&<div className="rounded-xl bg-black/[0.03] px-4 py-3">
      <p className="text-sm font-semibold">Worth knowing</p>
      <ul className="muted mt-1 space-y-1 text-sm">{entry.notices.map(n=><li key={n}>· {n}</li>)}</ul>
    </div>}

    {entry.status==="approved"&&<div className="rounded-xl bg-sage px-4 py-3 text-sm text-moss">
      {sending
        ?"Approved. It goes to the employer's form a few minutes after approval; Remove it before then to stop it."
        :"Approved and queued. This server does not send applications, so nothing has left your machine."}
    </div>}

    {entry.status==="sending"&&<div className="rounded-xl bg-sage px-4 py-3 text-sm text-moss">
      Being sent to the employer's form now.
    </div>}

    {entry.status==="sent"&&<div className="rounded-xl bg-emerald-50 px-4 py-3 text-sm text-emerald-800">
      Sent through the employer's form{entry.sent_at&&` on ${new Date(entry.sent_at).toLocaleString()}`}. It is recorded as applied.
    </div>}

    {entry.status==="failed"&&<div className="rounded-xl bg-red-50 px-4 py-3">
      <p className="text-sm font-semibold text-red-900">Back with you: finish this one on the employer's site</p>
      {entry.error&&<p className="mt-1 text-sm text-red-800">{entry.error}</p>}
      <div className="mt-3 flex flex-wrap gap-2">
        {form&&<a className="btn-primary !py-2" href={form} target="_blank" rel="noopener noreferrer">Open the employer's form</a>}
        {entry.application_id&&<button className="btn-secondary !py-2" disabled={busy} onClick={()=>onApplied(entry)}>I applied</button>}
      </div>
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

// Where the CV came from. Whether it is approved now is the chip's to say: an
// approval can be withdrawn after the application was prepared.
const decisionLabel:Record<string,string>={
  base:"Your uploaded CV, sent as it is",
  reused:"A tailored CV kept from before",
  generated:"Tailored for this role",
};

// CVCard names the CV this application carries and how well it covers the
// job, with a way to read it in full and, when it is still a draft, approve it.
// The entry carries the CV's summary as it is now, so nothing more is fetched.
function CVCard({entry,busy,done,onApprove}:{entry:OutboxEntry;busy:boolean;done:boolean;onApprove:(entry:OutboxEntry)=>void}){
  const cv=entry.cv;
  const missing=cv?.missing??[];
  return <div className="rounded-xl border border-black/10 px-4 py-3">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h3 className="font-semibold">CV</h3>
        <p className="muted text-sm">{decisionLabel[entry.cv_decision||""]||"Attached CV"}{cv&&` · ${cv.filename}`}</p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {cv&&<ApprovalChip approved={cv.approved}/>}
        <Link className="btn-secondary !py-2" href={`/cv?open=${entry.cv_document_id}`}>Preview</Link>
        {cv&&!cv.approved&&!done&&<button className="btn-primary !py-2" disabled={busy} onClick={()=>onApprove(entry)}>Approve CV</button>}
      </div>
    </div>
    {cv?.alignment!==undefined&&<p className="muted mt-2 text-xs">
      Covers {Math.round(cv.alignment*100)}% of the job's technologies.
      {missing.length>0&&` Not on your CV, so not added: ${missing.join(", ")}.`}</p>}
  </div>;
}
