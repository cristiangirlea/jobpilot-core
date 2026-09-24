import {describe} from "@/lib/description";

// JobDescription renders a posting as headings, paragraphs and lists. The text
// is rendered as text, never as HTML, so a posting cannot inject markup.
export function JobDescription({text}:{text:string}){
  const blocks=describe(text);
  if(!blocks.length)return <p className="mt-3 text-sm text-ink/50">No description was provided.</p>;
  return <div className="mt-3 space-y-3 text-sm leading-6 text-ink/70">
    {blocks.map((block,i)=>block.kind==="heading"
      ?<h3 key={i} className="pt-2 font-semibold text-ink">{block.text}</h3>
      :block.kind==="list"
        ?<ul key={i} className="list-disc space-y-1 pl-5">{block.items.map((item,j)=><li key={j}>{item}</li>)}</ul>
        :<p key={i} className="whitespace-pre-line">{block.label&&<strong className="font-semibold text-ink">{block.label}: </strong>}{block.text}</p>)}
  </div>;
}
