import {backupDraftLabel,TailoredCV} from "@/lib/types";

// TailoredPreview shows a tailored CV as it will read, beside what it was
// tailored against: which of the job's technologies the CV shows, and which
// it does not. Missing ones are never added to the CV; they are listed so the
// user can decide whether the role is worth it.
export function TailoredPreview({cv:stored}:{cv:TailoredCV}){
  // CVs stored before the API always sent lists may carry null for an empty one.
  const cv={...stored,contact:stored.contact??[],skills:stored.skills??[],experiences:stored.experiences??[],
    education:stored.education??[],certifications:stored.certifications??[],languages:stored.languages??[]};
  const highlighted=cv.highlighted??[],missing=cv.missing??[];
  const wanted=new Set(highlighted.map(x=>x.toLowerCase()));
  return <div className="mt-4 grid gap-4 xl:grid-cols-[1fr_260px]">
    <article className="rounded-xl border border-black/10 bg-white p-6 text-[13px] leading-relaxed text-ink shadow-sm">
      <header className="border-b border-black/10 pb-3">
        <h3 className="text-xl font-semibold">{cv.name||"(no name)"}</h3>
        {cv.headline&&<p className="text-sm text-ink/70">{cv.headline}</p>}
        {cv.contact.length>0&&<p className="mt-1 break-words text-xs text-ink/50">{cv.contact.join(" · ")}</p>}
      </header>
      {cv.summary&&<Section title="Summary"><p>{cv.summary}</p></Section>}
      {cv.skills.length>0&&<Section title="Skills">
        <div className="flex flex-wrap gap-1.5">{cv.skills.map(skill=>{
          const plain=skill.replace(/\s\(\d+y\)$/,"").toLowerCase();
          return <span key={skill} className={`rounded-md px-1.5 py-0.5 text-xs ${wanted.has(plain)?"bg-sage font-semibold text-moss":"bg-black/[0.04]"}`}>{skill}</span>;
        })}</div>
      </Section>}
      {cv.experiences.length>0&&<Section title="Experience">
        <div className="space-y-4">{cv.experiences.map((role,index)=><div key={index}>
          <div className="flex flex-wrap items-baseline justify-between gap-x-3">
            <p className="font-semibold">{role.title}{role.company&&<span className="font-normal text-ink/60"> — {role.company}</span>}</p>
            <p className="text-xs text-ink/50">{role.period}</p>
          </div>
          {role.context&&<p className="mt-0.5 text-xs italic text-ink/60">{role.context}</p>}
          {role.bullets.length>0&&<ul className="mt-1 list-disc space-y-0.5 pl-5">{role.bullets.map((bullet,i)=><li key={i}>{bullet}</li>)}</ul>}
        </div>)}</div>
      </Section>}
      {([["Education",cv.education],["Certifications",cv.certifications],["Languages",cv.languages]] as [string,string[]][])
        .filter(([,lines])=>lines?.length>0)
        .map(([title,lines])=><Section key={title} title={title}>{lines.map(line=><p key={line}>{line}</p>)}</Section>)}
    </article>

    <aside className="space-y-4 text-sm">
      <div className="rounded-xl bg-black/[0.03] p-4">
        <p className="text-[11px] font-bold uppercase tracking-wide text-ink/40">Summary</p>
        <p className="mt-1">{cv.summary_source==="ai"||cv.summary_source==="ai_backup"
          ?<span className="font-semibold text-amber-700">{cv.summary_source==="ai_backup"?backupDraftLabel:"Drafted by AI from your facts. Read it before approving."}</span>
          :"Your own summary, unchanged."}</p>
      </div>
      <div className="rounded-xl bg-black/[0.03] p-4">
        <p className="text-[11px] font-bold uppercase tracking-wide text-ink/40">The job asks for, and your CV shows</p>
        {highlighted.length
          ?<div className="mt-1.5 flex flex-wrap gap-1.5">{highlighted.map(x=><span key={x} className="rounded-md bg-sage px-1.5 py-0.5 text-xs font-semibold text-moss">{x}</span>)}</div>
          :<p className="muted mt-1 text-xs">None of the job's technologies.</p>}
      </div>
      <div className="rounded-xl bg-black/[0.03] p-4">
        <p className="text-[11px] font-bold uppercase tracking-wide text-ink/40">The job asks for, not on your CV</p>
        {missing.length
          ?<><div className="mt-1.5 flex flex-wrap gap-1.5">{missing.map(x=><span key={x} className="rounded-md bg-amber-50 px-1.5 py-0.5 text-xs font-semibold text-amber-800">{x}</span>)}</div>
            <p className="muted mt-2 text-xs">Not added: the CV only shows what you have.</p></>
          :<p className="muted mt-1 text-xs">Nothing missing.</p>}
      </div>
      <p className="muted px-1 text-xs">Your CV covers {Math.round(cv.alignment*100)}% of this job's technologies.</p>
    </aside>
  </div>;
}

function Section({title,children}:{title:string;children:React.ReactNode}){
  return <section className="mt-4">
    <h4 className="mb-1.5 text-[11px] font-bold uppercase tracking-[0.12em] text-ink/50">{title}</h4>
    {children}
  </section>;
}
