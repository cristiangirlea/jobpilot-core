"use client";
import {useEffect,useState} from "react";import {api} from "@/lib/api";import {ErrorMessage} from "./UI";import {JobList} from "./JobList";import type {CatalogSummary} from "@/lib/types";
// CatalogCard is what a user sees of discovery: how fresh the shared catalog
// is, how many open jobs fit them, and a button that scores the catalog's new
// jobs against their profile. It fetches nothing from job boards.
function CatalogCard({onFound}:{onFound:()=>void}){
  const [summary,setSummary]=useState<CatalogSummary|null>(null),[busy,setBusy]=useState(false),[note,setNote]=useState(""),[error,setError]=useState("");
  useEffect(()=>{api<CatalogSummary>("/catalog/summary").then(setSummary).catch(e=>setError(e.message))},[]);
  const find=async()=>{setBusy(true);setError("");setNote("");try{const r=await api<{scored:number;summary:CatalogSummary}>("/jobs/find",{method:"POST"});setSummary(r.summary);setNote(r.scored?`Checked ${r.scored} new ${r.scored===1?"job":"jobs"} against your profile.`:"No new jobs since you last looked.");onFound()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
  const last=summary?.last_job_at?new Date(summary.last_job_at).toLocaleString():"never";
  return <section className="card mb-6">
    <ErrorMessage message={error}/>
    <div className="flex flex-wrap items-center justify-between gap-4">
      <div>
        <h2 className="font-semibold">Jobs for your profile</h2>
        {summary&&<p className="muted mt-1">{summary.open_matches} open {summary.open_matches===1?"job fits":"jobs fit"} you · {summary.jobs_last_24h} new today, {summary.jobs_last_7d} this week · last added {last}</p>}
        {note&&<p className="mt-1 text-sm font-semibold text-moss">{note}</p>}
      </div>
      <button className="btn" onClick={find} disabled={busy}>{busy?"Looking…":"Find jobs for my profile"}</button>
    </div>
  </section>;
}
export function JobInbox(){const [round,setRound]=useState(0);return <><CatalogCard onFound={()=>setRound(n=>n+1)}/><JobList key={round}/></>}
