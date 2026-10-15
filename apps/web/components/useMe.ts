"use client";
import {useEffect,useState} from "react";import {api} from "@/lib/api";import type {Me} from "@/lib/types";
// useMe reads what the signed-in account may do (GET /api/me), once per page
// load, so the navigation and pages can hide what only an admin can use. The
// API enforces every one of these on its own; this only keeps members from
// seeing controls that would refuse them.
let pending:Promise<Me>|null=null;
export function useMe(){const [me,setMe]=useState<Me|null>(null);useEffect(()=>{pending??=api<Me>("/me").catch(e=>{pending=null;throw e});pending.then(setMe).catch(()=>setMe(null))},[]);return me}
