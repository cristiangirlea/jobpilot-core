"use client";import {useEffect} from "react";import {useRouter} from "next/navigation";import {token} from "@/lib/api";export default function Home(){const r=useRouter();useEffect(()=>r.replace(token()?'/dashboard':'/login'),[r]);return null}

