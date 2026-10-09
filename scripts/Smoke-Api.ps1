$ErrorActionPreference = 'Stop'
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
$taskHealth = Invoke-RestMethod -Uri "$taskBase/health"
if ($taskHealth.status -ne 'ok') { throw 'Health check failed' }
$taskBody = @{
    phone_number = $taskSettings['BOOTSTRAP_ADMIN_PHONE']
    password = $taskSettings['BOOTSTRAP_ADMIN_PASSWORD']
} | ConvertTo-Json
$taskLogin = Invoke-RestMethod -Method Post -Uri "$taskBase/api/login" -ContentType 'application/json' -Body $taskBody
$taskHeaders = @{ Authorization = 'Bearer ' + $taskLogin.access_token }
try {
    $taskMe = Invoke-RestMethod -Uri "$taskBase/api/me" -Headers $taskHeaders
    if ($taskMe.businesses.Count -lt 1) { throw 'Initial admin has no active business' }
    $taskBusiness = $taskMe.businesses[0]
    $taskForms = Invoke-RestMethod -Uri "$taskBase/api/businesses/$($taskBusiness.id)/form-types" -Headers $taskHeaders
    if ($taskForms.items.Count -ne 7) { throw 'Form catalogue check failed' }
    Write-Output "Health, login, business access and form catalogue passed for $($taskBusiness.name)."
} finally {
    $null = Invoke-RestMethod -Method Post -Uri "$taskBase/api/logout" -Headers $taskHeaders
}
