"use client";import {useEffect,useState} from "react";import {useRouter} from "next/navigation";import {Shell} from "@/components/Shell";import {Header,Field,ErrorMessage,CopyButton} from "@/components/UI";import {api} from "@/lib/api";import type {AgentToken,AgentTokenCreated} from "@/lib/types";

function when(value?:string){return value?new Date(value).toLocaleDateString():"—"}

function Tokens(){
  const [tokens,setTokens]=useState<AgentToken[]>([]);
  const [name,setName]=useState("");
  const [canWrite,setCanWrite]=useState(false);
  const [days,setDays]=useState(0);
  const [created,setCreated]=useState<AgentTokenCreated|null>(null);
  const [error,setError]=useState("");
  const load=()=>api<AgentToken[]>("/tokens").then(setTokens).catch(e=>setError(e.message));
  useEffect(()=>{load()},[]);
  const create=async()=>{
    setError("");
    try{
      const made=await api<AgentTokenCreated>("/tokens",{method:"POST",body:JSON.stringify({name,scopes:canWrite?["read","write"]:["read"],expires_in_days:days})});
      setCreated(made);setName("");setCanWrite(false);setDays(0);load();
    }catch(e){setError((e as Error).message)}
  };
  const revoke=async(id:string)=>{
    setError("");
    try{await api(`/tokens/${id}`,{method:"DELETE"});load()}catch(e){setError((e as Error).message)}
  };
  const live=tokens.filter(t=>!t.revoked_at);
  return <section className="card md:col-span-2">
    <h2 className="font-semibold">Agent tokens</h2>
    <p className="muted mt-2 mb-4">Long-lived tokens for the MCP server, scripts, or anything that is not this browser. A token can be revoked at any time, which takes effect immediately, and it never gives access to this page.</p>
    <ErrorMessage message={error}/>
    {created&&<div className="mb-4 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3">
      <p className="text-sm font-semibold text-amber-900">Copy this now. It is not stored and cannot be shown again.</p>
      <div className="mt-2 flex items-center gap-2"><code className="flex-1 overflow-x-auto rounded-lg bg-white px-3 py-2 text-xs">{created.token}</code><CopyButton text={created.token}/></div>
      <p className="muted mt-3 text-xs">Register it with Claude Code:</p>
      <div className="mt-1 flex items-center gap-2"><code className="flex-1 overflow-x-auto rounded-lg bg-white px-3 py-2 text-xs">{`claude mcp add jobpilot -e JOBPILOT_TOKEN=${created.token} -- /path/to/jobpilot-mcp`}</code><CopyButton text={`claude mcp add jobpilot -e JOBPILOT_TOKEN=${created.token} -- /path/to/jobpilot-mcp`}/></div>
      <button className="btn-secondary mt-3 !px-3 !py-1.5 text-xs" onClick={()=>setCreated(null)}>Done</button>
    </div>}
    <div className="grid gap-3 sm:grid-cols-[1fr_auto_auto_auto] sm:items-end">
      <Field label="Name"><input value={name} placeholder="Claude Code on this laptop" onChange={e=>setName(e.target.value)}/></Field>
      <Field label="Access"><select value={canWrite?"write":"read"} onChange={e=>setCanWrite(e.target.value==="write")}><option value="read">Read only</option><option value="write">Read and write</option></select></Field>
      <Field label="Expires"><select value={days} onChange={e=>setDays(Number(e.target.value))}><option value={0}>Never</option><option value={30}>30 days</option><option value={90}>90 days</option><option value={365}>365 days</option></select></Field>
      <button className="btn-primary" disabled={!name.trim()} onClick={create}>Create token</button>
    </div>
    {live.length>0&&<table className="mt-5 w-full text-sm"><thead><tr className="text-left text-xs uppercase tracking-wide text-ink/45"><th className="pb-2">Name</th><th className="pb-2">Token</th><th className="pb-2">Access</th><th className="pb-2">Last used</th><th className="pb-2">Expires</th><th className="pb-2"></th></tr></thead><tbody>
      {live.map(t=><tr key={t.id} className="border-t border-black/5"><td className="py-2 font-semibold">{t.name}</td><td className="py-2"><code className="text-xs">jpat_{t.token_prefix}…</code></td><td className="py-2">{t.scopes.includes("write")?"Read and write":"Read only"}</td><td className="py-2">{when(t.last_used_at)}</td><td className="py-2">{t.expires_at?when(t.expires_at):"Never"}</td><td className="py-2 text-right"><button className="btn-secondary !px-3 !py-1.5 text-xs" onClick={()=>revoke(t.id)}>Revoke</button></td></tr>)}
    </tbody></table>}
    {live.length===0&&<p className="muted mt-4 text-sm">No active tokens.</p>}
  </section>;
}

export default function Settings(){
  const r=useRouter();
  return <Shell>
    <Header eyebrow="Workspace" title="Settings" description="JobPilot Core is configured through local environment variables."/>
    <div className="grid gap-5 md:grid-cols-2">
      <section className="card"><h2 className="font-semibold">Generation mode</h2><p className="muted mt-2">Deterministic templates are active. They use only profile, experience, skill, and project data—no API key required.</p><span className="mt-4 inline-block rounded-full bg-emerald-50 px-3 py-1 text-xs font-bold text-emerald-700">OFFLINE-SAFE</span></section>
      <section className="card"><h2 className="font-semibold">Safety boundaries</h2><p className="muted mt-2">This product never submits applications, bypasses CAPTCHA, or automates job boards. Apply links always open for manual review.</p></section>
      <Tokens/>
      <section className="card md:col-span-2"><h2 className="font-semibold">Session</h2><p className="muted mt-2 mb-4">Signing out removes the local access token from this browser.</p><button className="btn-secondary" onClick={()=>{localStorage.removeItem('jobpilot_token');r.push('/login')}}>Sign out</button></section>
    </div>
  </Shell>;
}
