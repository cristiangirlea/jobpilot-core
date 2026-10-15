"use client";
import Link from "next/link";import {useEffect,useRef,useState} from "react";import {useRouter} from "next/navigation";import {api} from "@/lib/api";import {ErrorMessage} from "@/components/UI";
// The API's Google callback sends the browser here with a single-use code,
// which is exchanged for the session token, or with ?link=1 links the Google
// account to the signed-in one; the code then leaves the address bar. Both
// calls send cookies: the code works only with the binding cookie the callback
// set in this browser.
// The ref keeps a second run of the effect (React's development double run)
// from spending the code again.
export default function GoogleSignIn(){const router=useRouter();const [error,setError]=useState(''),started=useRef(false);useEffect(()=>{if(started.current)return;started.current=true;const params=new URLSearchParams(window.location.search),code=params.get('code')||'',link=params.get('link')==='1';window.history.replaceState(null,'','/login/google');const done=link?api('/auth/google/link',{method:'POST',credentials:'include',body:JSON.stringify({code})}).then(()=>router.replace('/settings?google=linked')):api<{token:string}>('/auth/google/exchange',{method:'POST',credentials:'include',body:JSON.stringify({code})}).then(r=>{localStorage.setItem('jobpilot_token',r.token);router.replace('/dashboard')});done.catch(e=>setError(e instanceof Error?e.message:'Unable to continue'))},[router]);return <main className="grid min-h-screen place-items-center p-6"><div className="w-full max-w-md rounded-3xl bg-white p-7 shadow-soft sm:p-10"><h2 className="text-2xl font-semibold">Signing you in…</h2><div className="mt-4"><ErrorMessage message={error}/></div>{error&&<Link className="font-semibold text-moss" href="/login">Back to sign in</Link>}</div></main>}
