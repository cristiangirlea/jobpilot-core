// Shapes a job description for display. The API stores postings as text that
// keeps paragraphs (blank lines) and list items ("• " lines); this turns that
// into blocks a page can render, with short "Label:" lines as headings.

export type DescriptionBlock =
  | {kind:"heading";text:string}
  | {kind:"paragraph";label?:string;text:string}
  | {kind:"list";items:string[]};

const bullet="• ";
// A label is a short phrase before a colon: "The role:", "What We Offer:".
const labelled=/^([^:\n]{1,60}):\s+([\s\S]+)$/;
const wordCount=(s:string)=>s.trim().split(/\s+/).length;
const isLabel=(s:string)=>/^[A-Z]/.test(s.trim())&&wordCount(s)<=6;

// splitFlat breaks up a posting that was stored before paragraphs were kept
// and so has no line breaks at all. Its section labels still end in a colon,
// so a paragraph starts before each one: at the last sentence end when the
// words from there to the colon read as a label, otherwise at the last few
// capitalised words before the colon.
export function splitFlat(text:string):string[]{
  const parts:string[]=[];
  let start=0;
  for(const match of text.matchAll(/:\s/g)){
    const before=text.slice(start,match.index);
    const sentenceEnd=Math.max(before.lastIndexOf(". "),before.lastIndexOf("! "),before.lastIndexOf("? "),before.lastIndexOf(") "));
    let cut=sentenceEnd>=0?sentenceEnd+2:0;
    if(!isLabel(before.slice(cut))){
      const capitalised=before.match(/(?:^|\s)((?:[A-Z]\S*\s){0,2}[A-Z]\S*)$/);
      if(!capitalised)continue;
      cut=before.length-capitalised[1].length;
    }
    if(cut>0){
      parts.push(before.slice(0,cut).trim());
      start+=cut;
    }
  }
  parts.push(text.slice(start).trim());
  return parts.filter(Boolean);
}

export function describe(text:string):DescriptionBlock[]{
  const trimmed=text.trim();
  if(!trimmed)return [];
  const chunks=trimmed.includes("\n")?trimmed.split(/\n{2,}/):splitFlat(trimmed);
  const blocks:DescriptionBlock[]=[];
  for(const chunk of chunks){
    let lines:string[]=[];
    const flush=()=>{
      if(!lines.length)return;
      const text=lines.join("\n");
      const match=text.match(labelled);
      if(lines.length===1&&text.endsWith(":")&&isLabel(text.slice(0,-1)))blocks.push({kind:"heading",text:text.slice(0,-1)});
      else if(match&&isLabel(match[1]))blocks.push({kind:"paragraph",label:match[1],text:match[2]});
      else blocks.push({kind:"paragraph",text});
      lines=[];
    };
    for(const line of chunk.split("\n")){
      if(line.startsWith(bullet)){
        flush();
        const last=blocks[blocks.length-1];
        if(last?.kind==="list")last.items.push(line.slice(bullet.length));
        else blocks.push({kind:"list",items:[line.slice(bullet.length)]});
      }else lines.push(line);
    }
    flush();
  }
  return blocks;
}
