"use client";

import {useEffect,useState} from "react";
import {api} from "@/lib/api";
import {AnswerEntry,AnswerScope,backupDraftLabel} from "@/lib/types";
import {Shell} from "@/components/Shell";
import {Empty,ErrorMessage,Header,Notice} from "@/components/UI";

const sourceLabel:Record<string,string>={human:"you wrote this",ai:"drafted by AI",ai_backup:backupDraftLabel,deterministic:"from your profile"};

// Where an approved answer is reused. The narrowest one that matches a job wins.
const scopeLabel=(item:AnswerEntry,scope:AnswerScope=item.scope)=>
  scope==="global"?"every job"
  :scope==="job"?"this job only"
  :item.role_family&&item.role_family!=="general"?`${item.role_family} roles`:"any role";

export default function AnswersPage(){
  const [items,setItems]=useState<AnswerEntry[]>([]);
  const [filter,setFilter]=useState<"pending"|"approved"|"all">("pending");
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const [adding,setAdding]=useState(false);

  const load=async(status=filter)=>{
    try{setItems(await api<AnswerEntry[]>(`/answers?status=${status==="all"?"":status}`))}
    catch(e){setError((e as Error).message)}
  };
  useEffect(()=>{load(filter)},[filter]);

  const pendingCount=items.filter(item=>!item.approved).length;

  return <Shell>
    <Header eyebrow="Applications" title="Answers"
      description="Answers you approve here are reused automatically for the same question on similar roles, so each one is only ever written once."
      action={<button className="btn-primary" onClick={()=>setAdding(true)}>Add an answer</button>}/>
    <ErrorMessage message={error}/><Notice message={notice}/>

    <div className="mb-4 flex gap-2">
      {(["pending","approved","all"] as const).map(name=>
        <button key={name} className={`rounded-lg px-3 py-1.5 text-xs font-semibold ${filter===name?"bg-ink text-white":"bg-black/5"}`}
          onClick={()=>setFilter(name)}>
          {name==="pending"?"Needs review":name==="approved"?"Approved":"All"}
          {name==="pending"&&filter==="pending"&&pendingCount>0?` (${pendingCount})`:""}
        </button>)}
    </div>

    {adding&&<Editor onCancel={()=>setAdding(false)}
      onSaved={message=>{setAdding(false);setNotice(message);load()}}
      onError={setError}/>}

    {items.length===0&&!adding
      ? <Empty title={filter==="pending"?"Nothing waiting":"No answers yet"}
          body={filter==="pending"
            ?"Answers drafted for you appear here for approval. Until you approve one, it is never sent to an employer."
            :"When an application asks something your profile does not already answer, it lands here."}/>
      : <div className="space-y-2">{items.map(item=>
          <Row key={item.id} item={item} onChanged={message=>{setNotice(message);load()}} onError={setError}/>)}</div>}
  </Shell>;
}

function Row({item,onChanged,onError}:{item:AnswerEntry;onChanged:(message:string)=>void;onError:(message:string)=>void}){
  const [editing,setEditing]=useState(false);
  const [draft,setDraft]=useState(item.answer);
  const [scope,setScope]=useState<AnswerScope>(item.scope||"role_family");
  const [busy,setBusy]=useState(false);
  // "This job only" needs the job the answer was written for.
  const scopes:AnswerScope[]=item.job_id?["job","role_family","global"]:["role_family","global"];

  const save=async(approved:boolean)=>{
    setBusy(true);
    try{
      await api(`/answers/${item.id}`,{method:"PATCH",body:JSON.stringify({answer:draft,approved,scope})});
      setEditing(false);
      onChanged(approved?`Approved. It will be reused for this question on ${scopeLabel(item,scope)}.`:"Saved without approving.");
    }catch(e){onError((e as Error).message)}finally{setBusy(false)}
  };

  const remove=async()=>{
    if(!confirm("Delete this answer?"))return;
    try{await api(`/answers/${item.id}`,{method:"DELETE"});onChanged("Deleted.")}
    catch(e){onError((e as Error).message)}
  };

  return <div className="card !py-4">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0 flex-1">
        <p className="text-sm font-semibold">{item.question_text}</p>
        <p className="muted mt-0.5 text-xs">
          {scopeLabel(item)}
          {" · "}{sourceLabel[item.source]||item.source}
          {item.usage_count>0&&` · used ${item.usage_count}×`}
          {item.technologies?.length>0&&` · ${item.technologies.slice(0,5).join(", ")}`}
        </p>
      </div>
      <span className={`shrink-0 rounded-lg px-2 py-1 text-[10px] font-bold ${item.approved?"bg-sage text-moss":"bg-amber-50 text-amber-700"}`}>
        {item.approved?"APPROVED":"NEEDS REVIEW"}</span>
    </div>

    {(editing||!item.approved)&&<label className="mt-2 flex items-center gap-2 text-xs">Reuse for
      <select className="!w-auto !py-1 text-xs" value={scope} onChange={e=>setScope(e.target.value as AnswerScope)}>
        {scopes.map(name=><option key={name} value={name}>{scopeLabel(item,name)}</option>)}
      </select></label>}

    {editing
      ? <div className="mt-3">
          <textarea rows={5} value={draft} onChange={e=>setDraft(e.target.value)}/>
          <div className="mt-2 flex flex-wrap gap-2">
            <button className="btn-primary !py-2" disabled={busy} onClick={()=>save(true)}>Save and approve</button>
            <button className="btn-secondary !py-2" disabled={busy} onClick={()=>save(false)}>Save only</button>
            <button className="btn-secondary !py-2" disabled={busy} onClick={()=>{setDraft(item.answer);setScope(item.scope||"role_family");setEditing(false)}}>Cancel</button>
          </div>
        </div>
      : <div className="mt-2">
          <p className="whitespace-pre-wrap text-sm leading-relaxed">{item.answer}</p>
          <div className="mt-2 flex flex-wrap gap-2">
            <button className="btn-secondary !py-1.5 text-xs" onClick={()=>setEditing(true)}>Edit</button>
            {!item.approved&&<button className="btn-primary !py-1.5 text-xs" disabled={busy} onClick={()=>save(true)}>Approve</button>}
            <button className="btn-secondary !py-1.5 text-xs !text-red-700" onClick={remove}>Delete</button>
          </div>
        </div>}
  </div>;
}

function Editor({onSaved,onCancel,onError}:{onSaved:(message:string)=>void;onCancel:()=>void;onError:(message:string)=>void}){
  const [question,setQuestion]=useState("");
  const [answer,setAnswer]=useState("");
  // No role family: a hand-written answer is filed under "general", which
  // serves any role after the role's own answers; "every job" comes last.
  const [scope,setScope]=useState<AnswerScope>("role_family");
  const [busy,setBusy]=useState(false);

  const save=async()=>{
    setBusy(true);
    try{
      await api("/answers",{method:"POST",body:JSON.stringify({question_text:question,answer,approved:true,scope})});
      onSaved("Saved and approved.");
    }catch(e){onError((e as Error).message)}finally{setBusy(false)}
  };

  return <div className="card mb-4">
    <h2 className="font-semibold">Add an answer</h2>
    <p className="muted mt-1 text-sm">Paste the question exactly as the employer asks it. Differently worded versions of the same question will still find it.</p>
    <div className="mt-4 space-y-3">
      <div><label>Question</label>
        <input value={question} onChange={e=>setQuestion(e.target.value)} placeholder="Why do you want to work here?"/></div>
      <div><label>Your answer</label>
        <textarea rows={5} value={answer} onChange={e=>setAnswer(e.target.value)}/></div>
      <div><label>Reuse for</label>
        <select value={scope} onChange={e=>setScope(e.target.value as AnswerScope)}>
          <option value="role_family">any role, after answers for that kind of role</option>
          <option value="global">every job</option>
        </select></div>
      <div className="flex gap-2">
        <button className="btn-primary !py-2" disabled={busy||!question.trim()||!answer.trim()} onClick={save}>Save and approve</button>
        <button className="btn-secondary !py-2" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  </div>;
}
