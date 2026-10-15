"use client";
import {useEffect,useState} from "react";import {api,token} from "@/lib/api";import type {Me} from "@/lib/types";
// useMe reads what the signed-in account may do (GET /api/me) so the
// navigation and pages can hide what only an admin can use. The API enforces
// every one of these on its own; this only keeps members from seeing controls
// that would refuse them. It is undefined while loading and null when it
// could not be read. The answer is shared within a page load and kept per
// token, so signing in as someone else asks again.
let cached:{token:string|null;me:Promise<Me>}|null=null;
export function useMe(){const [me,setMe]=useState<Me|null|undefined>(undefined);useEffect(()=>{const t=token();if(!cached||cached.token!==t)cached={token:t,me:api<Me>("/me")};const mine=cached;mine.me.then(setMe).catch(()=>{if(cached===mine)cached=null;setMe(null)})},[]);return me}
