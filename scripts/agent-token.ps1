<#
.SYNOPSIS
Mint a JobPilot agent token from the terminal, for the MCP server or a script.

.DESCRIPTION
Agent tokens are normally created under Settings in the web app. This does
the same from a shell: it asks for your email and password, logs in to get a
short-lived session token, uses it to mint a long-lived agent token, and
prints that token once together with the command that registers the MCP
server with Claude Code.

The password is read as a secure string and sent only to the JobPilot API you
point this at. It is never printed, stored, or written to history. The agent
token is printed once and cannot be shown again, exactly as in the web app.

.EXAMPLE
pwsh -File scripts/agent-token.ps1 -Name "Claude Code on this laptop" -Write

.EXAMPLE
pwsh -File scripts/agent-token.ps1 -Api http://localhost:8080 -ExpiresInDays 90
#>
param(
    [string]$Name = "Claude Code on $env:COMPUTERNAME",
    # Grant write as well as read. Without it the token can look but not act.
    [switch]$Write,
    [ValidateRange(0, 365)][int]$ExpiresInDays = 0,
    [string]$Api = $(if ($env:JOBPILOT_API_URL) { $env:JOBPILOT_API_URL } else { "http://localhost:8080" }),
    # For automation only. Interactive use should let the prompt ask.
    [System.Management.Automation.PSCredential]$Credential
)
$ErrorActionPreference = "Stop"
$Api = $Api.TrimEnd("/")
if ($Api.EndsWith("/api")) { $Api = $Api.Substring(0, $Api.Length - 4) }

if (-not $Credential) {
    $email = Read-Host "JobPilot email"
    $secret = Read-Host "Password" -AsSecureString
    $Credential = New-Object System.Management.Automation.PSCredential($email, $secret)
}

function Invoke-JobPilot {
    param([string]$Method, [string]$Path, $Body, [string]$Token)
    $arguments = @{ Method = $Method; Uri = "$Api/api$Path"; ContentType = "application/json" }
    if ($Token) { $arguments.Headers = @{ Authorization = "Bearer $Token" } }
    if ($null -ne $Body) { $arguments.Body = ($Body | ConvertTo-Json -Depth 5) }
    try { Invoke-RestMethod @arguments }
    catch {
        $detail = $_.ErrorDetails.Message
        if ($detail) { try { $detail = ($detail | ConvertFrom-Json).error } catch {} }
        if (-not $detail) { $detail = $_.Exception.Message }
        throw "JobPilot API ($Method $Path): $detail"
    }
}

# The session token lives only in this variable for the next two calls.
$login = Invoke-JobPilot POST "/auth/login" @{
    email    = $Credential.UserName
    password = $Credential.GetNetworkCredential().Password
}
$scopes = if ($Write) { @("read", "write") } else { @("read") }
$created = Invoke-JobPilot POST "/tokens" @{ name = $Name; scopes = $scopes; expires_in_days = $ExpiresInDays } $login.token

$binary = Join-Path (Split-Path $PSScriptRoot -Parent) "apps/mcp/jobpilot-mcp"
if ($IsWindows -or $env:OS -eq "Windows_NT") { $binary += ".exe" }
$access = if ($Write) { "read and write" } else { "read only" }
$expiry = if ($ExpiresInDays -gt 0) { "expires in $ExpiresInDays days" } else { "never expires; revoke it under Settings when done" }

Write-Host ""
Write-Host "Agent token '$Name' ($access, $expiry). It will not be shown again:" -ForegroundColor Yellow
Write-Output $created.token
Write-Host ""
Write-Host "Register the MCP server with Claude Code (build it first with 'make mcp' if needed):"
Write-Host "  claude mcp add jobpilot -e JOBPILOT_TOKEN=$($created.token) -e JOBPILOT_API_URL=$Api -- $binary"
