$ErrorActionPreference = "Stop"
$Api = if ($env:JOBPILOT_API_URL) { $env:JOBPILOT_API_URL } else { "http://localhost:8080/api" }
$Email = "smoke-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())@example.test"

function Send-JobPilotRequest {
    param([string]$Method, [string]$Path, $Body = $null)
    $arguments = @{ Method = $Method; Uri = "$Api$Path"; ContentType = "application/json" }
    if ($script:Headers) { $arguments.Headers = $script:Headers }
    if ($null -ne $Body) { $arguments.Body = ($Body | ConvertTo-Json -Depth 10) }
    Invoke-RestMethod @arguments
}

$registration = Send-JobPilotRequest POST "/auth/register" @{ email = $Email; password = "smoke-test-password" }
$script:Headers = @{ Authorization = "Bearer $($registration.token)" }

$profile = @{
    name = "Smoke Candidate"; email = $Email; phone = "+400000000"; location = "Bucharest, Romania"
    linkedin = ""; github = ""; portfolio = ""; current_title = "Senior Backend Engineer"
    desired_titles = @("Backend Engineer"); seniorities = @("mid", "senior"); years_experience = 7
    certifications = @(); languages = @("English"); salary_expectation = 90000; rate_expectation = 0; currency = "EUR"
    work_preference = @("remote"); preferred_countries = @("Romania"); preferred_timezones = @("EET")
    work_authorization = "EU citizen"; notice_period = "30 days"; industry_preferences = @("Software"); summary = ""
    experiences = @(); skills = @(); projects = @()
}
Send-JobPilotRequest PUT "/profile" $profile | Out-Null
Send-JobPilotRequest POST "/experiences" @{ company = "CurrentCo"; company_domain = "current.example"; title = "Senior Backend Engineer"; start_date = "2022"; end_date = ""; current = $true; description = "Built backend services." } | Out-Null
Send-JobPilotRequest POST "/skills" @{ name = "Go"; level = "advanced"; years = 5 } | Out-Null
Send-JobPilotRequest POST "/skills" @{ name = "PostgreSQL"; level = "advanced"; years = 5 } | Out-Null
Send-JobPilotRequest POST "/filters" @{
    name = "Smoke filter"; active = $true; included_titles = @("Backend Engineer"); excluded_title_words = @("unpaid")
    locations = @("Romania"); remote_types = @("remote"); min_salary = 90000; currency = "EUR"
    employment_types = @("full-time"); excluded_industries = @("Gambling"); required_skills = @("Go", "PostgreSQL")
    included_technologies = @("Go"); excluded_technologies = @("PHP"); max_age_days = 30
} | Out-Null

$rejected = Send-JobPilotRequest POST "/jobs" @{
    title = "Backend Engineer"; company = "CurrentCo LLC"; company_domain = ""; location = "Romania"; remote_type = "remote"
    salary_min = 90000; salary_max = 110000; currency = "EUR"; employment_type = "full-time"; industry = "Software"
    description = "Build services with Go and PostgreSQL."; apply_url = "https://example.test/apply"
}
if (-not $rejected.match.rejected) { throw "Employer exclusion smoke check failed" }

$acceptedBody = @{
    title = "Senior Backend Engineer"; company = "Fresh Systems GmbH"; company_domain = "fresh.example"; location = "Bucharest, Romania"; remote_type = "remote"
    salary_min = 95000; salary_max = 110000; currency = "EUR"; employment_type = "full-time"; industry = "Software"
    description = "Build reliable backend services with Go and PostgreSQL."; apply_url = "https://example.test/apply"
    posted_at = [DateTime]::UtcNow.ToString("o")
}
$accepted = Send-JobPilotRequest POST "/jobs" $acceptedBody
if ($accepted.match.rejected -or $accepted.match.score -lt 70) { throw "Accepted matching smoke check failed: score $($accepted.match.score)" }

$documents = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/generate"
if (-not $documents.cover_letter.Contains("Fresh Systems")) { throw "Template generation smoke check failed" }
$application = Send-JobPilotRequest POST "/applications" @{ job_id = $accepted.job.id }
$updated = Send-JobPilotRequest PATCH "/applications/$($application.id)" @{ status = "applied"; notes = "Smoke test" }
if ($updated.status -ne "applied") { throw "Application status smoke check failed" }
$outbound = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/outbound"
if ($outbound.url -ne "https://example.test/apply") { throw "Outbound attribution smoke check failed" }
Send-JobPilotRequest PUT "/discovery/settings" @{ enabled = $false; interval_minutes = 360; auto_prepare = $true; min_score = 75; max_jobs_per_run = 50 } | Out-Null
$source = Send-JobPilotRequest POST "/discovery/sources" @{ provider = "remotive"; name = "Smoke remote feed"; identifier = ""; search_query = "go backend engineer"; active = $true }
if (-not $source.id) { throw "Discovery source smoke check failed" }
$sources = Send-JobPilotRequest GET "/discovery/sources"
if ($sources.Count -lt 1) { throw "Discovery source listing smoke check failed" }
$dashboard = Send-JobPilotRequest GET "/dashboard"
if ($dashboard.jobs_imported -lt 2 -or $dashboard.applications_applied -lt 1) { throw "Dashboard smoke check failed" }

# Catalog jobs are global: another account evaluates and reuses the same job ID.
$second = Send-JobPilotRequest POST "/auth/register" @{ email = "second-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())@example.test"; password = "smoke-test-password" }
$script:Headers = @{ Authorization = "Bearer $($second.token)" }
$secondMatch = Send-JobPilotRequest POST "/jobs" $acceptedBody
if ($secondMatch.job.id -ne $accepted.job.id) { throw "Shared catalog deduplication smoke check failed" }

Write-Host "JobPilot smoke test passed: discovery config, shared catalog, scoring, generation, outbound attribution, application tracking."
