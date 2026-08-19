const API=process.env.NEXT_PUBLIC_API_URL||"http://localhost:8080/api";
export function token(){return typeof window!=="undefined"?localStorage.getItem("jobpilot_token"):null}
export async function api<T>(path:string,options:RequestInit={}):Promise<T>{
  const headers=new Headers(options.headers);headers.set("Content-Type","application/json");const t=token();if(t)headers.set("Authorization",`Bearer ${t}`);
  const response=await fetch(`${API}${path}`,{...options,headers,cache:"no-store"});
  if(response.status===401&&typeof window!=="undefined"&&!path.startsWith("/auth/")){localStorage.removeItem("jobpilot_token");window.location.href="/login";throw new Error("Session expired")}
  const data=response.status===204?null:await response.json().catch(()=>({error:"Unexpected server response"}));
  if(!response.ok)throw new Error(data?.error||`Request failed (${response.status})`);return data as T;
}
export const split=(value:string)=>value.split(",").map(x=>x.trim()).filter(Boolean);
export const join=(value:string[]|undefined)=>(value||[]).join(", ");

