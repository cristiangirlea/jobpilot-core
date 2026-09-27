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

function Assert-Status {
    param([scriptblock]$Action, [int]$Expected, [string]$Label)
    $matched = $false
    try { & $Action | Out-Null } catch {
        if ([int]$_.Exception.Response.StatusCode -eq $Expected) { $matched = $true } else { throw }
    }
    if (-not $matched) { throw "$Label was not refused with $Expected" }
}
function Assert-NotFound { param([scriptblock]$Action, [string]$Label) Assert-Status $Action 404 $Label }
function Assert-Forbidden { param([scriptblock]$Action, [string]$Label) Assert-Status $Action 403 $Label }
function Assert-Unauthorized { param([scriptblock]$Action, [string]$Label) Assert-Status $Action 401 $Label }

function Assert-Conflict {
    param([scriptblock]$Action, [string]$Label)
    $conflict = $false
    try { & $Action | Out-Null } catch {
        if ([int]$_.Exception.Response.StatusCode -eq 409) { $conflict = $true } else { throw }
    }
    if (-not $conflict) { throw "$Label was not blocked" }
}

$registration = Send-JobPilotRequest POST "/auth/register" @{ email = $Email; password = "smoke-test-password" }
$script:Headers = @{ Authorization = "Bearer $($registration.token)" }

$profile = @{
    name = "Candidate Smoke"; given_name = "Smoke"; family_name = "Candidate"
    email = $Email; phone = "+400000000"; location = "Bucharest, Romania"
    linkedin = ""; github = ""; portfolio = ""; current_title = "Senior Backend Engineer"
    desired_titles = @("Backend Engineer"); seniorities = @("mid", "senior"); years_experience = 7
    certifications = @(); languages = @("English"); salary_expectation = 90000; rate_expectation = 0; currency = "EUR"
    work_preference = @("remote"); preferred_countries = @("Romania"); preferred_timezones = @("EET")
    work_authorization = "EU citizen"; notice_period = "30 days"; industry_preferences = @("Software"); summary = ""
    experiences = @(); skills = @(); projects = @()
}
Send-JobPilotRequest PUT "/profile" $profile | Out-Null
# Stored surname first, as some candidates write it: forms take the stated
# given and family names, never a split of this one.
$stored = Send-JobPilotRequest GET "/profile"
if ($stored.given_name -ne "Smoke" -or $stored.family_name -ne "Candidate") { throw "The profile should keep the given and family names it was saved with" }
Send-JobPilotRequest POST "/experiences" @{ company = "CurrentCo"; company_domain = "current.example"; title = "Senior Backend Engineer"; start_date = "2022"; end_date = ""; current = $true; description = "Built backend services." } | Out-Null
Send-JobPilotRequest POST "/skills" @{ name = "Go"; level = "advanced"; years = 5 } | Out-Null
Send-JobPilotRequest POST "/skills" @{ name = "PostgreSQL"; level = "advanced"; years = 5 } | Out-Null
$suggested = @(Send-JobPilotRequest GET "/skills/suggestions" | ForEach-Object { $_.name })
if ($suggested -contains "Go" -or $suggested -contains "PostgreSQL") { throw "Skill suggestions should leave out skills the profile already has" }
if ($suggested -cnotcontains "Kubernetes") { throw "Skill suggestions should offer the known vocabulary, written as skills" }
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
if (-not $accepted.job.slug) { throw "An imported job should be named with a slug" }
$bySlug = Send-JobPilotRequest GET "/jobs/$($accepted.job.slug)"
if ($bySlug.job.id -ne $accepted.job.id) { throw "A job should be found by its slug" }
$byID = Send-JobPilotRequest GET "/jobs/$($accepted.job.id)"
if ($byID.job.slug -ne $accepted.job.slug) { throw "A job should still be found by its id" }
# Two postings whose links end the same must still get distinct names.
$twinBody = $acceptedBody.Clone(); $twinBody.apply_url = "https://example.test/jobs/shared-ending"
$twinA = Send-JobPilotRequest POST "/jobs" $twinBody
$twinBody.title = "Staff Backend Engineer"
$twinB = Send-JobPilotRequest POST "/jobs" $twinBody
if (-not $twinA.job.slug -or $twinA.job.slug -eq $twinB.job.slug) { throw "Jobs with the same link ending should get distinct slugs" }
if ((Send-JobPilotRequest GET "/jobs/$($twinB.job.slug)").job.id -ne $twinB.job.id) { throw "A suffixed slug should find its own job" }

$documents = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/generate"
if (-not $documents.cover_letter.Contains("Fresh Systems")) { throw "Template generation smoke check failed" }
$application = Send-JobPilotRequest POST "/applications" @{ job_id = $accepted.job.id }
$updated = Send-JobPilotRequest PATCH "/applications/$($application.id)" @{ status = "applied"; notes = "Smoke test" }
if ($updated.status -ne "applied") { throw "Application status smoke check failed" }

# A status-only PATCH must keep the stored notes, so a client that moves an
# application forward cannot silently erase what the user wrote.
$statusOnly = Send-JobPilotRequest PATCH "/applications/$($application.id)" @{ status = "interview" }
if ($statusOnly.status -ne "interview" -or $statusOnly.notes -ne "Smoke test") {
    throw "A status-only update must preserve notes, got '$($statusOnly.notes)'"
}
$cleared = Send-JobPilotRequest PATCH "/applications/$($application.id)" @{ status = "applied"; notes = "" }
if ($cleared.notes -ne "") { throw "An explicit empty notes value must clear the notes" }

# Rejected jobs are visible for explanation but carry no apply URL.
$allJobs = Send-JobPilotRequest GET "/jobs"
$rejectedJob = $allJobs | Where-Object { $_.job.id -eq $rejected.job.id }
if (-not $rejectedJob.match.rejected) { throw "The excluded-employer job should still be rejected" }
if ($rejectedJob.job.apply_url) { throw "A rejected job must not carry an apply URL" }
$acceptedJob = $allJobs | Where-Object { $_.job.id -eq $accepted.job.id }
if ($acceptedJob.job.apply_url -ne "https://example.test/apply") { throw "An accepted job must keep its apply URL" }
$outbound = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/outbound"
if ($outbound.url -ne "https://example.test/apply") { throw "Outbound attribution smoke check failed" }
Send-JobPilotRequest PUT "/discovery/settings" @{ enabled = $false; interval_minutes = 360; auto_prepare = $true; min_score = 75; max_jobs_per_run = 50 } | Out-Null
$source = Send-JobPilotRequest POST "/discovery/sources" @{ provider = "remotive"; name = "Smoke remote feed"; identifier = ""; search_query = "go backend engineer"; active = $true }
if (-not $source.id) { throw "Discovery source smoke check failed" }
$sources = Send-JobPilotRequest GET "/discovery/sources"
if ($sources.Count -lt 1) { throw "Discovery source listing smoke check failed" }
$dashboard = Send-JobPilotRequest GET "/dashboard"
if ($dashboard.jobs_imported -lt 2 -or $dashboard.applications_applied -lt 1) { throw "Dashboard smoke check failed" }

# The apply engine must work with no AI provider configured, and the outbox
# must refuse to release an application that still has blockers.
$ai = Send-JobPilotRequest GET "/ai/status"
if ($ai.enabled -ne $false) { throw "AI adapter should be disabled by default" }
$entry = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/compose"
if ($entry.status -ne "needs_review") { throw "Compose should need review for an unreadable employer form, got $($entry.status)" }
if ($entry.blockers.Count -lt 1) { throw "Compose should record at least one blocker" }
if (-not $entry.cover_letter) { throw "Compose should produce a cover letter without an AI provider" }
$outboxList = Send-JobPilotRequest GET "/outbox"
if (@($outboxList | Where-Object { $_.id -eq $entry.id }).Count -lt 1) { throw "Composed entry is missing from the outbox" }
Assert-Conflict { Send-JobPilotRequest POST "/outbox/$($entry.id)/approve" } "Outbox approval with blockers"

# Rebuilding an entry must succeed rather than fail on the guard that keeps a
# sent application immutable, and deleting something that is not there is a
# 404 rather than a silent success.
$recomposed = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/compose"
if ($recomposed.id -ne $entry.id) { throw "Recomposing should replace the same entry" }
if ($recomposed.notices -contains "You had approved this application. Rebuilding it withdrew that approval, so it needs approving again.") {
    throw "An entry that was never approved should not report a withdrawn approval"
}

# A tailored CV starts as a draft that blocks the application. Approving it
# clears that blocker on the queued entry at once, preparing the application
# again keeps the approved CV, and withdrawing the approval blocks it again.
$cvBlocker = "A tailored CV was prepared for this role and needs your approval before it can be sent."
if ($recomposed.blockers -notcontains $cvBlocker) { throw "A freshly tailored CV should block the application until approved" }
Send-JobPilotRequest POST "/cv/$($recomposed.cv_document_id)/approve" @{ approved = $true } | Out-Null
$afterApproval = Send-JobPilotRequest GET "/outbox/$($recomposed.id)"
if ($afterApproval.blockers -contains $cvBlocker) { throw "Approving the CV should clear its blocker on the queued application" }
$keptCV = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/compose"
if ($keptCV.cv_document_id -ne $recomposed.cv_document_id) { throw "Preparing again should keep the approved tailored CV" }
if ($keptCV.cv_decision -ne "reused") { throw "A kept CV should be reported as reused, got $($keptCV.cv_decision)" }
if ($keptCV.blockers -contains $cvBlocker) { throw "An approved CV should no longer block the application" }
Send-JobPilotRequest POST "/cv/$($keptCV.cv_document_id)/approve" @{ approved = $false } | Out-Null
$afterWithdrawal = Send-JobPilotRequest GET "/outbox/$($keptCV.id)"
if ($afterWithdrawal.blockers -notcontains $cvBlocker -or $afterWithdrawal.status -ne "needs_review") {
    throw "Withdrawing the CV's approval should block the application again"
}
Assert-NotFound { Send-JobPilotRequest DELETE "/answers/00000000-0000-0000-0000-000000000000" } "Deleting an unknown answer"
Assert-NotFound { Send-JobPilotRequest DELETE "/cv/00000000-0000-0000-0000-000000000000" } "Deleting an unknown CV"

# Agent tokens: read can look but not touch, write can act, and neither can
# manage tokens. Revocation takes effect on the next request.
$readToken = Send-JobPilotRequest POST "/tokens" @{ name = "Smoke read"; scopes = @("read"); expires_in_days = 0 }
if (-not $readToken.token.StartsWith("jpat_")) { throw "An agent token should carry the jpat_ prefix" }
if ($readToken.agent_token.token_prefix.Length -ne 8) { throw "The listing prefix should be 8 characters" }
$writeToken = Send-JobPilotRequest POST "/tokens" @{ name = "Smoke write"; scopes = @("write"); expires_in_days = 30 }
if ($writeToken.agent_token.scopes -notcontains "read") { throw "A write token must also carry read" }

$sessionHeaders = $script:Headers
$script:Headers = @{ Authorization = "Bearer $($readToken.token)" }
Send-JobPilotRequest GET "/profile" | Out-Null
Assert-Forbidden { Send-JobPilotRequest POST "/applications" @{ job_id = $accepted.job.id } } "A read token writing"
Assert-Forbidden { Send-JobPilotRequest GET "/tokens" } "An agent token listing tokens"
# validate and detect change nothing, so read may reach them; this pins the
# two deliberate exceptions to the write-scope rule.
$readValidation = Send-JobPilotRequest POST "/discovery/validate" @{ provider = "remotive"; name = "Scope check"; identifier = ""; search_query = "go backend engineer"; active = $true }
if ($null -eq $readValidation.count) { throw "A read token should be able to validate a source" }

$script:Headers = @{ Authorization = "Bearer $($writeToken.token)" }
$agentDocs = Send-JobPilotRequest POST "/jobs/$($accepted.job.id)/generate"
if (-not $agentDocs.cover_letter) { throw "A write token should be able to generate documents" }
Assert-Forbidden { Send-JobPilotRequest POST "/tokens" @{ name = "Nope"; scopes = @("read") } } "An agent token minting a token"

$script:Headers = $sessionHeaders
$tokens = Send-JobPilotRequest GET "/tokens"
if (@($tokens).Count -lt 2) { throw "Both tokens should be listed" }
if ($tokens | Where-Object { $_.PSObject.Properties.Name -contains "token" }) { throw "The listing must never return token values" }
Send-JobPilotRequest DELETE "/tokens/$($readToken.agent_token.id)" | Out-Null
$script:Headers = @{ Authorization = "Bearer $($readToken.token)" }
Assert-Unauthorized { Send-JobPilotRequest GET "/profile" } "A revoked token"
$script:Headers = $sessionHeaders

# Catalog jobs are global: another account evaluates and reuses the same job ID.
$second = Send-JobPilotRequest POST "/auth/register" @{ email = "second-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())@example.test"; password = "smoke-test-password" }
$script:Headers = @{ Authorization = "Bearer $($second.token)" }
$secondMatch = Send-JobPilotRequest POST "/jobs" $acceptedBody
if ($secondMatch.job.id -ne $accepted.job.id) { throw "Shared catalog deduplication smoke check failed" }

Write-Host "JobPilot smoke test passed: discovery config, shared catalog, scoring, generation, outbound attribution, application tracking, notes preservation, the outbox approval gate, and scoped agent tokens."
