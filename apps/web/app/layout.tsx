import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = { title:"JobPilot Core",description:"A focused job application assistant and tracker" };
export default function RootLayout({children}:{children:React.ReactNode}){return <html lang="en"><body>{children}</body></html>}

