$ErrorActionPreference = "Stop"
$Api = if ($env:JOBPILOT_API_URL) { $env:JOBPILOT_API_URL } else { "http://localhost:8080/api" }
$email = "seniority-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())@example.test"

function Send-Request {
    param([string]$Method, [string]$Path, $Body = $null)
    $arguments = @{ Method = $Method; Uri = "$Api$Path"; ContentType = "application/json" }
    if ($script:Headers) { $arguments.Headers = $script:Headers }
    if ($null -ne $Body) { $arguments.Body = ($Body | ConvertTo-Json -Depth 10) }
    Invoke-RestMethod @arguments
}

function Assert-Blocked {
    param([scriptblock]$Action, [string]$Label)
    $conflict = $false
    try { & $Action | Out-Null } catch {
        if ([int]$_.Exception.Response.StatusCode -eq 409) { $conflict = $true } else { throw }
    }
    if (-not $conflict) { throw "$Label was not blocked" }
}

$registration = Send-Request POST "/auth/register" @{ email = $email; password = "seniority-test-password" }
$script:Headers = @{ Authorization = "Bearer $($registration.token)" }
$profile = @{
    name = "Seniority Test"; email = $email; phone = ""; location = "Europe"; linkedin = ""; github = ""; portfolio = ""
    current_title = "Backend Engineer"; desired_titles = @("Backend Engineer"); seniorities = @("mid", "senior")
    years_experience = 5; certifications = @(); languages = @("English"); salary_expectation = 0; rate_expectation = 0
    currency = "EUR"; work_preference = @("remote"); preferred_countries = @("Europe"); preferred_timezones = @()
    work_authorization = "EU"; notice_period = "30 days"; industry_preferences = @(); summary = ""
    experiences = @(); skills = @(); projects = @()
}
Send-Request PUT "/profile" $profile | Out-Null
$savedProfile = Send-Request GET "/profile"
if ($savedProfile.seniorities.Count -ne 2 -or $savedProfile.seniorities -notcontains "mid" -or $savedProfile.seniorities -notcontains "senior") {
    throw "Multiple seniority values were not persisted"
}

$filter = Send-Request POST "/filters" @{
    name = "Technology regression"; active = $true; included_titles = @("Backend Engineer"); excluded_title_words = @()
    locations = @("Europe"); remote_types = @("remote"); min_salary = 0; currency = "EUR"; employment_types = @()
    excluded_industries = @(); required_skills = @(); included_technologies = @("Go"); excluded_technologies = @(); max_age_days = 0
}
$job = Send-Request POST "/jobs" @{
    title = "Mid Backend Engineer"; company = "Regression Labs"; company_domain = ""; location = "Europe"; remote_type = "remote"
    salary_min = 0; salary_max = 0; currency = "EUR"; employment_type = "full-time"; industry = "Software"
    description = "Build reliable services with Rust."; apply_url = "https://example.test/seniority-regression"
}
if (-not $job.match.rejected -or $job.match.breakdown.seniority_match -ne 15) {
    throw "Initial seniority or technology evaluation failed"
}

$filter.included_technologies = @("Rust")
$filter.required_skills = @("Rust")
Send-Request PUT "/filters/$($filter.id)" $filter | Out-Null
$jobs = Send-Request GET "/jobs"
$updated = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if (-not $updated -or $updated.match.rejected) { throw "Existing job was not re-evaluated after the filter changed" }
if ($updated.match.missing_skills -notcontains "Rust") { throw "Expected Rust to be reported as a missing skill before it is added to the profile" }

$skill = Send-Request POST "/skills" @{ name = "Rust"; level = "intermediate"; years = 2 }
$jobs = Send-Request GET "/jobs"
$skillMatched = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if ($skillMatched.match.matched_skills -notcontains "Rust" -or $skillMatched.match.breakdown.skill_match -ne 30) {
    throw "Existing job was not re-evaluated after adding a matching skill"
}
Send-Request DELETE "/skills/$($skill.id)" | Out-Null
$jobs = Send-Request GET "/jobs"
$skillRemoved = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if ($skillRemoved.match.matched_skills -contains "Rust" -or $skillRemoved.match.missing_skills -notcontains "Rust") {
    throw "Existing job was not re-evaluated after removing a matching skill"
}
$application = Send-Request POST "/applications" @{ job_id = $job.job.id }

$exclusion = Send-Request POST "/blacklist" @{ company_name = "Regression Labs LLC"; company_domain = "regression.example"; reason = "Regression check" }
$jobs = Send-Request GET "/jobs"
$blocked = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if (-not $blocked.match.rejected -or ($blocked.match.rejection_reasons -join " ") -notmatch "company blacklist") {
    throw "Company exclusion did not re-evaluate and reject the existing job"
}
Assert-Blocked { Send-Request POST "/applications" @{ job_id = $job.job.id } } "Application creation"
Assert-Blocked { Send-Request POST "/jobs/$($job.job.id)/generate" } "Document generation"
Assert-Blocked { Send-Request POST "/jobs/$($job.job.id)/outbound" } "Apply URL"
Assert-Blocked { Send-Request PATCH "/applications/$($application.id)" @{ status = "applied"; notes = "Should be blocked" } } "Applied status"
$applicationDetail = Send-Request GET "/applications/$($application.id)"
if (-not $applicationDetail.blocked) { throw "Application detail did not expose its locked state" }
if ($applicationDetail.application.job.apply_url) { throw "A blocked application must not carry an apply URL" }
$applicationList = Send-Request GET "/applications"
$listed = $applicationList | Where-Object { $_.id -eq $application.id }
if ($listed.job.apply_url) { throw "A blocked application must not carry an apply URL in the listing" }
Send-Request DELETE "/blacklist/$($exclusion.id)" | Out-Null
$jobs = Send-Request GET "/jobs"
$unblocked = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if ($unblocked.match.rejected) { throw "Removing the company exclusion did not restore the existing match" }

$experience = Send-Request POST "/experiences" @{
    company = "Regression Labs"; company_domain = ""; title = "Backend Engineer"; start_date = "2021"; end_date = ""; current = $false
    description = "Regression experience entry."
}
$jobs = Send-Request GET "/jobs"
$experienceBlocked = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if (-not $experienceBlocked.match.rejected -or ($experienceBlocked.match.rejection_reasons -join " ") -notmatch "previous employer") {
    throw "Adding experience did not re-evaluate and reject the existing job"
}
Send-Request DELETE "/experiences/$($experience.id)" | Out-Null
$jobs = Send-Request GET "/jobs"
$experienceRemoved = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if ($experienceRemoved.match.rejected) { throw "Removing experience did not restore the existing match" }

$source = Send-Request POST "/discovery/sources" @{
    provider = "arbeitnow"; name = "Europe OR/NOT check"; identifier = ""; search_query = "Go, Rust"
    excluded_search_query = "PHP, WordPress"; active = $true
}
$sources = Send-Request GET "/discovery/sources"
$savedSource = $sources | Where-Object { $_.id -eq $source.id }
if ($savedSource.search_query -ne "Go, Rust" -or $savedSource.excluded_search_query -ne "PHP, WordPress") {
    throw "Discovery include/exclude rules were not persisted"
}
$source.active = $false
$source.search_query = "Go"
$source.excluded_search_query = "PHP"
$updatedSource = Send-Request PUT "/discovery/sources/$($source.id)" $source
if ($updatedSource.active -or $updatedSource.search_query -ne "Go" -or $updatedSource.excluded_search_query -ne "PHP") {
    throw "Discovery source edits and pause state were not persisted"
}
$suggestions = Send-Request GET "/discovery/suggestions"
if (-not ($suggestions | Where-Object { $_.provider -eq "greenhouse" -and $_.identifier -eq "grafanalabs" })) {
    throw "Curated discovery suggestions did not include expected employer-direct boards"
}
$detected = Send-Request POST "/discovery/detect" @{ url = "https://job-boards.greenhouse.io/grafanalabs" }
if ($detected.provider -ne "greenhouse" -or $detected.identifier -ne "grafanalabs" -or -not $detected.supported) {
    throw "Discovery URL detection failed for a Greenhouse board"
}

Write-Host "Regression passed: excluded-company jobs stay visible, all application actions are locked, and apply URLs are withheld."
