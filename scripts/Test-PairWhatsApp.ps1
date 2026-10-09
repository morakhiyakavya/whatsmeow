# Offline self-check: no real .env, credentials or HTTP calls are used.
$ErrorActionPreference = 'Stop'
$taskFixtureAccount = [guid]::NewGuid()
$taskFixtureQR = Join-Path ([IO.Path]::GetTempPath()) "ipo-whatsapp-$taskFixtureAccount.png"
function Get-Content { param([string]$LiteralPath)
    if ([IO.Path]::GetFileName($LiteralPath) -ne '.env') { throw 'Unexpected fixture read' }
    return @('API_ADDR=127.0.0.1:8180','BOOTSTRAP_ADMIN_PHONE=+999000000001','BOOTSTRAP_ADMIN_PASSWORD=fixture-only-password')
}
function Start-Sleep { param([int]$Seconds) }
function Invoke-WebRequest { param([string]$Uri, $Headers, [string]$OutFile)
    if (-not $Uri.EndsWith('/qr') -or $Headers.Authorization -ne 'Bearer fixture-token' -or $OutFile -ne $taskFixtureQR) { throw 'Invalid QR request' }
    [IO.File]::WriteAllText($OutFile, 'synthetic QR')
    $script:taskQRReads++
}
function Invoke-RestMethod { param([string]$Uri, [string]$Method = 'GET', $Headers, [string]$ContentType, [string]$Body)
    if ($Uri.EndsWith('/api/login')) { return @{access_token='fixture-token'} }
    if ($Headers.Authorization -ne 'Bearer fixture-token') { throw 'Missing authentication' }
    if ($Uri.EndsWith('/api/logout')) { $script:taskLogouts++; return }
    if ($Uri.EndsWith('/api/me')) { return @{businesses=@(@{id='fixture-business'})} }
    if ($Uri.EndsWith('/pair')) {
        $taskInput = $Body | ConvertFrom-Json
        if ($taskInput.method -ne $script:taskMode -or ($script:taskMode -eq 'phone' -and $taskInput.phone_number -ne '+999000000002')) { throw 'Incorrect pairing input' }
        $script:taskStarts++; return
    }
    if ($Uri.EndsWith('/connect')) { $script:taskStarts++; return }
    if ($Uri.EndsWith('/connection')) {
        $script:taskPolls++
        if ($script:taskMode -eq 'failure') { return @{state='pairing_failed'} }
        if ($script:taskPolls -gt 1 -or $script:taskMode -eq 'saved') { return @{state='connected'} }
        if ($script:taskMode -eq 'phone') { return @{state='awaiting_phone';code='ABCD-EFGH'} }
        return @{state='awaiting_qr';code='synthetic-qr'}
    }
    return @{pairing_state=$(if ($script:taskMode -in @('saved','failure')) {'paired'} else {'unpaired'})}
}
foreach ($taskMode in @('qr','phone','saved','failure')) {
    $script:taskMode = $taskMode
    $script:taskLogouts = $script:taskStarts = $script:taskPolls = $script:taskQRReads = 0
    $taskError = $null
    try {
        $taskArgs = @{AccountId=$taskFixtureAccount}
        if ($taskMode -eq 'phone') { $taskArgs.Method='phone'; $taskArgs.WhatsAppPhone='+999000000002' }
        $taskOutput = @(. (Join-Path $PSScriptRoot 'Pair-WhatsApp.ps1') @taskArgs)
    } catch { $taskError = $_ }
    if ($taskMode -eq 'failure') {
        if (-not $taskError -or $taskError.ToString() -notmatch 'pairing_failed') { throw 'Failed state was not reported' }
    } elseif ($taskError -or ($taskOutput -join ' ') -notmatch 'WhatsApp connected') { throw "Pairing self-check failed: $taskMode, $taskError" }
    if ($taskStarts -ne 1 -or $taskLogouts -ne 1 -or (Test-Path -LiteralPath $taskFixtureQR)) { throw 'Pairing did not clean up its QR and sign-in session' }
    if (($taskMode -eq 'qr') -ne ($taskQRReads -eq 1)) { throw 'Incorrect QR download behavior' }
    if ($taskMode -eq 'phone' -and ($taskOutput -join ' ') -notmatch 'ABCD-EFGH') { throw 'Phone code was not displayed' }
}
Write-Output 'Offline QR/phone/saved-session/failure and cleanup checks passed.'
