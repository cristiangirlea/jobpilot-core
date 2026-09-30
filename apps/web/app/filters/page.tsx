"use client";

import {FormEvent,useEffect,useState} from "react";
import {Shell} from "@/components/Shell";
import {ErrorMessage,Field,Header,ListInput,Notice} from "@/components/UI";
import {api,join} from "@/lib/api";
import {Filter} from "@/lib/types";
import {CompanyExclusions} from "@/components/CompanyExclusions";

const newFilter=():Filter=>({
  name:"My active search",active:true,included_titles:[],excluded_title_words:[],
  locations:[],remote_types:["remote"],min_salary:0,currency:"EUR",
  employment_types:[],excluded_industries:[],required_skills:[],
  included_technologies:[],excluded_technologies:[],max_age_days:30,
});

export default function Filters(){
  const [items,setItems]=useState<Filter[]>([]);
  const [form,setForm]=useState<Filter>(newFilter());
  const [error,setError]=useState("");
  const [saved,setSaved]=useState("");
  const load=()=>api<Filter[]>("/filters").then(setItems).catch(e=>setError(e.message));
  useEffect(()=>{load()},[]);

  const list=(key:keyof Filter)=>(value:string[])=>setForm(current=>({...current,[key]:value}));
  async function save(e:FormEvent){
    e.preventDefault();setError("");setSaved("");
    try{
      await api(`/filters${form.id?`/${form.id}`:""}`,{method:form.id?"PUT":"POST",body:JSON.stringify(form)});
      setForm(newFilter());setSaved("Filter saved. Existing jobs were re-evaluated with the active rules.");load();
    }catch(e){setError(e instanceof Error?e.message:"Unable to save filter")}
  }
  function edit(item:Filter){setForm({...item,included_technologies:item.included_technologies||[],excluded_technologies:item.excluded_technologies||[]});window.scrollTo({top:0,behavior:"smooth"})}
  async function remove(id?:string){if(!id)return;setError("");try{await api(`/filters/${id}`,{method:"DELETE"});load()}catch(e){setError(e instanceof Error?e.message:"Unable to delete filter")}}

  return <Shell>
    <Header eyebrow="Matching rules" title="Filters that say no early" description="Use comma-separated values. Fields stay stable while you type, and every hard rejection explains which rule fired."/>
    <ErrorMessage message={error}/><Notice message={saved}/>
    <form className="card" onSubmit={save}>
      <div className="grid gap-6 lg:grid-cols-3">
        <section className="space-y-4">
          <div><h2 className="font-semibold">Role</h2><p className="muted mt-1">Leave a list empty to accept any value.</p></div>
          <Field label="Preset name"><input required value={form.name} onChange={e=>setForm({...form,name:e.target.value})}/></Field>
          <Field label="Included titles"><ListInput value={form.included_titles} onChange={list("included_titles")} placeholder="Backend Engineer, Go Developer"/></Field>
          <Field label="Excluded title words"><ListInput value={form.excluded_title_words} onChange={list("excluded_title_words")} placeholder="unpaid, commission"/></Field>
          <Field label="Employment types"><ListInput value={form.employment_types} onChange={list("employment_types")} placeholder="full-time, contract"/></Field>
          <Field label="Excluded industries"><ListInput value={form.excluded_industries} onChange={list("excluded_industries")} placeholder="gambling, crypto"/></Field>
        </section>
        <section className="space-y-4">
          <div><h2 className="font-semibold">Technology</h2><p className="muted mt-1">Required technology means at least one must appear in the title or description.</p></div>
          <Field label="Required technologies"><ListInput value={form.included_technologies} onChange={list("included_technologies")} placeholder="Go, TypeScript, PostgreSQL"/></Field>
          <Field label="Excluded technologies"><ListInput value={form.excluded_technologies} onChange={list("excluded_technologies")} placeholder="PHP, WordPress"/></Field>
          <Field label="Skills used for score"><ListInput value={form.required_skills} onChange={list("required_skills")} placeholder="Go, Docker, AWS"/></Field>
          <div className="rounded-xl bg-sage/70 p-4 text-sm text-ink/70">Required technologies are hard filters. The 30-point skill score is the share of the technologies a job mentions that your profile holds, weighted by how often it mentions each. Skills used for score count too wherever a job names them, even ones outside JobPilot's vocabulary.</div>
        </section>
        <section className="space-y-4">
          <div><h2 className="font-semibold">Location & compensation</h2><p className="muted mt-1">Remote jobs outside the location list are flagged for review, not silently discarded.</p></div>
          <Field label="Allowed locations"><ListInput value={form.locations} onChange={list("locations")} placeholder="Romania, Europe, EMEA"/></Field>
          <Field label="Remote types"><ListInput value={form.remote_types} onChange={list("remote_types")} placeholder="remote, hybrid"/></Field>
          <div className="grid grid-cols-[1fr_100px] gap-3"><Field label="Minimum salary / rate"><input type="number" min="0" value={form.min_salary} onChange={e=>setForm({...form,min_salary:+e.target.value})}/></Field><Field label="Currency"><input value={form.currency} onChange={e=>setForm({...form,currency:e.target.value.toUpperCase()})}/></Field></div>
          <Field label="Maximum posting age (days)"><input type="number" min="0" value={form.max_age_days} onChange={e=>setForm({...form,max_age_days:+e.target.value})}/></Field>
          <label className="!flex items-center gap-2 !normal-case !tracking-normal"><input className="!w-auto" type="checkbox" checked={form.active} onChange={e=>setForm({...form,active:e.target.checked})}/> Use this preset for matching</label>
        </section>
      </div>
      <div className="mt-6 flex gap-2"><button className="btn">{form.id?"Update preset":"Save preset"}</button>{form.id&&<button type="button" className="btn-secondary" onClick={()=>setForm(newFilter())}>Cancel</button>}</div>
    </form>
    <div className="mt-7"><CompanyExclusions/></div>
    <h2 className="mb-3 mt-7 text-lg font-semibold">Saved presets</h2>
    <div className="space-y-2">{items.map(item=><div className="card flex flex-wrap items-center justify-between gap-3 !p-4" key={item.id}><div><div className="flex items-center gap-2"><p className="font-semibold">{item.name}</p>{item.active&&<span className="rounded-full bg-emerald-50 px-2 py-1 text-[10px] font-bold text-emerald-700">ACTIVE</span>}</div><p className="muted mt-1">{join(item.included_titles)||"Any title"} · {join(item.remote_types)||"Any arrangement"} · {join(item.included_technologies||[])||"Any technology"}</p></div><div className="flex gap-2"><button className="btn-secondary !py-1.5" onClick={()=>edit(item)}>Edit</button><button className="text-xs text-red-600" onClick={()=>remove(item.id)}>Delete</button></div></div>)}</div>
  </Shell>
}
