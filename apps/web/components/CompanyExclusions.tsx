"use client";

import {FormEvent,useEffect,useState} from "react";
import {api} from "@/lib/api";
import {CompanyExclusion} from "@/lib/types";
import {ErrorMessage,Field,Notice} from "./UI";

const newEntry=():CompanyExclusion=>({company_name:"",company_domain:"",reason:""});

export function CompanyExclusions(){
  const [items,setItems]=useState<CompanyExclusion[]>([]);
  const [form,setForm]=useState(newEntry());
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const load=()=>api<CompanyExclusion[]>("/blacklist").then(setItems).catch(e=>setError(e.message));
  useEffect(()=>{load()},[]);
  async function add(e:FormEvent){e.preventDefault();setError("");setNotice("");try{await api("/blacklist",{method:"POST",body:JSON.stringify(form)});setForm(newEntry());setNotice("Company excluded. Existing jobs were re-evaluated.");load()}catch(e){setError(e instanceof Error?e.message:"Unable to exclude company")}}
  async function remove(id?:string){if(!id)return;setError("");setNotice("");try{await api(`/blacklist/${id}`,{method:"DELETE"});setNotice("Company removed from exclusions. Existing jobs were re-evaluated.");load()}catch(e){setError(e instanceof Error?e.message:"Unable to remove company")}}
  return <section className="card">
    <div className="mb-5"><h2 className="text-lg font-semibold">Excluded companies</h2><p className="muted mt-1">Jobs from these companies are hard-rejected. Add the domain when known to catch alternate brand names.</p></div>
    <ErrorMessage message={error}/><Notice message={notice}/>
    <form className="grid items-end gap-3 md:grid-cols-[1.25fr_1fr_1.5fr_auto]" onSubmit={add}>
      <Field label="Company name"><input required placeholder="Example Holdings Ltd" value={form.company_name} onChange={e=>setForm({...form,company_name:e.target.value})}/></Field>
      <Field label="Company domain"><input placeholder="example.com" value={form.company_domain} onChange={e=>setForm({...form,company_domain:e.target.value})}/></Field>
      <Field label="Reason (optional)"><input placeholder="Not interested" value={form.reason} onChange={e=>setForm({...form,reason:e.target.value})}/></Field>
      <button className="btn">Exclude company</button>
    </form>
    {items.length?<div className="mt-5 grid gap-2 md:grid-cols-2">{items.map(item=><div className="flex items-center justify-between gap-3 rounded-xl border border-black/5 bg-paper px-4 py-3" key={item.id}><div className="min-w-0"><p className="truncate font-semibold">{item.company_name}</p><p className="truncate text-xs text-ink/50">{item.company_domain||item.normalized_name}{item.reason?` · ${item.reason}`:""}</p></div><button type="button" className="shrink-0 text-xs font-semibold text-red-600" onClick={()=>remove(item.id)}>Remove</button></div>)}</div>:<p className="mt-5 rounded-xl bg-paper px-4 py-3 text-sm text-ink/50">No extra companies excluded yet. Current and previous employers are excluded automatically.</p>}
  </section>;
}
