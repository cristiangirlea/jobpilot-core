"use client";

import {Shell} from "@/components/Shell";
import {ProfileNav} from "@/components/ProfileNav";
import {CompanyExclusions} from "@/components/CompanyExclusions";
import {Header} from "@/components/UI";

export default function Blacklist(){return <Shell><Header eyebrow="Candidate profile" title="Companies to ignore" description="Current and past employers are ignored automatically. Add any other company you never want recommended."/><ProfileNav/><CompanyExclusions/></Shell>}
