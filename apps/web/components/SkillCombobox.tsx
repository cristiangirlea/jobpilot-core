"use client";

import {forwardRef,KeyboardEvent,useId,useMemo,useState} from "react";
import {SkillSuggestion} from "@/lib/types";

const shownAtOnce=8;

// Why a suggestion is offered, in the terms a user scans for.
function reason(s:SkillSuggestion){
  const parts:string[]=[];
  if(s.in_cv)parts.push("in your CV");
  if(s.jobs)parts.push(`${s.jobs} matching job${s.jobs===1?"":"s"}`);
  return parts.join(" · ");
}

type Props={value:string;onChange:(value:string)=>void;suggestions:SkillSuggestion[];exclude:Set<string>};

// SkillCombobox is a free-text input that offers known skills as you type.
// Picking one only fills the input, so Level and Years can still be set before
// adding; Enter with nothing highlighted submits whatever was typed.
export const SkillCombobox=forwardRef<HTMLInputElement,Props>(function SkillCombobox({value,onChange,suggestions,exclude},ref){
  const [open,setOpen]=useState(false);
  const [active,setActive]=useState(-1);
  const id=useId();
  const options=useMemo(()=>{
    const query=value.trim().toLowerCase();
    const available=suggestions.filter(s=>!exclude.has(s.name.toLowerCase()));
    if(!query)return available.slice(0,shownAtOnce);
    const starts=available.filter(s=>s.name.toLowerCase().startsWith(query));
    const contains=available.filter(s=>!s.name.toLowerCase().startsWith(query)&&s.name.toLowerCase().includes(query));
    return [...starts,...contains].slice(0,shownAtOnce);
  },[value,suggestions,exclude]);
  const shown=open&&options.length>0;
  const close=()=>{setOpen(false);setActive(-1)};
  const pick=(s:SkillSuggestion)=>{onChange(s.name);close()};
  function onKeyDown(e:KeyboardEvent<HTMLInputElement>){
    if(e.key==="ArrowDown"||e.key==="ArrowUp"){
      if(!options.length)return;
      e.preventDefault();
      setOpen(true);
      setActive(i=>e.key==="ArrowDown"?(i+1)%options.length:i<=0?options.length-1:i-1);
    }else if(e.key==="Enter"&&shown&&active>=0){e.preventDefault();pick(options[active])}
    else if(e.key==="Escape"&&shown){e.preventDefault();close()}
  }
  return <div className="relative">
    <input ref={ref} required autoComplete="off" placeholder="Start typing, e.g. Kubernetes" value={value}
      role="combobox" aria-autocomplete="list" aria-expanded={shown} aria-controls={id} aria-activedescendant={shown&&active>=0?`${id}-${active}`:undefined}
      onChange={e=>{onChange(e.target.value);setOpen(true);setActive(-1)}} onFocus={()=>setOpen(true)} onBlur={close} onKeyDown={onKeyDown}/>
    {shown&&<ul id={id} role="listbox" className="absolute z-20 mt-1 max-h-72 w-full overflow-auto rounded-xl border border-black/10 bg-white py-1 shadow-lg">
      {options.map((s,i)=><li id={`${id}-${i}`} role="option" aria-selected={i===active} key={s.name}
        className={`flex cursor-pointer items-center justify-between gap-3 px-3.5 py-2 text-sm ${i===active?"bg-sage text-moss":""}`}
        onMouseDown={e=>{e.preventDefault();pick(s)}} onMouseEnter={()=>setActive(i)}>
        <span className="font-semibold">{s.name}</span><span className="shrink-0 text-xs text-ink/45">{reason(s)}</span>
      </li>)}
    </ul>}
  </div>;
});
