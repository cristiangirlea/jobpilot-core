"use client";
import Link from "next/link";import {useEffect,useState} from "react";import {api} from "@/lib/api";import {ErrorMessage} from "@/components/UI";
// The link's token is in the fragment, which never reaches a server log.
export default function VerifyEmail(){
  const [state,setState]=useState<'working'|'done'|'failed'>('working');const [error,setError]=useState('');
  useEffect(()=>{const t=new URLSearchParams(window.location.hash.slice(1)).get('token');if(!t){setState('failed');setError('This link has no token. Open the link from the email again.');return}
    history.replaceState(null,'',window.location.pathname);
    api('/auth/verify-email',{method:'POST',body:JSON.stringify({token:t})}).then(()=>setState('done')).catch(e=>{setState('failed');setError(e.message)})},[]);
  return <main className="grid min-h-screen place-items-center p-6"><div className="w-full max-w-md rounded-3xl bg-white p-7 shadow-soft sm:p-10"><p className="eyebrow">Your account</p><h2 className="mt-2 text-3xl font-semibold">Verify your email</h2>
    <div className="mt-6"><ErrorMessage message={error}/>{state==='working'&&<p className="muted">Checking the link…</p>}{state==='done'&&<p className="muted">Your email address is verified.</p>}</div>
    <p className="muted mt-6"><Link className="font-semibold text-moss" href="/dashboard">Go to JobPilot</Link></p></div></main>;
}
