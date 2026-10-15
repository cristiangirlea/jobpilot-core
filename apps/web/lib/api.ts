export const API=process.env.NEXT_PUBLIC_API_URL||"http://localhost:8080/api";
export function token(){return typeof window!=="undefined"?localStorage.getItem("jobpilot_token"):null}
async function send(path:string,options:RequestInit={}){
  const headers=new Headers(options.headers);headers.set("Content-Type","application/json");const t=token();if(t)headers.set("Authorization",`Bearer ${t}`);
  const response=await fetch(`${API}${path}`,{...options,headers,cache:"no-store"});
  if(response.status===401&&typeof window!=="undefined"&&!path.startsWith("/auth/")){localStorage.removeItem("jobpilot_token");window.location.href="/login";throw new Error("Session expired")}
  const data=response.status===204?null:await response.json().catch(()=>({error:"Unexpected server response"}));
  if(!response.ok)throw new Error(data?.error||`Request failed (${response.status})`);return {data,response};
}
export async function api<T>(path:string,options:RequestInit={}):Promise<T>{return (await send(path,options)).data as T}
// apiPage reads one page of a list and the count the API reports for the
// whole list in X-Total-Count; without the header, the page is the list.
export async function apiPage<T>(path:string):Promise<{items:T[];total:number}>{
  const {data,response}=await send(path);const items=(data||[]) as T[];const header=response.headers.get("X-Total-Count");
  return {items,total:header!==null&&Number.isFinite(Number(header))?Number(header):items.length};
}
// jobHref is a job page's address: its readable slug, or its id until it has one.
export const jobHref=(job:{id:string;slug?:string})=>`/jobs/${job.slug||job.id}`;
export const split=(value:string)=>value.split(",").map(x=>x.trim()).filter(Boolean);
export const join=(value:string[]|undefined)=>(value||[]).join(", ");

