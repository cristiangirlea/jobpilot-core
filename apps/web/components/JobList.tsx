"use client";

import Link from "next/link";
import {useEffect,useMemo,useState} from "react";
import {api,jobHref} from "@/lib/api";
import {JobMatch} from "@/lib/types";
import {Empty,ErrorMessage,Score} from "./UI";

function hasTechnology(item:JobMatch,technology:string){
  const text=[item.job.title,item.job.description,...item.match.matched_skills,...item.match.missing_skills].join(" ");
  const escaped=technology.replace(/[.*+?^${}()|[\]\\]/g,"\\$&");
  return new RegExp(`(^|[^a-z0-9+#.])${escaped}([^a-z0-9+#.]|$)`,"i").test(text);
}

export function JobList({matchesOnly=false}:{matchesOnly?:boolean}){
  const [items,setItems]=useState<JobMatch[]>([]);
  const [error,setError]=useState("");
  const [loading,setLoading]=useState(true);
  const [technology,setTechnology]=useState("");
  const [relatedOnly,setRelatedOnly]=useState(false);
  useEffect(()=>{api<JobMatch[]>(matchesOnly?"/matches":"/jobs").then(setItems).catch(e=>setError(e.message)).finally(()=>setLoading(false))},[matchesOnly]);
  const visible=useMemo(()=>{
    const needle=technology.trim().toLowerCase();
    return items.filter(item=>{
      if(relatedOnly&&(item.match.rejected||item.match.score<70))return false;
      if(!needle)return true;
      return hasTechnology(item,needle);
    });
  },[items,technology,relatedOnly]);
  if(loading)return <div className="card text-ink/50">Loading jobs…</div>;
  if(!items.length)return <><ErrorMessage message={error}/><Empty title={matchesOnly?"No accepted matches":"No jobs discovered yet"} body={matchesOnly?"Jobs that pass employer and filter checks will appear here.":"Run discovery to bring public job-board roles into your shared catalog."} action={<Link href="/discovery" className="btn">Find jobs</Link>}/></>;
  return <>
    <ErrorMessage message={error}/>
    <div className="card mb-4 flex flex-wrap items-end gap-3 !p-4">
      <div className="min-w-[240px] flex-1"><label>Technology in job</label><input value={technology} onChange={e=>setTechnology(e.target.value)} placeholder="Go, React, PostgreSQL…"/></div>
      <button type="button" className={relatedOnly?"btn":"btn-secondary"} onClick={()=>setRelatedOnly(value=>!value)}>{relatedOnly?"Showing related jobs":"Related to me"}</button>
      <p className="pb-2 text-xs text-ink/50">{visible.length} of {items.length} jobs</p>
    </div>
    {!visible.length?<Empty title="No jobs match these controls" body="Try another technology or show all jobs." action={<button className="btn-secondary" onClick={()=>{setTechnology("");setRelatedOnly(false)}}>Clear controls</button>}/>:<div className="space-y-3">{visible.map(item=><Link href={jobHref(item.job)} key={item.job.id} className="card flex gap-4 hover:border-moss/30"><Score value={item.match.score} rejected={item.match.rejected}/><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><h2 className="font-semibold">{item.job.title}</h2>{item.match.rejected?<span className="rounded-full bg-red-50 px-2 py-1 text-[10px] font-bold text-red-700">REJECTED</span>:item.match.score>=70?<span className="rounded-full bg-emerald-50 px-2 py-1 text-[10px] font-bold text-emerald-700">GOOD MATCH</span>:null}</div><p className="muted mt-1">{item.job.company} · {item.job.location||"Unspecified"} · {item.job.remote_type||"Arrangement unstated"}</p><p className={`mt-2 truncate text-xs ${item.match.rejected?"text-red-600":"text-moss"}`}>{item.match.rejected?item.match.rejection_reasons.join(" · "):item.match.reasons.join(" · ")}</p>{item.match.matched_skills.length>0&&<p className="mt-1 text-xs text-ink/50">Matched: {item.match.matched_skills.join(", ")}</p>}</div><span className="self-center text-ink/30">→</span></Link>)}</div>}
  </>;
}
