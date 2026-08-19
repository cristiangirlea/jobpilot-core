"use client";

import {useEffect,useState} from "react";
import {Shell} from "@/components/Shell";
import {ProfileNav} from "@/components/ProfileNav";
import {ErrorMessage,Field,Header,ListInput,Notice} from "@/components/UI";
import {api} from "@/lib/api";
import {Profile} from "@/lib/types";

const seniorityOptions=["intern","junior","mid","senior","lead","staff","principal","manager","director"];
const empty:Profile={name:"",email:"",phone:"",location:"",linkedin:"",github:"",portfolio:"",current_title:"",desired_titles:[],seniorities:[],years_experience:0,certifications:[],languages:[],salary_expectation:0,rate_expectation:0,currency:"EUR",work_preference:[],preferred_countries:[],preferred_timezones:[],work_authorization:"",notice_period:"",industry_preferences:[],summary:"",experiences:[],skills:[],projects:[]};

export default function Setup(){
  const [profile,setProfile]=useState<Profile>(empty);
  const [error,setError]=useState("");
  const [saved,setSaved]=useState("");
  const [loading,setLoading]=useState(true);
  useEffect(()=>{api<Profile>("/profile").then(value=>setProfile({...empty,...value,seniorities:value.seniorities?.length?value.seniorities:value.seniority?[value.seniority]:[]})).catch(e=>setError(e.message)).finally(()=>setLoading(false))},[]);
  const update=(key:keyof Profile,value:unknown)=>setProfile(current=>({...current,[key]:value}));
  const toggleSeniority=(value:string)=>setProfile(current=>({...current,seniorities:current.seniorities.includes(value)?current.seniorities.filter(item=>item!==value):[...current.seniorities,value]}));
  async function save(e:React.FormEvent){e.preventDefault();setError("");setSaved("");try{await api("/profile",{method:"PUT",body:JSON.stringify(profile)});setSaved("Profile saved. Existing jobs were re-scored with your selected seniority levels.")}catch(e){setError(e instanceof Error?e.message:"Unable to save profile")}}

  return <Shell>
    <Header eyebrow="Candidate profile" title="The facts matching starts from" description="Fill this manually. JobPilot never infers or invents experience."/>
    <ProfileNav/>
    {loading?<div className="card">Loading…</div>:<form onSubmit={save}>
      <ErrorMessage message={error}/><Notice message={saved}/>
      <div className="space-y-5">
        <section className="card"><h2 className="mb-4 text-lg font-semibold">Identity & links</h2><div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3"><Field label="Name"><input required value={profile.name} onChange={e=>update("name",e.target.value)}/></Field><Field label="Email"><input type="email" required value={profile.email} onChange={e=>update("email",e.target.value)}/></Field><Field label="Phone"><input value={profile.phone} onChange={e=>update("phone",e.target.value)}/></Field><Field label="Location"><input placeholder="Bucharest, Romania" value={profile.location} onChange={e=>update("location",e.target.value)}/></Field><Field label="LinkedIn"><input type="url" value={profile.linkedin} onChange={e=>update("linkedin",e.target.value)}/></Field><Field label="GitHub"><input type="url" value={profile.github} onChange={e=>update("github",e.target.value)}/></Field><Field label="Portfolio"><input type="url" value={profile.portfolio} onChange={e=>update("portfolio",e.target.value)}/></Field></div></section>
        <section className="card"><h2 className="mb-4 text-lg font-semibold">Career direction</h2><div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3"><Field label="Current title"><input value={profile.current_title} onChange={e=>update("current_title",e.target.value)}/></Field><Field label="Desired titles"><ListInput value={profile.desired_titles} onChange={value=>update("desired_titles",value)} placeholder="Backend Engineer, Go Developer"/></Field><Field label="Years of experience"><input type="number" min="0" step="0.5" value={profile.years_experience} onChange={e=>update("years_experience",+e.target.value)}/></Field><Field className="md:col-span-2 lg:col-span-3" label="Seniority levels"><div className="flex flex-wrap gap-2">{seniorityOptions.map(value=><label key={value} className={`!flex cursor-pointer items-center gap-2 rounded-xl border px-3 py-2 !normal-case !tracking-normal transition ${profile.seniorities.includes(value)?"border-moss bg-sage text-moss":"border-black/10 bg-white"}`}><input className="!w-auto" type="checkbox" checked={profile.seniorities.includes(value)} onChange={()=>toggleSeniority(value)}/><span className="capitalize">{value}</span></label>)}</div><p className="mt-2 text-xs text-ink/50">Choose every level you would genuinely accept. Matching uses the closest selected level.</p></Field><Field label="Certifications"><ListInput value={profile.certifications} onChange={value=>update("certifications",value)} placeholder="AWS Solutions Architect"/></Field><Field label="Languages"><ListInput value={profile.languages} onChange={value=>update("languages",value)} placeholder="English, Romanian"/></Field><Field label="Preferred industries"><ListInput value={profile.industry_preferences} onChange={value=>update("industry_preferences",value)} placeholder="Software, Fintech"/></Field><Field label="Profile summary" className="md:col-span-2 lg:col-span-3"><textarea value={profile.summary} onChange={e=>update("summary",e.target.value)}/></Field></div></section>
        <section className="card"><h2 className="mb-4 text-lg font-semibold">Terms & logistics</h2><div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3"><Field label="Salary expectation"><input type="number" min="0" value={profile.salary_expectation} onChange={e=>update("salary_expectation",+e.target.value)}/></Field><Field label="Contract rate expectation"><input type="number" min="0" value={profile.rate_expectation} onChange={e=>update("rate_expectation",+e.target.value)}/></Field><Field label="Currency"><input value={profile.currency} onChange={e=>update("currency",e.target.value.toUpperCase())}/></Field><Field label="Work preferences"><ListInput value={profile.work_preference} onChange={value=>update("work_preference",value)} placeholder="remote, hybrid"/></Field><Field label="Preferred countries"><ListInput value={profile.preferred_countries} onChange={value=>update("preferred_countries",value)} placeholder="Romania, Germany"/></Field><Field label="Preferred timezones"><ListInput value={profile.preferred_timezones} onChange={value=>update("preferred_timezones",value)} placeholder="EET, CET"/></Field><Field label="Work authorization"><input value={profile.work_authorization} onChange={e=>update("work_authorization",e.target.value)}/></Field><Field label="Notice period"><input value={profile.notice_period} onChange={e=>update("notice_period",e.target.value)}/></Field></div></section>
      </div>
      <div className="sticky bottom-4 mt-5 flex justify-end"><button className="btn shadow-lg">Save profile</button></div>
    </form>}
  </Shell>
}
