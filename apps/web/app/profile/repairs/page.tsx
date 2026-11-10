"use client";
import {useEffect,useState} from "react";
import {api} from "@/lib/api";
import {CVDocument,ProfileRepair,ProfileRepairSummary} from "@/lib/types";
import {Shell} from "@/components/Shell";
import {ProfileNav} from "@/components/ProfileNav";
import {ProfileRepairReview} from "@/components/ProfileRepairReview";
import {Header,ErrorMessage,Notice} from "@/components/UI";

export default function ProfileRepairs(){
 const [items,setItems]=useState<ProfileRepairSummary[]>([]),[cvs,setCVs]=useState<CVDocument[]>([]),[source,setSource]=useState(''),[open,setOpen]=useState<ProfileRepair|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[reviewed,setReviewed]=useState(false);
 const load=()=>api<ProfileRepairSummary[]>('/profile/repairs').then(setItems);
 useEffect(()=>{Promise.all([load(),api<CVDocument[]>('/cv').then(v=>setCVs(v.filter(c=>c.kind==='base')))]).catch(e=>setError(e.message))},[]);
 const show=async(id:string)=>{setBusy(true);setError('');setReviewed(false);try{setOpen(await api<ProfileRepair>(`/profile/repairs/${id}`))}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const propose=async()=>{setBusy(true);setError('');setNotice('');setReviewed(false);try{setOpen(await api<ProfileRepair>('/profile/repairs',{method:'POST',body:JSON.stringify({source_cv_id:source})}));await load()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const decide=async(decision:'apply'|'reject'|'restore')=>{
  if(!open)return;setBusy(true);setError('');setNotice('');
  try{const next=await api<ProfileRepair>(`/profile/repairs/${open.id}/decision`,{method:'POST',body:JSON.stringify({decision,expected_fingerprint:decision==='restore'?open.applied_fingerprint:open.before_fingerprint})});setOpen(next);setReviewed(false);setNotice(decision==='apply'?'Repair applied. The full previous state is retained.':decision==='restore'?'Previous profile restored. Both snapshots remain available.':'Proposal rejected; profile unchanged.');await load()}catch(e){setError((e as Error).message)}finally{setBusy(false)}
 };
 return <Shell><Header eyebrow="Profile" title="Review profile repairs" description="Propose corrections from an existing stored CV, review every replacement, then apply or reject. Nothing is sent."/><ProfileNav/><ErrorMessage message={error}/><Notice message={notice}/>
 <div className="card mb-5 space-y-3"><label className="block">Source CV <select className="input" value={source} onChange={e=>setSource(e.target.value)} disabled={busy}><option value="">Choose a stored base CV</option>{cvs.map(c=><option value={c.id} key={c.id}>{c.label||c.filename}</option>)}</select></label><button className="btn-secondary" disabled={busy||!source} onClick={propose}>Create review proposal</button><p className="muted text-sm">Proposals keep the stored parse as it is. Upload a corrected CV first if its parse is wrong. A proposal does not approve that CV.</p></div>
 <div className="mb-5 flex flex-wrap gap-2">{items.map(i=><button className="btn-secondary" key={i.id} disabled={busy} onClick={()=>show(i.id)}>{new Date(i.created_at).toLocaleString()} · {i.status}{i.proposed_by==='agent'?' · by an agent':''}</button>)}</div>
 {open&&<div className="card"><ProfileRepairReview proposal={open}/>{(open.status==='pending'||open.status==='applied')&&<div className="mt-6 space-y-3"><label className="flex gap-2"><input type="checkbox" checked={reviewed} onChange={e=>setReviewed(e.target.checked)} disabled={busy}/>I reviewed the source, warnings, before/after values and retained exclusions{open.status==='applied'?' and want to restore the full previous state':''}.</label><div className="flex gap-3">{open.status==='pending'?<><button className="btn-primary" disabled={busy||!reviewed} onClick={()=>decide('apply')}>Apply reviewed repair</button><button className="btn-secondary" disabled={busy} onClick={()=>decide('reject')}>Reject proposal</button></>:<button className="btn-secondary" disabled={busy||!reviewed} onClick={()=>decide('restore')}>Restore full previous profile</button>}</div><p className="muted text-sm">Apply and restore refuse if the expected state changed. After later edits, create a fresh proposal.</p></div>}</div>}
 </Shell>
}
