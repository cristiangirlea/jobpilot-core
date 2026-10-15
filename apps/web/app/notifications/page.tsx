"use client";

import Link from "next/link";
import {useEffect,useState} from "react";
import {api} from "@/lib/api";
import {Notification,NotificationFeed} from "@/lib/types";
import {Shell} from "@/components/Shell";
import {Empty,ErrorMessage,Header} from "@/components/UI";

const kindLabel:Record<string,string>={answer:"Answer",cv:"CV",outbox:"Application"};

// href comes from the API; only a path inside this app becomes a link.
const internalHref=(n:Notification)=>{const href=n.detail?.href;return typeof href==="string"&&href.startsWith("/")&&!href.startsWith("//")?href:""};

export default function NotificationsPage(){
  const [feed,setFeed]=useState<NotificationFeed|null>(null);
  const [state,setState]=useState<"open"|"all">("open");
  const [error,setError]=useState("");

  useEffect(()=>{setError("");api<NotificationFeed>(`/notifications?state=${state}&limit=200`).then(setFeed).catch(e=>setError((e as Error).message))},[state]);

  return <Shell>
    <Header eyebrow="Inbox" title="Waiting for you"
      description="Every draft and application that needs your decision, in one list. An item clears itself once you approve, cancel or delete the thing it points to."/>
    <ErrorMessage message={error}/>
    <div className="mb-4 flex gap-2">
      {(["open","all"] as const).map(name=>
        <button key={name} className={`rounded-lg px-3 py-1.5 text-xs font-semibold ${state===name?"bg-ink text-white":"bg-black/5"}`} onClick={()=>setState(name)}>
          {name==="open"?`Open${feed?` (${feed.open})`:""}`:"History"}
        </button>)}
    </div>
    {!feed?<div className="card animate-pulse text-ink/40">Loading…</div>
      :feed.items.length===0?<Empty title={state==="open"?"Nothing waits for you":"No history yet"} body="Drafted answers, CVs and composed applications that need your decision appear here."/>
      :<div className="space-y-3">{feed.items.map(n=>{const href=internalHref(n);const blockers=Array.isArray(n.detail?.blockers)?n.detail.blockers as string[]:[];return <div key={n.id} className={`card flex flex-wrap items-start justify-between gap-3 !p-4 ${n.resolved_at?"opacity-60":""}`}>
        <div className="min-w-0">
          <p className="text-[10px] font-bold uppercase tracking-widest text-ink/40">{kindLabel[n.kind]||n.kind}</p>
          <h2 className="font-semibold">{n.title}</h2>
          {blockers.length>0&&<ul className="mt-1 list-disc pl-5 text-sm text-amber-700">{blockers.map(b=><li key={b}>{b}</li>)}</ul>}
          <p className="mt-1 text-xs text-ink/50">Waiting since {new Date(n.created_at).toLocaleString()}{n.resolved_at?` · resolved ${new Date(n.resolved_at).toLocaleString()}`:""}</p>
        </div>
        {href&&!n.resolved_at&&<Link className="btn-secondary !px-3 !py-1.5 text-xs" href={href}>Open</Link>}
      </div>})}</div>}
  </Shell>
}
