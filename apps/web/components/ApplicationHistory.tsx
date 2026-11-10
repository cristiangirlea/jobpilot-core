import Link from "next/link";
import type {ApplicationHistory as History} from "@/lib/types";

const relations={same_role:"Same role",different_role:"Different role",uncertain_role:"Role not certain"};

// The one-line state a job card shows, or "" when history has nothing to say now.
export function historyChip(history?:History){
 if(!history?.entries.length)return "";
 if(history.blocked)return "Excluded by history";
 const current=history.entries.filter(entry=>entry.in_window);
 if(!current.length)return "";
 return current.some(entry=>entry.relation==="same_role")?"Applied to this role before":"Applied to this company before";
}

export function ApplicationHistory({history}:{history?:History}){
 if(!history?.entries.length)return null;
 return <section className="mb-5 space-y-3 rounded-xl border border-black/10 p-4" aria-label="Earlier applications to this company">
  <h2 className="font-semibold">Earlier applications to this company</h2>
  <p className="text-sm">{history.blocked?"Excluded by your application-history rule.":history.entries.some(entry=>entry.in_window)?"You applied to this company before.":"Earlier applications are outside your lookback."}</p>
  {history.entries.map(entry=><article className="space-y-1 break-words border-t border-black/5 pt-3 text-sm" key={entry.id}>
   <h3 className="font-semibold">{entry.employer} - {entry.title||"Role unknown"}</h3>
   <p>{relations[entry.relation]}{entry.title_changed&&" - title changed since"}</p>
   <p>Applied: {entry.date_unknown||!entry.applied_at?"Unknown date":entry.applied_at} - {entry.status}</p>
   {entry.outcome_at&&<p>Outcome date: {entry.outcome_at}</p>}
   {entry.requisition&&<p>Requisition: {entry.requisition}</p>}
   <p>Source: {entry.source}</p><p>Evidence: {entry.evidence_kind} - {entry.evidence_ref}</p>
   {!entry.in_window&&<p className="muted">Outside the lookback</p>}
   <p>{entry.cv?.document_id?<>CV used: {entry.cv.filename||`a file no longer stored (document ${entry.cv.document_id})`}{entry.cv.version_at&&` (${entry.cv.version_at.slice(0,10)})`}</>:entry.cv_label?<>CV used: {entry.cv_label}</>:"CV used: Unknown. The previous application still counts."}</p>
   {entry.job_id&&<p><Link className="text-moss underline" href={`/jobs/${encodeURIComponent(entry.job_id)}`}>View recorded job</Link></p>}
   {entry.canonical_url&&<p>Canonical URL (reference only): {entry.canonical_url}</p>}
  </article>)}
  <p className="muted text-xs">JobPilot has not checked any mailbox. This list holds only what was imported or recorded here.</p>
 </section>;
}
