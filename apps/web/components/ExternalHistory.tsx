"use client";
import {useEffect,useState} from "react";
import {api} from "@/lib/api";
import {ExternalApplication,HistoryPage} from "@/lib/types";
import {ErrorMessage} from "@/components/UI";

export function ExternalHistory(){
 const [items,setItems]=useState<ExternalApplication[]>([]),[next,setNext]=useState(""),[error,setError]=useState(""),[busy,setBusy]=useState(false);
 async function load(after=""){setBusy(true);try{const page=await api<HistoryPage>(`/application-history?limit=50&after=${encodeURIComponent(after)}`);setItems(old=>after?[...old,...page.items]:page.items);setNext(page.next);setError("")}catch(e){setError(e instanceof Error?e.message:"Could not load history")}finally{setBusy(false)}}
 useEffect(()=>{void load()},[]);
 return <section className="mt-8 space-y-3" aria-label="External application history"><h2 className="text-xl font-semibold">External application history</h2><p className="muted">Private records imported from earlier applications. Evidence references are supplied facts; JobPilot has not independently verified them. These records have no application actions.</p><ErrorMessage message={error}/>{!busy&&!error&&!items.length&&<p className="muted">No external history imported.</p>}{items.map(x=><details className="card !p-4" key={x.id}><summary className="cursor-pointer font-semibold">{x.employer} · {x.title||"Role unknown"} · {x.status}</summary><dl className="mt-3 grid gap-2 text-sm sm:grid-cols-2">{Object.entries({"External key":x.external_key,"Requisition":x.requisition,"Canonical URL (reference only)":x.canonical_url,"Original applied date":x.applied_at,"Original outcome date":x.outcome_at,"Source":x.source,"Evidence kind":x.evidence_kind,"Evidence reference":x.evidence_ref,"CV version (historical reference)":x.cv_version,"Imported at":x.imported_at}).map(([k,v])=><div className="break-words" key={k}><dt className="font-semibold">{k}</dt><dd>{v||"Unknown"}</dd></div>)}</dl></details>)}{next&&<button className="btn" disabled={busy} onClick={()=>void load(next)}>Load more history</button>}</section>
}
