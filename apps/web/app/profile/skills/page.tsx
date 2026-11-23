"use client";

import {FormEvent,useEffect,useMemo,useRef,useState} from "react";
import {Shell} from "@/components/Shell";
import {ProfileNav} from "@/components/ProfileNav";
import {SkillCombobox} from "@/components/SkillCombobox";
import {ErrorMessage,Field,Header} from "@/components/UI";
import {api,notRescored} from "@/lib/api";
import {Skill,SkillSuggestion} from "@/lib/types";

const blank=():Skill=>({name:"",level:"",years:0});
const key=(name:string)=>name.trim().toLowerCase();
// The API lists skills by lower(name); showing them in that order means a
// reload never reshuffles the list.
const byName=(a:Skill,b:Skill)=>key(a.name)<key(b.name)?-1:key(a.name)>key(b.name)?1:0;
const reason=(e:unknown)=>e instanceof Error?e.message:"unknown error";

export default function SkillsPage(){
  // items is what the server has; adding and removing are changes on their
  // way there. The list shows all three, so undoing a failed save is only a
  // matter of forgetting the change.
  const [items,setItems]=useState<Skill[]>([]);
  const [adding,setAdding]=useState<Record<string,Skill>>({});
  const [removing,setRemoving]=useState<string[]>([]);
  const [suggestions,setSuggestions]=useState<SkillSuggestion[]>([]);
  const [form,setForm]=useState(blank());
  const [error,setError]=useState("");
  const input=useRef<HTMLInputElement>(null);
  // Every save re-scores all of the user's matches. Sending them one at a time
  // keeps two re-scores from overlapping, where the one that read the older
  // profile could finish last and leave its scores behind.
  const queue=useRef(Promise.resolve());
  const enqueue=(work:()=>Promise<void>)=>{queue.current=queue.current.then(work)};
  // Suggestions only help; the page works the same without them.
  const loadSuggestions=()=>api<SkillSuggestion[]>("/skills/suggestions").then(setSuggestions).catch(()=>{});
  const loadSkills=()=>api<Skill[]>("/skills").then(list=>{setItems(list);return list});
  useEffect(()=>{loadSkills().catch(e=>setError(e.message));loadSuggestions()},[]);
  // A queued save lives only in this page until it is sent, so leaving now
  // would lose it; the browser asks first.
  const saving=Object.keys(adding).length+removing.length>0;
  useEffect(()=>{
    if(!saving)return;
    const warn=(e:BeforeUnloadEvent)=>{e.preventDefault();e.returnValue=""};
    window.addEventListener("beforeunload",warn);
    return ()=>window.removeEventListener("beforeunload",warn);
  },[saving]);

  const rows=useMemo(()=>[
    ...items.filter(x=>!adding[key(x.name)]&&!removing.includes(x.id??"")),
    ...Object.values(adding),
  ].sort(byName),[items,adding,removing]);
  const held=useMemo(()=>new Set(rows.map(x=>key(x.name))),[rows]);

  function add(e:FormEvent){
    e.preventDefault();
    const typed=form.name.trim();
    if(!typed)return;
    // Typing a listed skill in another case updates it instead of adding a
    // near-duplicate beside it. Items being removed count too: re-adding one
    // keeps its spelling, so a failed removal cannot leave two of it.
    const listed=[...items,...Object.values(adding)].find(x=>key(x.name)===key(typed));
    const skill={...form,name:listed?.name??typed};
    const k=key(skill.name);
    setError("");
    setAdding(a=>({...a,[k]:skill}));
    setForm(blank());
    input.current?.focus();
    enqueue(async()=>{
      try{
        const saved=await api<Skill>("/skills",{method:"POST",body:JSON.stringify(skill)});
        setItems(list=>[...list.filter(x=>x.id!==saved.id),saved]);
      }catch(err){
        // The skill is saved before matches are re-scored, so a failure may
        // come after it was stored. Where the API says so, its words stand;
        // otherwise ask the server rather than guess.
        const stored=await loadSkills().then(list=>list.some(x=>x.name===skill.name)).catch(()=>false);
        setError(notRescored(err)?`${skill.name}: ${reason(err)}`:stored?`${skill.name} was saved, but your matches were not re-scored: ${reason(err)}`:`${skill.name} was not saved: ${reason(err)}`);
      }
      // A later edit of the same skill may be queued behind this one; leave it.
      setAdding(a=>{if(a[k]!==skill)return a;const next={...a};delete next[k];return next});
    });
  }

  function remove(skill:Skill){
    const id=skill.id;
    if(!id)return;
    setError("");
    setRemoving(r=>[...r,id]);
    enqueue(async()=>{
      try{
        await api(`/skills/${id}`,{method:"DELETE"});
        setItems(list=>list.filter(x=>x.id!==id));
        loadSuggestions();
      }catch(err){
        const kept=await loadSkills().then(list=>list.some(x=>x.id===id)).catch(()=>true);
        setError(notRescored(err)?`${skill.name}: ${reason(err)}`:kept?`${skill.name} was not removed: ${reason(err)}`:`${skill.name} was removed, but your matches were not re-scored: ${reason(err)}`);
      }
      setRemoving(r=>r.filter(x=>x!==id));
    });
  }

  return <Shell>
    <Header eyebrow="Candidate profile" title="Skills" description="Only skills listed here can appear in generated application text."/>
    <ProfileNav/>
    <ErrorMessage message={error}/>
    <form className="card mb-5 grid items-end gap-3 md:grid-cols-[2fr_1fr_1fr_auto]" onSubmit={add}>
      <Field label="Skill"><SkillCombobox ref={input} value={form.name} onChange={name=>setForm(f=>({...f,name}))} suggestions={suggestions} exclude={held}/></Field>
      <Field label="Level"><select value={form.level} onChange={e=>setForm({...form,level:e.target.value})}><option value="">Unspecified</option>{["beginner","intermediate","advanced","expert"].map(x=><option key={x}>{x}</option>)}</select></Field>
      <Field label="Years"><input type="number" min="0" step="0.5" value={form.years} onChange={e=>setForm({...form,years:+e.target.value})}/></Field>
      <button className="btn">Add skill</button>
    </form>
    {rows.length?<div className="flex flex-wrap gap-2">{rows.map(x=>{
      // Only a saved skill has an id; until then it is on its way.
      const saving=!x.id;
      // The API leaves an empty level out, so each part is shown only when set.
      const detail=saving?"saving…":[x.level,x.years?`${x.years}y`:""].filter(Boolean).join(" · ");
      return <div key={x.id??`new:${key(x.name)}`} aria-busy={saving||undefined} className={`rounded-full border border-black/5 bg-white px-4 py-2 text-sm shadow-sm transition-opacity ${saving?"opacity-60":""}`}>
        <span className="font-semibold">{x.name}</span>
        {detail&&<span className="ml-2 text-ink/40">{detail}</span>}
        {!saving&&<button type="button" className="ml-3 text-red-500" aria-label={`Remove ${x.name}`} onClick={()=>remove(x)}>×</button>}
      </div>;
    })}</div>:<p className="rounded-xl bg-paper px-4 py-3 text-sm text-ink/50">No skills yet. Start typing above: suggestions come from your CV and from the jobs you match.</p>}
  </Shell>;
}
