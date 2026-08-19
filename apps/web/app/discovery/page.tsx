"use client";

import Link from "next/link";
import {FormEvent,useEffect,useState} from "react";
import {Shell} from "@/components/Shell";
import {ErrorMessage,Field,Header,Notice,TagInput} from "@/components/UI";
import {api} from "@/lib/api";
import {DiscoveryRun,DiscoverySettings,DiscoverySource} from "@/lib/types";

const defaultSettings:DiscoverySettings={enabled:false,interval_minutes:360,auto_prepare:true,min_score:75,max_jobs_per_run:100};
const newSource=():DiscoverySource=>({provider:"remotive",name:"Remote jobs",identifier:"",search_query:"backend engineer",excluded_search_query:"",active:true});
const directProviders=new Set<DiscoverySource["provider"]>(["greenhouse","lever","ashby","smartrecruiters"]);
const providerHelp:Record<DiscoverySource["provider"],{label:string;help:string;placeholder:string}>={
  remotive:{label:"Focused search query",placeholder:"Go backend engineer",help:"Broad remote feed. Use a focused title or technology query."},
  greenhouse:{label:"Greenhouse board token",placeholder:"company-token",help:"From job-boards.greenhouse.io/company-token or boards.greenhouse.io/company-token, enter only company-token."},
  lever:{label:"Lever site slug",placeholder:"company-slug",help:"From jobs.lever.co/company-slug, enter only company-slug."},
  ashby:{label:"Ashby board name",placeholder:"company-name",help:"From jobs.ashbyhq.com/company-name, enter only company-name."},
  smartrecruiters:{label:"SmartRecruiters company identifier",placeholder:"CompanyIdentifier",help:"From jobs.smartrecruiters.com/CompanyIdentifier, enter the company identifier exactly."},
  arbeitnow:{label:"Match any technology or title",placeholder:"Go, Rust, Backend Engineer",help:"Green choices use OR. Red choices below reject a job when any excluded value appears."},
};
const sourceTerms=(value:string)=>value.split(/,|\||\s+or\s+/i).map(item=>item.trim()).filter(Boolean);

export default function DiscoveryPage(){
  const [settings,setSettings]=useState(defaultSettings);
  const [sources,setSources]=useState<DiscoverySource[]>([]);
  const [runs,setRuns]=useState<DiscoveryRun[]>([]);
  const [source,setSource]=useState(newSource());
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const [running,setRunning]=useState(false);
  const [testing,setTesting]=useState(false);
  const load=()=>Promise.all([api<DiscoverySettings>("/discovery/settings"),api<DiscoverySource[]>("/discovery/sources"),api<DiscoveryRun[]>("/discovery/runs")]).then(([a,b,c])=>{setSettings(a);setSources(b);setRuns(c)}).catch(e=>setError(e.message));
  useEffect(()=>{load()},[]);
  const help=providerHelp[source.provider];

  async function saveSettings(e:FormEvent){e.preventDefault();setError("");try{setSettings(await api<DiscoverySettings>("/discovery/settings",{method:"PUT",body:JSON.stringify(settings)}));setNotice("Discovery automation saved.")}catch(e){setError(e instanceof Error?e.message:"Unable to save settings")}}
  async function addSource(e:FormEvent){e.preventDefault();setError("");try{await api("/discovery/sources",{method:"POST",body:JSON.stringify(source)});setSource(newSource());setNotice("Source added.");load()}catch(e){setError(e instanceof Error?e.message:"Unable to add source")}}
  async function testSource(){setTesting(true);setError("");setNotice("");try{const result=await api<{count:number;sample_title:string}>("/discovery/validate",{method:"POST",body:JSON.stringify(source)});setNotice(`Source works: ${result.count} open job${result.count===1?"":"s"}${result.sample_title?`; first result is “${result.sample_title}”.`:"."}`)}catch(e){setError(e instanceof Error?e.message:"Source could not be read")}finally{setTesting(false)}}
  async function removeSource(id?:string){if(!id)return;setError("");try{await api(`/discovery/sources/${id}`,{method:"DELETE"});load()}catch(e){setError(e instanceof Error?e.message:"Unable to remove source")}}
  async function run(path:"/discovery/run"|"/discovery/recommended"){setRunning(true);setError("");setNotice("");try{const result=await api<DiscoveryRun>(path,{method:"POST"});setNotice(result.status==="completed"?`Discovery finished: ${result.jobs_added} new jobs, ${result.applications_prepared} prepared applications.`:`Discovery failed: ${result.error_message}`);load()}catch(e){setError(e instanceof Error?e.message:"Discovery failed")}finally{setRunning(false)}}

  return <Shell>
    <Header eyebrow="Automated discovery" title="Let the roles come to you" description="Employer-direct boards reduce stale reposts. Every job is deduplicated globally, then filtered and scored separately for your profile." action={<div className="flex flex-wrap gap-2"><button className="btn" disabled={running} onClick={()=>run("/discovery/recommended")}>{running?"Searching…":"Find jobs for my profile"}</button><button className="btn-secondary" disabled={running||!sources.length} onClick={()=>run("/discovery/run")}>Run all sources</button></div>}/>
    <ErrorMessage message={error}/><Notice message={notice}/>
    <div className="grid gap-6 lg:grid-cols-[.85fr_1.15fr]">
      <form className="card h-fit" onSubmit={saveSettings}>
        <h2 className="font-semibold">Automation</h2><p className="muted mb-5 mt-1">Runs at most every six hours to keep public-feed usage polite and lightweight.</p>
        <div className="space-y-4"><label className="!flex items-center gap-2 !normal-case !tracking-normal"><input className="!w-auto" type="checkbox" checked={settings.enabled} onChange={e=>setSettings({...settings,enabled:e.target.checked})}/> Run automatically</label><Field label="Interval (minutes)"><input type="number" min="360" step="60" value={settings.interval_minutes} onChange={e=>setSettings({...settings,interval_minutes:+e.target.value})}/></Field><Field label="Maximum jobs per run"><input type="number" min="1" max="500" value={settings.max_jobs_per_run} onChange={e=>setSettings({...settings,max_jobs_per_run:+e.target.value})}/></Field><label className="!flex items-center gap-2 !normal-case !tracking-normal"><input className="!w-auto" type="checkbox" checked={settings.auto_prepare} onChange={e=>setSettings({...settings,auto_prepare:e.target.checked})}/> Auto-prepare strong matches</label><Field label="Auto-prepare score"><input type="number" min="0" max="100" value={settings.min_score} onChange={e=>setSettings({...settings,min_score:+e.target.value})}/></Field><button className="btn w-full">Save automation</button></div>
        {settings.next_run_at&&<p className="mt-4 text-xs text-ink/50">Next scheduled run: {new Date(settings.next_run_at).toLocaleString()}</p>}
      </form>
      <div>
        <form className="card" onSubmit={addSource}>
          <h2 className="font-semibold">Add a public source</h2><p className="muted mb-5 mt-1">Ashby, Greenhouse, Lever, and SmartRecruiters read current openings directly from employer boards.</p>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Provider"><select value={source.provider} onChange={e=>{const provider=e.target.value as DiscoverySource["provider"];setSource({...source,provider,name:provider==="arbeitnow"?"Europe jobs":provider==="remotive"?"Remote jobs":"",identifier:"",search_query:provider==="remotive"?source.search_query:"",excluded_search_query:""})}}><option value="remotive">Remotive remote feed</option><option value="arbeitnow">Arbeitnow Europe feed</option><option value="ashby">Ashby employer board</option><option value="greenhouse">Greenhouse employer board</option><option value="lever">Lever employer site</option><option value="smartrecruiters">SmartRecruiters employer board</option></select></Field>
            <Field label={directProviders.has(source.provider)?"Company name":"Source label"}><input required value={source.name} onChange={e=>setSource({...source,name:e.target.value})}/></Field>
            {source.provider==="arbeitnow"?<>
              <Field className="sm:col-span-2" label={help.label}><TagInput value={sourceTerms(source.search_query)} onChange={terms=>setSource({...source,search_query:terms.join(", ")})} placeholder={help.placeholder}/><p className="mt-2 text-xs leading-relaxed text-ink/50">{help.help}</p></Field>
              <Field className="sm:col-span-2" label="Do not include jobs mentioning"><TagInput tone="exclude" value={sourceTerms(source.excluded_search_query)} onChange={terms=>setSource({...source,excluded_search_query:terms.join(", ")})} placeholder="WordPress, PHP, Sales"/><p className="mt-2 text-xs leading-relaxed text-red-700/70">A match on any red value removes the posting from this source.</p></Field>
            </>:<Field className="sm:col-span-2" label={help.label}><input required value={directProviders.has(source.provider)?source.identifier:source.search_query} placeholder={help.placeholder} onChange={e=>directProviders.has(source.provider)?setSource({...source,identifier:e.target.value}):setSource({...source,search_query:e.target.value})}/><p className="mt-2 text-xs leading-relaxed text-ink/50">{help.help}</p></Field>}
          </div>
          <div className="mt-4 flex gap-2"><button className="btn">Add source</button><button type="button" className="btn-secondary" disabled={testing} onClick={testSource}>{testing?"Testing…":"Test source"}</button></div>
        </form>
        <div className="mt-4 space-y-2">{sources.map(item=><div className="card flex items-center justify-between gap-4 !p-4" key={item.id}><div><div className="flex items-center gap-2"><p className="font-semibold">{item.name}</p><span className="rounded-full bg-sage px-2 py-1 text-[10px] font-bold uppercase text-moss">{item.provider}</span></div><p className="muted mt-1">{item.identifier||item.search_query||"All current jobs"}{item.last_crawled_at?` · checked ${new Date(item.last_crawled_at).toLocaleString()}`:""}</p>{item.excluded_search_query&&<p className="mt-1 text-xs font-semibold text-red-600">NOT: {item.excluded_search_query}</p>}{item.last_error&&<p className="mt-1 text-xs text-red-600">{item.last_error}</p>}</div><button className="text-xs text-red-600" onClick={()=>removeSource(item.id)}>Remove</button></div>)}</div>
      </div>
    </div>
    <section className="mt-7"><div className="mb-3 flex items-end justify-between"><div><h2 className="text-lg font-semibold">Recent runs</h2><p className="muted">Catalog jobs are shared; rejection and scoring are private to each user.</p></div><Link className="text-sm font-semibold text-moss" href="/matches">Review matches →</Link></div>{runs.length?<div className="overflow-hidden rounded-2xl border border-black/5 bg-white"><div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="bg-paper text-xs uppercase tracking-wider text-ink/50"><tr><th className="p-3">Started</th><th className="p-3">Status</th><th className="p-3">Seen</th><th className="p-3">New</th><th className="p-3">Rejected</th><th className="p-3">Prepared</th><th className="p-3">Duplicates</th></tr></thead><tbody>{runs.map(item=><tr className="border-t border-black/5" key={item.id}><td className="p-3">{new Date(item.started_at).toLocaleString()}</td><td className="p-3 font-semibold">{item.status}</td><td className="p-3">{item.jobs_seen}</td><td className="p-3">{item.jobs_added}</td><td className="p-3">{item.jobs_rejected}</td><td className="p-3">{item.applications_prepared}</td><td className="p-3">{item.duplicates_skipped}</td></tr>)}</tbody></table></div></div>:<div className="card muted">No discovery runs yet.</div>}</section>
    <section className="mt-6 rounded-2xl border border-blue-100 bg-blue-50 p-5 text-sm text-blue-900"><strong>Referral model:</strong> outbound clicks are attributable, but compensation only applies to jobs backed by an explicit employer or affiliate agreement. Partner jobs disclose that relationship before opening the application link.</section>
  </Shell>
}
