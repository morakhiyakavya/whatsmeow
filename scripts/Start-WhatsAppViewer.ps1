param([switch]$NewSession, [string]$Session = '')
$ErrorActionPreference = 'Stop'
if ($NewSession -and $Session) { throw 'Use either -NewSession or -Session, not both' }
$taskRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$taskPreviousUrl = [Environment]::GetEnvironmentVariable('WHATSAPP_DATABASE_URL', 'Process')
$taskLine = Get-Content -LiteralPath (Join-Path $taskRoot '.env') | Where-Object { $_ -match '^WHATSAPP_DATABASE_URL=' } | Select-Object -First 1
if (-not $taskLine) { throw 'WHATSAPP_DATABASE_URL is missing from .env' }
$taskGoCommand = Get-Command go -ErrorAction SilentlyContinue
$taskGo = if ($taskGoCommand) { $taskGoCommand.Source } else { Join-Path $env:LOCALAPPDATA 'Programs/Go1.27.1/go/bin/go.exe' }
if (-not (Test-Path -LiteralPath $taskGo)) { throw 'Go 1.27.1 is required' }
$env:WHATSAPP_DATABASE_URL = ($taskLine -split '=',2)[1].Trim().Trim('"',"'")
Push-Location (Join-Path $taskRoot 'examples/login')
try {
    if ($NewSession) { & $taskGo run . -new-session }
    elseif ($Session) { & $taskGo run . -session $Session }
    else { & $taskGo run . }
    if ($LASTEXITCODE -ne 0) { throw 'WhatsApp viewer failed' }
} finally {
    Pop-Location
    [Environment]::SetEnvironmentVariable('WHATSAPP_DATABASE_URL', $taskPreviousUrl, 'Process')
}
