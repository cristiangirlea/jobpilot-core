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
Send-Request PUT "/filters/$($filter.id)" $filter | Out-Null
$jobs = Send-Request GET "/jobs"
$updated = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if (-not $updated -or $updated.match.rejected) { throw "Existing job was not re-evaluated after the filter changed" }
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
Send-Request DELETE "/blacklist/$($exclusion.id)" | Out-Null
$jobs = Send-Request GET "/jobs"
$unblocked = $jobs | Where-Object { $_.job.id -eq $job.job.id }
if ($unblocked.match.rejected) { throw "Removing the company exclusion did not restore the existing match" }

$source = Send-Request POST "/discovery/sources" @{
    provider = "arbeitnow"; name = "Europe OR/NOT check"; identifier = ""; search_query = "Go, Rust"
    excluded_search_query = "PHP, WordPress"; active = $true
}
$sources = Send-Request GET "/discovery/sources"
$savedSource = $sources | Where-Object { $_.id -eq $source.id }
if ($savedSource.search_query -ne "Go, Rust" -or $savedSource.excluded_search_query -ne "PHP, WordPress") {
    throw "Discovery include/exclude rules were not persisted"
}

Write-Host "Regression passed: excluded-company jobs stay visible but all application actions are locked."
