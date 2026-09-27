package cv

import (
	"strings"
	"testing"
)

// pipeLayoutCV reproduces a common single-column export: a headline and a
// contact line split by pipes, skills as "Category: a, b, c" lines that wrap,
// and role headers "Company | Title | Location | Dates" that wrap across lines
// at either end. Content is invented; the layout is what is under test.
const pipeLayoutCV = `JANE DOE
Lead Full - Stack & AI Engineer | Python (FastAPI) | Go | TypeScript / React / Next.js | AWS | Kubernetes | LLM,
RAG & Agentic AI
Portugal | Remote (EU time zone) | +351 912 345 678 | jane.doe@example.com | janedoe.dev |
linkedin.com/in/jane-doe | github.com/janedoe
PROFESSIONAL SUMMARY
Full-stack engineer with 11+ years building production systems, most recently data platforms and
AI - assisted workflows.
CORE SKILLS
Languages: Go (preferred backend language), Python, TypeScript, SQL; legacy Perl (maintenance
and migration)
Backend & APIs: FastAPI, Node.js (Express, NestJS), REST API design and scalable APIs, gRPC / Protobuf
Cloud & Infrastructure: AWS (EKS, Lambda, S3), Docker, Kubernetes, Terraform
/ OpenTofu, Linux
CI/CD & Quality: GitHub Actions, Playwright / Puppeteer, pytest, fault - injection testing
Security & Identity: JWT, RBAC and ABAC, Vault - encrypted secrets
PROFESSIONAL EXPERIENCE
Acme Security | Senior Software Engineer, Payments & Backend | London, UK (remote contract) | Dec 2022 - Oct
2023
- Owned the ingestion platform end to end, from queue consumers to the Kubernetes rollout.
Globex | Lead Delivery Engineer & Architect, Data Processing Platform | Barcelona, ESP
(remote contract) | Aug 2022 - Jan 2023
- Sole engineer on the synthetic data service: architecture, workers and evaluation pipelines.
Umbrella Corp | Senior Software Engineer & Security Architect, Retail - Ordering and Billing Platforms
| Rochester, New York, USA (remote) | Jan 2022 - Jul 2022
- Designed the billing decision service and its audit trail for two regulated clients.
Initech | Senior Full Stack Engineer | Lisbon, Portugal | May 2019 - Apr 2020
- Rebuilt the grading service and cut response times by half across the whole platform.
EDUCATION
M.Sc. Computer Engineering, Example University | 2009 - 2011
SELECTED PROJECTS & OPEN SOURCE (github.com/janedoe)
- harness: a Go agent runner with sandboxed execution and a local control plane for approvals.
`

func TestParseReadsAPipeSeparatedHeader(t *testing.T) {
	p := Parse(pipeLayoutCV)
	got := map[string]string{"name": p.Name, "title": p.CurrentTitle, "phone": p.Phone, "location": p.Location,
		"email": p.Email, "linkedin": p.LinkedIn, "github": p.GitHub}
	want := map[string]string{"name": "Jane Doe", "title": "Lead Full-Stack & AI Engineer", "phone": "+351 912 345 678",
		"location": "Portugal", "email": "jane.doe@example.com", "linkedin": "https://www.linkedin.com/in/jane-doe",
		"github": "https://github.com/janedoe"}
	for field, w := range want {
		if got[field] != w {
			t.Errorf("%s = %q, want %q", field, got[field], w)
		}
	}
	if p.YearsExperience != 11 {
		t.Errorf("years = %v, want 11 from \"11+ years building\"", p.YearsExperience)
	}
}

func TestParseReadsCategorisedSkillLines(t *testing.T) {
	p := Parse(pipeLayoutCV)
	have := map[string]bool{}
	for _, s := range p.Skills {
		have[s.Name] = true
	}
	for _, name := range []string{"Go", "Python", "TypeScript", "SQL", "Perl", "FastAPI", "Node.js", "Express", "NestJS",
		"gRPC", "Protobuf", "AWS", "EKS", "Lambda", "S3", "Docker", "Kubernetes", "Terraform", "OpenTofu", "Linux",
		"GitHub Actions", "Playwright", "Puppeteer", "pytest", "fault-injection testing", "JWT", "RBAC", "ABAC",
		"Vault-encrypted secrets"} {
		if !have[name] {
			t.Errorf("missing skill %q", name)
		}
	}
	for _, s := range p.Skills {
		switch {
		case strings.Contains(s.Name, "("), strings.Contains(s.Name, ":"):
			t.Errorf("skill %q still carries a note or a category label", s.Name)
		case s.Name == "Languages", s.Name == "Backend & APIs", strings.Contains(s.Name, "scalable"):
			t.Errorf("%q is a label or a description, not a skill", s.Name)
		}
	}
}

func TestParseReadsWrappedPipeRoleHeaders(t *testing.T) {
	p := Parse(pipeLayoutCV)
	type role struct{ company, title, start, end string }
	want := []role{
		{"Acme Security", "Senior Software Engineer, Payments & Backend", "Dec 2022", "Oct 2023"},
		{"Globex", "Lead Delivery Engineer & Architect, Data Processing Platform", "Aug 2022", "Jan 2023"},
		// Past 80 characters a title is cut at its first comma rather than lost.
		{"Umbrella Corp", "Senior Software Engineer & Security Architect", "Jan 2022", "Jul 2022"},
		{"Initech", "Senior Full Stack Engineer", "May 2019", "Apr 2020"},
	}
	if len(p.Experiences) != len(want) {
		t.Fatalf("got %d roles, want %d (the degree is not a role): %+v", len(p.Experiences), len(want), p.Experiences)
	}
	for i, w := range want {
		e := p.Experiences[i]
		if e.Company != w.company || e.Title != w.title || e.StartDate != w.start || e.EndDate != w.end {
			t.Errorf("role %d = %q at %q, %s to %s; want %q at %q, %s to %s", i, e.Title, e.Company, e.StartDate, e.EndDate, w.title, w.company, w.start, w.end)
		}
		// Each role has exactly its own bullet: the degree and the projects
		// section after the last role belong to no role.
		if !strings.HasPrefix(e.Description, "- ") || strings.Contains(e.Description, "\n") {
			t.Errorf("role %d should have exactly its one bullet, got %q", i, e.Description)
		}
	}
}
