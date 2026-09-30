"use client";

import Link from "next/link";
import {useEffect,useRef,useState} from "react";
import {apiPage,jobHref} from "@/lib/api";
import {JobMatch} from "@/lib/types";
import {Empty,ErrorMessage,Score} from "./UI";

// The list is searched, filtered and paged on the server: a whole list was
// 14.4 MB on a full account. Summaries leave the descriptions out.
const PAGE=50;

function listPath(matchesOnly:boolean,search:string,relatedOnly:boolean,offset:number){
  const query=new URLSearchParams({open:"true",fields:"summary",limit:String(PAGE),offset:String(offset)});
  if(search)query.set("q",search);
  if(relatedOnly){query.set("state","accepted");query.set("min_score","70")}
  return `${matchesOnly?"/matches":"/jobs"}?${query}`;
}

export function JobList({matchesOnly=false}:{matchesOnly?:boolean}){
  const [items,setItems]=useState<JobMatch[]>([]);
  const [total,setTotal]=useState(0);
  const [error,setError]=useState("");
  const [loading,setLoading]=useState(true);
  const [more,setMore]=useState(false);
  const [technology,setTechnology]=useState("");
  const [search,setSearch]=useState("");
  const [relatedOnly,setRelatedOnly]=useState(false);
  // Each request is numbered, so a slow answer to an older search cannot
  // replace the list a newer one already filled.
  const latest=useRef(0);
  // Search once typing pauses rather than on every key.
  useEffect(()=>{const timer=setTimeout(()=>setSearch(technology.trim()),300);return()=>clearTimeout(timer)},[technology]);
  useEffect(()=>{
    const request=++latest.current;setLoading(true);setError("");
    apiPage<JobMatch>(listPath(matchesOnly,search,relatedOnly,0)).then(page=>{if(request===latest.current){setItems(page.items);setTotal(page.total)}}).catch(e=>{if(request===latest.current)setError(e.message)}).finally(()=>{if(request===latest.current)setLoading(false)});
  },[matchesOnly,search,relatedOnly]);
  async function loadMore(){
    const request=latest.current;setMore(true);
    try{const page=await apiPage<JobMatch>(listPath(matchesOnly,search,relatedOnly,items.length));if(request===latest.current){setItems(current=>[...current,...page.items]);setTotal(page.total)}}
    catch(e){setError(e instanceof Error?e.message:"Unable to load more jobs")}
    finally{setMore(false)}
  }
  const filtered=search!==""||relatedOnly;
  if(loading&&!items.length&&!filtered)return <div className="card text-ink/50">Loading jobs…</div>;
  if(!loading&&!total&&!filtered)return <><ErrorMessage message={error}/><Empty title={matchesOnly?"No accepted matches":"No jobs discovered yet"} body={matchesOnly?"Jobs that pass employer and filter checks will appear here.":"Run discovery to bring public job-board roles into your shared catalog."} action={<Link href="/discovery" className="btn">Find jobs</Link>}/></>;
  const visible=items;
  return <>
    <ErrorMessage message={error}/>
    <div className="card mb-4 flex flex-wrap items-end gap-3 !p-4">
      <div className="min-w-[240px] flex-1"><label>Technology in job</label><input value={technology} onChange={e=>setTechnology(e.target.value)} placeholder="Go, React, PostgreSQL…"/></div>
      <button type="button" className={relatedOnly?"btn":"btn-secondary"} onClick={()=>setRelatedOnly(value=>!value)}>{relatedOnly?"Showing related jobs":"Related to me"}</button>
      <p className="pb-2 text-xs text-ink/50">{loading?"Searching…":`${items.length} of ${total} jobs`}</p>
    </div>
    {!visible.length&&!loading?<Empty title="No jobs match these controls" body="Try another technology or show all jobs." action={<button className="btn-secondary" onClick={()=>{setTechnology("");setRelatedOnly(false)}}>Clear controls</button>}/>:<div className="space-y-3">{visible.map(item=><Link href={jobHref(item.job)} key={item.job.id} className={`card flex gap-4 hover:border-moss/30 ${item.job.closed_at?"opacity-60":""}`}><Score value={item.match.score} rejected={item.match.rejected}/><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><h2 className="font-semibold">{item.job.title}</h2>{item.job.closed_at&&<span className="rounded-full bg-slate-100 px-2 py-1 text-[10px] font-bold text-slate-600" title={item.job.closed_reason}>CLOSED</span>}{item.match.rejected?<span className="rounded-full bg-red-50 px-2 py-1 text-[10px] font-bold text-red-700">REJECTED</span>:item.match.score>=70?<span className="rounded-full bg-emerald-50 px-2 py-1 text-[10px] font-bold text-emerald-700">GOOD MATCH</span>:null}</div><p className="muted mt-1">{item.job.company} · {item.job.location||"Unspecified"} · {item.job.remote_type||"Arrangement unstated"}</p><p className={`mt-2 truncate text-xs ${item.match.rejected?"text-red-600":"text-moss"}`}>{item.match.rejected?item.match.rejection_reasons.join(" · "):item.match.reasons.join(" · ")}</p>{item.match.matched_skills.length>0&&<p className="mt-1 text-xs text-ink/50">Matched: {item.match.matched_skills.join(", ")}</p>}</div><span className="self-center text-ink/30">→</span></Link>)}</div>}
    {items.length<total&&!loading&&<div className="mt-4 flex justify-center"><button type="button" className="btn-secondary" disabled={more} onClick={loadMore}>{more?"Loading…":`Load ${Math.min(PAGE,total-items.length)} more`}</button></div>}
  </>;
}
