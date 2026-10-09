param(
    [Parameter(Mandatory)][guid]$AccountId,
    [ValidateSet('qr', 'phone')][string]$Method = 'qr',
    [string]$WhatsAppPhone = ''
)
$ErrorActionPreference = 'Stop'
if ($Method -eq 'phone' -and $WhatsAppPhone -notmatch '^\+[1-9][0-9]{6,14}$') {
    throw 'Phone pairing requires -WhatsAppPhone in international format, including +'
}
$taskRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$taskSettings = @{}
Get-Content -LiteralPath (Join-Path $taskRoot '.env') | ForEach-Object {
    $taskLine = $_.Trim()
    if ($taskLine -and -not $taskLine.StartsWith('#')) {
        $taskPair = $taskLine -split '=',2
        if ($taskPair.Length -eq 2) { $taskSettings[$taskPair[0].Trim()] = $taskPair[1].Trim().Trim('"',"'") }
    }
}
$taskBase = 'http://' + $taskSettings['API_ADDR']
$taskLoginBody = @{
    phone_number = $taskSettings['BOOTSTRAP_ADMIN_PHONE']
    password = $taskSettings['BOOTSTRAP_ADMIN_PASSWORD']
} | ConvertTo-Json
$taskLogin = Invoke-RestMethod -Method Post -Uri "$taskBase/api/login" -ContentType 'application/json' -Body $taskLoginBody
$taskHeaders = @{ Authorization = 'Bearer ' + $taskLogin.access_token }
$taskQRPath = Join-Path ([IO.Path]::GetTempPath()) "ipo-whatsapp-$AccountId.png"
try {
    $taskMe = Invoke-RestMethod -Uri "$taskBase/api/me" -Headers $taskHeaders
    # Look only in authorized businesses; the server enforces the registering user.
    $taskAccount = $null
    foreach ($taskBusiness in $taskMe.businesses) {
        $taskPath = "$taskBase/api/businesses/$($taskBusiness.id)/whatsapp-accounts/$AccountId"
        try { $taskAccount = Invoke-RestMethod -Uri $taskPath -Headers $taskHeaders } catch {
            if ([int]$_.Exception.Response.StatusCode -ne 404) { throw }
        }
        if ($taskAccount) { break }
    }
    if (-not $taskAccount) { throw 'Account not found in an authorized business' }
    if ($taskAccount.pairing_state -eq 'paired') {
        $null = Invoke-RestMethod -Method Post -Uri "$taskPath/connect" -Headers $taskHeaders
    } else {
        $taskPairBody = @{ method = $Method }
        if ($Method -eq 'phone') { $taskPairBody.phone_number = $WhatsAppPhone }
        $null = Invoke-RestMethod -Method Post -Uri "$taskPath/pair" -Headers $taskHeaders -ContentType 'application/json' -Body ($taskPairBody | ConvertTo-Json)
    }
    $taskDeadline = [DateTime]::UtcNow.AddMinutes(3)
    $taskPreviousCode = ''
    while ([DateTime]::UtcNow -lt $taskDeadline) {
        $taskState = Invoke-RestMethod -Uri "$taskPath/connection" -Headers $taskHeaders
        if ($taskState.state -eq 'connected') { Write-Output "WhatsApp connected for account $AccountId."; return }
        if ($taskState.state -in @('awaiting_qr','awaiting_phone') -and $taskState.code -and $taskState.code -ne $taskPreviousCode) {
            $taskPreviousCode = $taskState.code
            if ($taskState.state -eq 'awaiting_qr') {
                Invoke-WebRequest -Uri "$taskPath/qr" -Headers $taskHeaders -OutFile $taskQRPath
                Write-Output "Open $taskQRPath and scan in WhatsApp > Linked devices. The image refreshes here."
            } else {
                Write-Output "Enter $($taskState.code) in WhatsApp > Linked devices > Link with phone number."
            }
        } elseif ($taskState.state -notin @('connecting','awaiting_qr','awaiting_phone','finishing_pairing','reconnecting')) {
            throw "WhatsApp connection stopped: $($taskState.state). Retry after resolving this state."
        }
        Start-Sleep -Seconds 2
    }
    throw 'Pairing wait timed out; inspect the connection state before retrying'
} finally {
    try {
        if (Test-Path -LiteralPath $taskQRPath) { Remove-Item -LiteralPath $taskQRPath -Force }
    } finally {
        $null = Invoke-RestMethod -Method Post -Uri "$taskBase/api/logout" -Headers $taskHeaders
    }
}
