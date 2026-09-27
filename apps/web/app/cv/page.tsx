"use client";

import {useEffect,useRef,useState} from "react";
import {api,token} from "@/lib/api";
import {CVDocument,CVImportResult} from "@/lib/types";
import {Shell} from "@/components/Shell";
import {TailoredPreview} from "@/components/TailoredPreview";
import {Empty,ErrorMessage,Header,Notice} from "@/components/UI";

const API=process.env.NEXT_PUBLIC_API_URL||"http://localhost:8080/api";

export default function CVPage(){
  const [items,setItems]=useState<CVDocument[]>([]);
  const [open,setOpen]=useState<CVDocument|null>(null);
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const [busy,setBusy]=useState(false);
  const file=useRef<HTMLInputElement>(null);

  const load=async()=>{try{const list=await api<CVDocument[]>("/cv");setItems(list);return list}catch(e){setError((e as Error).message);return []}};
  // /cv?open=<id> opens that document, so the outbox can link to the CV an
  // application carries.
  useEffect(()=>{load().then(list=>{
    const wanted=new URLSearchParams(window.location.search).get("open");
    const match=list.find(item=>item.id===wanted);
    if(match)setOpen(match);
  })},[]);

  // Upload goes through fetch directly: the shared client sends JSON, and this
  // is multipart.
  const upload=async(chosen:File)=>{
    setBusy(true);setError("");setNotice("");
    try{
      const body=new FormData();body.append("file",chosen);
      const response=await fetch(`${API}/cv`,{method:"POST",body,headers:{Authorization:`Bearer ${token()}`}});
      const data=await response.json().catch(()=>({error:"Unexpected server response"}));
      if(!response.ok)throw new Error(data?.error||`Upload failed (${response.status})`);
      setNotice(`Read ${data.filename}. Review the parse below, then import and approve it.`);
      await load();setOpen(data);
    }catch(e){setError((e as Error).message)}finally{setBusy(false);if(file.current)file.current.value=""}
  };

  const approve=async(item:CVDocument,approved:boolean)=>{
    setBusy(true);setError("");
    try{
      const updated=await api<CVDocument>(`/cv/${item.id}/approve`,{method:"POST",body:JSON.stringify({approved})});
      setNotice(approved?"Approved. This CV can now be attached to applications.":"Approval withdrawn.");
      setOpen(updated);await load();
    }catch(e){setError((e as Error).message)}finally{setBusy(false)}
  };

  const importToProfile=async(item:CVDocument)=>{
    setBusy(true);setError("");
    try{
      const result=await api<CVImportResult>(`/cv/${item.id}/import`,{method:"POST",
        body:JSON.stringify({import_experiences:true,import_skills:true})});
      const employers=result.excluded_employers.length?` ${new Set(result.excluded_employers).size} employers are now excluded from matches.`:"";
      setNotice(`Imported ${result.imported_fields.length} fields, ${result.added_experiences} roles and ${result.added_skills} skills.${employers}`);
    }catch(e){setError((e as Error).message)}finally{setBusy(false)}
  };

  const remove=async(item:CVDocument)=>{
    if(!confirm(`Delete ${item.filename}? This cannot be undone.`))return;
    try{await api(`/cv/${item.id}`,{method:"DELETE"});setOpen(null);await load()}
    catch(e){setError((e as Error).message)}
  };

  return <Shell>
    <Header eyebrow="Documents" title="CV" description="Upload your CV, check what was read out of it, then import it into your profile and approve it for sending. Nothing is sent until you approve."
      action={<div><input ref={file} type="file" accept=".pdf,.docx,.md,.txt" className="hidden" onChange={e=>{const chosen=e.target.files?.[0];if(chosen)upload(chosen)}}/>
        <button className="btn-primary" disabled={busy} onClick={()=>file.current?.click()}>{busy?"Working…":"Upload a CV"}</button></div>}/>
    <ErrorMessage message={error}/><Notice message={notice}/>

    {items.length===0
      ? <Empty title="No CV yet" body="Upload a PDF, DOCX, Markdown or text CV. It is parsed on this machine, and nothing is written to your profile until you say so."/>
      : <div className="grid gap-4 lg:grid-cols-[340px_1fr]">
          <div className="space-y-2">
            {items.map(item=>
              <button key={item.id} onClick={()=>setOpen(item)}
                className={`card w-full text-left !py-3 ${open?.id===item.id?"ring-2 ring-moss":""}`}>
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-semibold">{item.label||item.filename}</p>
                    <p className="muted mt-0.5 text-xs">{item.kind==="tailored"?"Tailored":"Base"} · {item.format.toUpperCase()} · {Math.round(item.size_bytes/1024)} KB</p>
                  </div>
                  <span className={`shrink-0 rounded-lg px-2 py-1 text-[10px] font-bold ${item.approved?"bg-sage text-moss":"bg-amber-50 text-amber-700"}`}>
                    {item.approved?"APPROVED":"NEEDS REVIEW"}</span>
                </div>
              </button>)}
          </div>
          {open&&<CVDetail item={open} busy={busy} onApprove={approve} onImport={importToProfile} onDelete={remove}/>}
        </div>}
  </Shell>;
}

function CVDetail({item,busy,onApprove,onImport,onDelete}:{item:CVDocument;busy:boolean;
  onApprove:(item:CVDocument,approved:boolean)=>void;onImport:(item:CVDocument)=>void;onDelete:(item:CVDocument)=>void}){
  const [full,setFull]=useState<CVDocument|null>(null);
  // A tailored CV opens on its preview; a base CV on what was read from it.
  const [tab,setTab]=useState<"preview"|"parsed"|"text">(item.kind==="tailored"?"preview":"parsed");
  useEffect(()=>{setFull(null);setTab(item.kind==="tailored"?"preview":"parsed");api<CVDocument>(`/cv/${item.id}`).then(setFull).catch(()=>{})},[item.id,item.kind]);
  const parsed=full?.parsed;
  const tailored=parsed?.tailored;
  // A tailored CV made before previews existed has only its text to show.
  const legacyTailored=item.kind==="tailored"&&!!full&&!tailored;
  const tabs:readonly ("preview"|"parsed"|"text")[]=tailored?["preview","text"]:item.kind==="tailored"?["text"]:["parsed","text"];
  const shown=tabs.includes(tab)?tab:tabs[0];
  const tabLabel={preview:"Preview",parsed:"What was read",text:"Extracted text"};

  return <div className="card">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h2 className="text-lg font-semibold">{item.filename}</h2>
        <p className="muted text-xs">{item.kind==="tailored"?`Tailored for ${item.role_family||"a role"}`:"Base CV"}
          {item.approved_at&&` · approved ${new Date(item.approved_at).toLocaleDateString()}`}</p></div>
      <div className="flex flex-wrap gap-2">
        <a className="btn-secondary !py-2" href={`${API}/cv/${item.id}/file`} target="_blank" rel="noreferrer"
          onClick={e=>{e.preventDefault();downloadCV(item)}}>Download</a>
        {item.kind==="base"&&<button className="btn-secondary !py-2" disabled={busy} onClick={()=>onImport(item)}>Import into profile</button>}
        <button className={item.approved?"btn-secondary !py-2":"btn-primary !py-2"} disabled={busy}
          onClick={()=>onApprove(item,!item.approved)}>{item.approved?"Withdraw approval":"Approve for sending"}</button>
        <button className="btn-secondary !py-2 !text-red-700" disabled={busy} onClick={()=>onDelete(item)}>Delete</button>
      </div>
    </div>

    {!full&&<p className="muted mt-6 text-sm">Loading…</p>}
    {parsed&&!tailored&&parsed.warnings?.length>0&&
      <div className="mt-5 rounded-xl bg-amber-50 px-4 py-3">
        <p className="text-sm font-semibold text-amber-900">Check these before importing</p>
        <ul className="mt-1 space-y-0.5 text-sm text-amber-800">{parsed.warnings.map(w=><li key={w}>· {w}</li>)}</ul>
      </div>}

    {legacyTailored&&<p className="mt-5 rounded-xl bg-black/[0.03] px-4 py-3 text-sm text-ink/70">
      This CV was made before previews existed. Prepare the application again to get a fresh one you can preview.</p>}
    {full&&<>
      <div className="mt-5 flex gap-2">
        {tabs.map(name=>
          <button key={name} className={`rounded-lg px-3 py-1.5 text-xs font-semibold ${shown===name?"bg-ink text-white":"bg-black/5"}`}
            onClick={()=>setTab(name)}>{tabLabel[name]}</button>)}
      </div>
      {shown==="preview"&&tailored
        ? <TailoredPreview cv={tailored}/>
        : shown==="text"
        ? <pre className="mt-4 max-h-[30rem] overflow-auto whitespace-pre-wrap rounded-xl bg-black/[0.03] p-4 text-xs leading-relaxed">{full.extracted_text||"(no text)"}</pre>
        : parsed&&<div className="mt-4 space-y-5">
            <dl className="grid gap-x-6 gap-y-2 sm:grid-cols-2">
              {([["Name",parsed.name],["Title",parsed.current_title],["Email",parsed.email],["Phone",parsed.phone],
                 ["Location",parsed.location],["Years",parsed.years_experience?String(parsed.years_experience):""],
                 ["LinkedIn",parsed.linkedin],["GitHub",parsed.github]] as [string,string][])
                .map(([label,value])=><div key={label}>
                  <dt className="text-[11px] font-bold uppercase tracking-wide text-ink/40">{label}</dt>
                  <dd className={`truncate text-sm ${value?"":"text-ink/30"}`}>{value||"not found"}</dd></div>)}
            </dl>
            {parsed.summary&&<div><p className="text-[11px] font-bold uppercase tracking-wide text-ink/40">Summary</p>
              <p className="mt-1 text-sm leading-relaxed">{parsed.summary}</p></div>}
            {parsed.skills?.length>0&&<div>
              <p className="text-[11px] font-bold uppercase tracking-wide text-ink/40">Skills ({parsed.skills.length})</p>
              <div className="mt-1.5 flex flex-wrap gap-1.5">{parsed.skills.map(skill=>
                <span key={skill.name} className="rounded-lg bg-sage px-2 py-0.5 text-xs font-semibold text-moss">
                  {skill.name}{skill.years?` ${skill.years}y`:""}</span>)}</div></div>}
            {parsed.experiences?.length>0&&<div>
              <p className="text-[11px] font-bold uppercase tracking-wide text-ink/40">Experience ({parsed.experiences.length})</p>
              <div className="mt-1.5 space-y-1">{parsed.experiences.map((role,index)=>
                <div key={index} className="flex flex-wrap items-baseline justify-between gap-2 border-b border-black/5 py-1 last:border-0">
                  <span className="text-sm font-medium">{role.title}</span>
                  <span className="muted text-xs">{role.company||<em className="text-amber-700">employer not found</em>}
                    {" · "}{role.start_date}{role.current?" – present":role.end_date?` – ${role.end_date}`:""}</span>
                </div>)}</div></div>}
            {parsed.education?.length>0&&<div>
              <p className="text-[11px] font-bold uppercase tracking-wide text-ink/40">Education</p>
              <p className="mt-1 text-sm">{parsed.education.join(" · ")}</p></div>}
          </div>}
    </>}
  </div>;
}

// Downloading needs the bearer token, so the file is fetched and handed to the
// browser as a blob rather than linked to directly.
async function downloadCV(item:CVDocument){
  const response=await fetch(`${API}/cv/${item.id}/file`,{headers:{Authorization:`Bearer ${token()}`}});
  if(!response.ok)return;
  const blob=await response.blob();
  const url=URL.createObjectURL(blob);
  const link=document.createElement("a");
  link.href=url;link.download=item.filename||"cv.pdf";link.click();
  URL.revokeObjectURL(url);
}
