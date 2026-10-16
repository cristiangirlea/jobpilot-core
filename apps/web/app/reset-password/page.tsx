"use client";
import Link from "next/link";import {FormEvent,useEffect,useState} from "react";import {api} from "@/lib/api";import {ErrorMessage,Field} from "@/components/UI";
// Without a token this page asks for the email to send a link to; opened from
// that link (token in the fragment) it sets the new password.
export default function ResetPassword(){
  const [token,setToken]=useState(''),[email,setEmail]=useState(''),[password,setPassword]=useState(''),[error,setError]=useState(''),[message,setMessage]=useState(''),[busy,setBusy]=useState(false);
  useEffect(()=>{const t=new URLSearchParams(window.location.hash.slice(1)).get('token');if(t){setToken(t);history.replaceState(null,'',window.location.pathname)}},[]);
  async function submit(e:FormEvent){e.preventDefault();setError('');setMessage('');setBusy(true);try{
      if(token){const r=await api<{status:string}>('/auth/password-reset/confirm',{method:'POST',body:JSON.stringify({token,password})});setMessage(r.status);setToken('');setPassword('')}
      else{const r=await api<{status:string}>('/auth/password-reset',{method:'POST',body:JSON.stringify({email})});setMessage(r.status)}
    }catch(e){setError(e instanceof Error?e.message:'Unable to continue')}finally{setBusy(false)}}
  return <main className="grid min-h-screen place-items-center p-6"><form onSubmit={submit} className="w-full max-w-md rounded-3xl bg-white p-7 shadow-soft sm:p-10"><p className="eyebrow">Your account</p><h2 className="mt-2 text-3xl font-semibold">{token?'Choose a new password':'Reset your password'}</h2>
    <p className="muted mt-2 mb-7">{token?'The link works once and expires an hour after it was sent.':'We email a link to choose a new password, if an account uses this address.'}</p>
    <ErrorMessage message={error}/>{message&&<div className="mb-4 rounded-xl bg-emerald-50 px-4 py-3 text-sm text-emerald-800">{message}</div>}
    <div className="space-y-4">{token?<Field label="New password"><input type="password" required minLength={8} autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)}/></Field>:<Field label="Email"><input type="email" required autoComplete="email" value={email} onChange={e=>setEmail(e.target.value)}/></Field>}
    <button className="btn w-full" disabled={busy}>{busy?'Working…':token?'Set password':'Send link'}</button></div>
    <p className="muted mt-6 text-center"><Link className="font-semibold text-moss" href="/login">Back to sign in</Link></p></form></main>;
}
