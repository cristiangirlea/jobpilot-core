$ErrorActionPreference = "Stop"
$Api = if ($env:JOBPILOT_API_URL) { $env:JOBPILOT_API_URL } else { "http://localhost:8080/api" }
$email = "live-discovery-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())@example.test"
$registration = Invoke-RestMethod -Method POST -Uri "$Api/auth/register" -ContentType "application/json" -Body (@{email=$email;password="live-discovery-password"}|ConvertTo-Json)
$headers = @{Authorization="Bearer $($registration.token)"}
$profile = @{name="Discovery Test";email=$email;phone="";location="Europe";linkedin="";github="";portfolio="";current_title="Backend Engineer";desired_titles=@("golang");seniorities=@("mid","senior");years_experience=5;certifications=@();languages=@("English");salary_expectation=0;rate_expectation=0;currency="EUR";work_preference=@("remote");preferred_countries=@("Europe");preferred_timezones=@();work_authorization="EU";notice_period="30 days";industry_preferences=@();summary="";experiences=@();skills=@();projects=@()}
Invoke-RestMethod -Method PUT -Uri "$Api/profile" -Headers $headers -ContentType "application/json" -Body ($profile|ConvertTo-Json -Depth 8)|Out-Null
Invoke-RestMethod -Method PUT -Uri "$Api/discovery/settings" -Headers $headers -ContentType "application/json" -Body (@{enabled=$false;interval_minutes=360;auto_prepare=$true;min_score=0;max_jobs_per_run=2}|ConvertTo-Json)|Out-Null
$run = Invoke-RestMethod -Method POST -Uri "$Api/discovery/recommended" -Headers $headers -ContentType "application/json"
if ($run.status -ne "completed") { throw "Live discovery failed: $($run.error_message)" }
$applications = Invoke-RestMethod -Method GET -Uri "$Api/applications" -Headers $headers
if ($applications.Count -lt 1 -or $applications[0].status -ne "ready") { throw "Auto-prepare did not create a ready application" }
$jobs = Invoke-RestMethod -Method GET -Uri "$Api/jobs" -Headers $headers
if ($jobs.Count -lt 1 -or $jobs[0].job.source_provider -ne "remotive") { throw "Discovery source attribution is missing" }
Write-Host "Live discovery passed: seen=$($run.jobs_seen) new=$($run.jobs_added) prepared=$($run.applications_prepared)"
