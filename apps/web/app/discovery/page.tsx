"use client";

import Link from "next/link";
import {FormEvent,useEffect,useState} from "react";
import {Shell} from "@/components/Shell";
import {useMe} from "@/components/useMe";
import {ErrorMessage,Field,Header,Notice,TagInput} from "@/components/UI";
import {api} from "@/lib/api";
import {BoardSuggestion,DetectedBoard,DiscoveryRun,DiscoverySettings,DiscoverySource} from "@/lib/types";

type Provider=DiscoverySource["provider"];

const defaultSettings:DiscoverySettings={enabled:false,interval_minutes:360,auto_prepare:true,min_score:75,max_jobs_per_run:100};
const directProviders=new Set<Provider>(["greenhouse","lever","ashby","smartrecruiters"]);
const providers:{value:Provider;label:string}[]=[
  {value:"remotive",label:"Remotive remote feed"},
  {value:"arbeitnow",label:"Arbeitnow Europe feed"},
  {value:"ashby",label:"Ashby employer board"},
  {value:"greenhouse",label:"Greenhouse employer board"},
  {value:"lever",label:"Lever employer site"},
  {value:"smartrecruiters",label:"SmartRecruiters employer board"},
];
const providerHelp:Record<Provider,{label:string;help:string;placeholder:string}>={
  remotive:{label:"Focused search query",placeholder:"Go backend engineer",help:"Broad remote feed. Use a focused title or technology query."},
  greenhouse:{label:"Greenhouse board token",placeholder:"company-token",help:"Paste a careers URL below or enter only the token after job-boards.greenhouse.io/."},
  lever:{label:"Lever site slug",placeholder:"company-slug",help:"Paste a careers URL below or enter only the slug after jobs.lever.co/."},
  ashby:{label:"Ashby board name",placeholder:"company-name",help:"Paste a careers URL below or enter only the name after jobs.ashbyhq.com/."},
  smartrecruiters:{label:"SmartRecruiters company identifier",placeholder:"CompanyIdentifier",help:"Paste a careers URL below or enter the identifier after jobs.smartrecruiters.com/."},
  arbeitnow:{label:"Match any technology or title",placeholder:"Go, Rust, Backend Engineer",help:"Green choices use OR. Red choices below reject a job when any excluded value appears."},
};

const newSource=(provider:Provider="remotive", current?:DiscoverySource):DiscoverySource=>{
  const base={id:current?.id,provider,name:"",identifier:"",search_query:"",excluded_search_query:"",active:current?.active??true};
  if(provider==="arbeitnow")return {...base,name:"Europe jobs",search_query:"Go, Rust, Backend Engineer"};
  if(provider==="remotive")return {...base,name:"Remote jobs",search_query:current?.search_query||"backend engineer"};
  return {...base,name:"",identifier:""};
};
const sourceTerms=(value:string)=>value.split(/,|\||\s+or\s+/i).map(item=>item.trim()).filter(Boolean);
const sourceLabel=(item:DiscoverySource)=>item.identifier||item.search_query||"All current jobs";
const titleFromIdentifier=(value:string)=>value.split(/[-_]/).filter(Boolean).map(part=>part.charAt(0).toUpperCase()+part.slice(1)).join(" ")||value;

// Discovery belongs to the server's admins (#128); a member who opens this
// page is pointed at the jobs page, where the catalog is scored for them.
// Nothing mounts until the role is known, so a member never loads the admin
// controls. If the role cannot be read the admin view shows, and the API
// refuses whatever a member may not use.
export default function DiscoveryPage(){const me=useMe();if(me===undefined)return <Shell><p className="muted">Loading…</p></Shell>;if(me&&!me.admin)return <Shell><Header eyebrow="Discovery" title="Jobs come from this server's discovery" description="The admin runs the job searches. Every new job is checked against your profile and filters."/><section className="card"><p className="muted">Your matches are on the jobs page, where Find jobs for my profile checks anything new.</p><Link className="btn mt-4 inline-flex" href="/jobs">Go to jobs</Link></section></Shell>;return <DiscoveryAdmin/>}
function DiscoveryAdmin(){
  const [settings,setSettings]=useState(defaultSettings);
  const [sources,setSources]=useState<DiscoverySource[]>([]);
  const [runs,setRuns]=useState<DiscoveryRun[]>([]);
  const [suggestions,setSuggestions]=useState<BoardSuggestion[]>([]);
  const [source,setSource]=useState(newSource());
  const [detectUrl,setDetectUrl]=useState("");
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const [running,setRunning]=useState(false);
  const [testing,setTesting]=useState("");
  const [detecting,setDetecting]=useState(false);
  const help=providerHelp[source.provider];

  const load=()=>Promise.all([
    api<DiscoverySettings>("/discovery/settings"),
    api<DiscoverySource[]>("/discovery/sources"),
    api<DiscoveryRun[]>("/discovery/runs"),
    api<BoardSuggestion[]>("/discovery/suggestions"),
  ]).then(([a,b,c,d])=>{setSettings(a);setSources(b);setRuns(c);setSuggestions(d)}).catch(e=>setError(e.message));
  useEffect(()=>{load()},[]);

  async function saveSettings(e:FormEvent){
    e.preventDefault();setError("");
    try{setSettings(await api<DiscoverySettings>("/discovery/settings",{method:"PUT",body:JSON.stringify(settings)}));setNotice("Discovery automation saved.")}
    catch(e){setError(e instanceof Error?e.message:"Unable to save settings")}
  }
  async function saveSource(e:FormEvent){
    e.preventDefault();setError("");setNotice("");
    try{
      const path=source.id?`/discovery/sources/${source.id}`:"/discovery/sources";
      const method=source.id?"PUT":"POST";
      const saved=await api<DiscoverySource>(path,{method,body:JSON.stringify(source)});
      setNotice(source.id?`Updated ${saved.name}.`:`Added ${saved.name}.`);
      setSource(newSource());load();
    }catch(e){setError(e instanceof Error?e.message:"Unable to save source")}
  }
  async function testSource(candidate:DiscoverySource=source){
    const key=candidate.id||"draft";setTesting(key);setError("");setNotice("");
    try{
      const result=await api<{count:number;sample_title:string}>("/discovery/validate",{method:"POST",body:JSON.stringify(candidate)});
      setNotice(`${candidate.name} works: ${result.count} open job${result.count===1?"":"s"}${result.sample_title?`; first result is “${result.sample_title}”.`:"."}`);
    }catch(e){setError(e instanceof Error?e.message:"Source could not be read")}
    finally{setTesting("")}
  }
  async function toggleSource(item:DiscoverySource){
    if(!item.id)return;setError("");setNotice("");
    try{await api<DiscoverySource>(`/discovery/sources/${item.id}`,{method:"PUT",body:JSON.stringify({...item,active:!item.active})});load()}
    catch(e){setError(e instanceof Error?e.message:"Unable to update source")}
  }
  async function removeSource(id?:string){
    if(!id)return;setError("");setNotice("");
    try{await api(`/discovery/sources/${id}`,{method:"DELETE"});if(source.id===id)setSource(newSource());load()}
    catch(e){setError(e instanceof Error?e.message:"Unable to remove source")}
  }
  async function detectSource(e:FormEvent){
    e.preventDefault();setDetecting(true);setError("");setNotice("");
    try{
      const result=await api<DetectedBoard>("/discovery/detect",{method:"POST",body:JSON.stringify({url:detectUrl})});
      const provider=result.provider as Provider;
      if(!result.supported||!providerHelp[provider]){setNotice(`Detected ${result.provider}, but JobPilot cannot follow it as a discovery source yet.`);return}
      setSource({provider,name:titleFromIdentifier(result.identifier),identifier:result.identifier,search_query:"",excluded_search_query:"",active:true});
      setNotice(`Detected a ${provider} board: ${result.identifier}${result.job_id?". The posting id was ignored because sources follow whole boards.":""}`);
      setDetectUrl("");
    }catch(e){setError(e instanceof Error?e.message:"Unable to detect source")}
    finally{setDetecting(false)}
  }
  async function run(path:"/discovery/run"|"/discovery/recommended"){
    setRunning(true);setError("");setNotice("");
    try{
      const result=await api<DiscoveryRun>(path,{method:"POST"});
      setNotice(result.status==="completed"?`Discovery finished: ${result.jobs_added} new jobs, ${result.applications_prepared} prepared applications.`:`Discovery failed: ${result.error_message}`);
      load();
    }catch(e){setError(e instanceof Error?e.message:"Discovery failed")}
    finally{setRunning(false)}
  }

  return <Shell>
    <Header eyebrow="Automated discovery" title="Let the roles come to you" description="Employer-direct boards reduce stale reposts. Every job is deduplicated globally, then filtered and scored separately for your profile." action={<div className="flex flex-wrap gap-2"><button className="btn" disabled={running} onClick={()=>run("/discovery/recommended")}>{running?"Searching…":"Find jobs for my profile"}</button><button className="btn-secondary" disabled={running||!sources.some(x=>x.active)} onClick={()=>run("/discovery/run")}>Run active sources</button></div>}/>
    <ErrorMessage message={error}/><Notice message={notice}/>
    <div className="grid gap-6 lg:grid-cols-[.85fr_1.15fr]">
      <div className="space-y-6">
        <form className="card" onSubmit={saveSettings}>
          <h2 className="font-semibold">Automation</h2><p className="muted mb-5 mt-1">Runs at most every six hours to keep public-feed usage polite and lightweight.</p>
          <div className="space-y-4"><label className="!flex items-center gap-2 !normal-case !tracking-normal"><input className="!w-auto" type="checkbox" checked={settings.enabled} onChange={e=>setSettings({...settings,enabled:e.target.checked})}/> Run automatically</label><Field label="Interval (minutes)"><input type="number" min="360" step="60" value={settings.interval_minutes} onChange={e=>setSettings({...settings,interval_minutes:+e.target.value})}/></Field><Field label="Maximum jobs per run"><input type="number" min="1" max="500" value={settings.max_jobs_per_run} onChange={e=>setSettings({...settings,max_jobs_per_run:+e.target.value})}/></Field><label className="!flex items-center gap-2 !normal-case !tracking-normal"><input className="!w-auto" type="checkbox" checked={settings.auto_prepare} onChange={e=>setSettings({...settings,auto_prepare:e.target.checked})}/> Auto-prepare strong matches</label><Field label="Auto-prepare score"><input type="number" min="0" max="100" value={settings.min_score} onChange={e=>setSettings({...settings,min_score:+e.target.value})}/></Field><button className="btn w-full">Save automation</button></div>
          {settings.next_run_at&&<p className="mt-4 text-xs text-ink/50">Next scheduled run: {new Date(settings.next_run_at).toLocaleString()}</p>}
        </form>
        <section className="card">
          <h2 className="font-semibold">Starter employer boards</h2><p className="muted mb-4 mt-1">Curated boards are employer-direct when possible. Validate a board before relying on it.</p>
          <div className="space-y-2">{suggestions.map(item=><button type="button" className="w-full rounded-xl border border-black/5 bg-paper p-3 text-left transition hover:border-moss/30 hover:bg-sage/60" key={`${item.provider}-${item.identifier||item.name}`} onClick={()=>setSource(item.provider==="remotive"?{...newSource("remotive"),name:item.name}:{provider:item.provider as Provider,name:item.name,identifier:item.identifier,search_query:"",excluded_search_query:"",active:true})}><div className="flex items-center justify-between gap-3"><span className="font-semibold">{item.name}</span><span className={`rounded-full px-2 py-1 text-[10px] font-bold uppercase ${item.remote_friendly?"bg-emerald-50 text-emerald-700":"bg-amber-50 text-amber-700"}`}>{item.remote_friendly?"remote-friendly":"mixed"}</span></div><p className="mt-1 text-xs text-ink/55">{item.provider}{item.identifier?` · ${item.identifier}`:""} — {item.note}</p></button>)}</div>
        </section>
      </div>
      <div>
        <form className="card mb-4" onSubmit={detectSource}>
          <h2 className="font-semibold">Paste a careers URL</h2><p className="muted mb-4 mt-1">JobPilot can extract board identifiers from supported employer ATS URLs, including specific posting links.</p>
          <div className="flex gap-2"><input value={detectUrl} placeholder="https://job-boards.greenhouse.io/company" onChange={e=>setDetectUrl(e.target.value)}/><button className="btn-secondary shrink-0" disabled={detecting||!detectUrl.trim()}>{detecting?"Detecting…":"Detect"}</button></div>
        </form>
        <form className="card" onSubmit={saveSource}>
          <div className="mb-5 flex items-start justify-between gap-3"><div><h2 className="font-semibold">{source.id?"Edit source":"Add a public source"}</h2><p className="muted mt-1">Pause noisy boards, tune OR/NOT terms, and validate sources before running discovery.</p></div>{source.id&&<button type="button" className="text-xs font-semibold text-ink/50 hover:text-moss" onClick={()=>setSource(newSource())}>Cancel edit</button>}</div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Provider"><select value={source.provider} onChange={e=>setSource(newSource(e.target.value as Provider,source))}>{providers.map(item=><option value={item.value} key={item.value}>{item.label}</option>)}</select></Field>
            <Field label={directProviders.has(source.provider)?"Company name":"Source label"}><input required value={source.name} onChange={e=>setSource({...source,name:e.target.value})}/></Field>
            {source.provider==="arbeitnow"?<>
              <Field className="sm:col-span-2" label={help.label}><TagInput value={sourceTerms(source.search_query)} onChange={terms=>setSource({...source,search_query:terms.join(", ")})} placeholder={help.placeholder}/><p className="mt-2 text-xs leading-relaxed text-ink/50">{help.help}</p></Field>
              <Field className="sm:col-span-2" label="Do not include jobs mentioning"><TagInput tone="exclude" value={sourceTerms(source.excluded_search_query)} onChange={terms=>setSource({...source,excluded_search_query:terms.join(", ")})} placeholder="WordPress, PHP, Sales"/><p className="mt-2 text-xs leading-relaxed text-red-700/70">A match on any red value removes the posting from this source.</p></Field>
            </>:<Field className="sm:col-span-2" label={help.label}><input required value={directProviders.has(source.provider)?source.identifier:source.search_query} placeholder={help.placeholder} onChange={e=>directProviders.has(source.provider)?setSource({...source,identifier:e.target.value}):setSource({...source,search_query:e.target.value})}/><p className="mt-2 text-xs leading-relaxed text-ink/50">{help.help}</p></Field>}
            {directProviders.has(source.provider)&&<>
              <Field className="sm:col-span-2" label="Only titles containing"><TagInput value={sourceTerms(source.search_query)} onChange={terms=>setSource({...source,search_query:terms.join(", ")})} placeholder="Engineer, Developer"/><p className="mt-2 text-xs leading-relaxed text-ink/50">A board lists every open role. Leave empty to take them all, or keep only titles containing any of these.</p></Field>
              <Field className="sm:col-span-2" label="Not titles containing"><TagInput tone="exclude" value={sourceTerms(source.excluded_search_query)} onChange={terms=>setSource({...source,excluded_search_query:terms.join(", ")})} placeholder="Sales, Manager"/><p className="mt-2 text-xs leading-relaxed text-red-700/70">A title with any red value is left out.</p></Field>
            </>}
            <label className="!flex items-center gap-2 !normal-case !tracking-normal"><input className="!w-auto" type="checkbox" checked={source.active} onChange={e=>setSource({...source,active:e.target.checked})}/> Active source</label>
          </div>
          <div className="mt-4 flex flex-wrap gap-2"><button className="btn">{source.id?"Save source":"Add source"}</button><button type="button" className="btn-secondary" disabled={testing==="draft"} onClick={()=>testSource(source)}>{testing==="draft"?"Testing…":"Test draft"}</button></div>
        </form>
        <div className="mt-4 space-y-2">{sources.map(item=><article className={`card !p-4 ${item.active?"":"opacity-70"}`} key={item.id}><div className="flex items-start justify-between gap-4"><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><p className="font-semibold">{item.name}</p><span className="rounded-full bg-sage px-2 py-1 text-[10px] font-bold uppercase text-moss">{item.provider}</span><span className={`rounded-full px-2 py-1 text-[10px] font-bold uppercase ${item.active?"bg-emerald-50 text-emerald-700":"bg-slate-100 text-slate-500"}`}>{item.active?"active":"paused"}</span></div><p className="muted mt-1 truncate">{sourceLabel(item)}{item.last_crawled_at?` · checked ${new Date(item.last_crawled_at).toLocaleString()}`:""}</p>{item.excluded_search_query&&<p className="mt-1 text-xs font-semibold text-red-600">NOT: {item.excluded_search_query}</p>}{item.last_error&&<p className="mt-1 text-xs text-red-600">{item.last_error}</p>}</div><div className="flex shrink-0 flex-wrap justify-end gap-2"><button className="btn-secondary !px-3 !py-1.5 text-xs" onClick={()=>toggleSource(item)}>{item.active?"Pause":"Resume"}</button><button className="btn-secondary !px-3 !py-1.5 text-xs" disabled={testing===item.id} onClick={()=>testSource(item)}>{testing===item.id?"Testing…":"Test"}</button><button className="btn-secondary !px-3 !py-1.5 text-xs" onClick={()=>setSource(item)}>Edit</button><button className="text-xs font-semibold text-red-600" onClick={()=>removeSource(item.id)}>Remove</button></div></div></article>)}</div>
      </div>
    </div>
    <section className="mt-7"><div className="mb-3 flex items-end justify-between"><div><h2 className="text-lg font-semibold">Recent runs</h2><p className="muted">Catalog jobs are shared; rejection and scoring are private to each user.</p></div><Link className="text-sm font-semibold text-moss" href="/matches">Review matches →</Link></div>{runs.length?<div className="overflow-hidden rounded-2xl border border-black/5 bg-white"><div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="bg-paper text-xs uppercase tracking-wider text-ink/50"><tr><th className="p-3">Started</th><th className="p-3">Status</th><th className="p-3">Seen</th><th className="p-3">New</th><th className="p-3">New rejected</th><th className="p-3">Prepared</th><th className="p-3">Known</th></tr></thead><tbody>{runs.map(item=><tr className="border-t border-black/5" key={item.id}><td className="p-3">{new Date(item.started_at).toLocaleString()}</td><td className="p-3 font-semibold">{item.status}</td><td className="p-3">{item.jobs_seen}</td><td className="p-3">{item.jobs_added}</td><td className="p-3">{item.jobs_rejected}</td><td className="p-3">{item.applications_prepared}</td><td className="p-3">{item.duplicates_skipped}</td></tr>)}</tbody></table></div></div>:<div className="card muted">No discovery runs yet.</div>}</section>
    <section className="mt-6 rounded-2xl border border-blue-100 bg-blue-50 p-5 text-sm text-blue-900"><strong>Referral model:</strong> outbound clicks are attributable, but compensation only applies to jobs backed by an explicit employer or affiliate agreement. Partner jobs disclose that relationship before opening the application link.</section>
  </Shell>
}
