param(
    [string]$PostgresBin = 'C:\Program Files\PostgreSQL\17\bin',
    [switch]$WithSessionTests,
    [switch]$WithApiTests,
    [switch]$WithHostedSetupTests,
    [string]$GoExecutable = 'go'
)
$ErrorActionPreference = 'Stop'

foreach ($command in @('initdb.exe', 'pg_ctl.exe', 'psql.exe')) {
    if (-not (Test-Path -LiteralPath (Join-Path $PostgresBin $command))) {
        throw "Missing PostgreSQL tool: $command in $PostgresBin"
    }
}
$taskTempBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$taskTempRoot = Join-Path $taskTempBase ('whatsmeow-ipo-schema-' + [guid]::NewGuid().ToString('N'))
$taskData = Join-Path $taskTempRoot 'data'
$started = $false
New-Item -ItemType Directory -Path $taskTempRoot | Out-Null

try {
    # Bind once to select an unused local port; pg_ctl fails safely if it is taken.
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $taskPort = $listener.LocalEndpoint.Port
    $listener.Stop()

    & (Join-Path $PostgresBin 'initdb.exe') -D $taskData -U schema_test --auth=trust --encoding=UTF8 --locale=C
    if ($LASTEXITCODE -ne 0) { throw 'Temporary database initialization failed' }
    & (Join-Path $PostgresBin 'pg_ctl.exe') -D $taskData -l (Join-Path $taskTempRoot 'postgres.log') -o "-h 127.0.0.1 -p $taskPort" -w -t 20 start
    if ($LASTEXITCODE -ne 0) { throw 'Temporary database startup failed' }
    $started = $true

    if ($WithHostedSetupTests) {
        # Exercise hosted setup as a non-superuser with managed-database privileges.
        & (Join-Path $PostgresBin 'psql.exe') -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p $taskPort -U schema_test -d postgres -c 'CREATE ROLE hosted_admin LOGIN CREATEROLE;'
        if ($LASTEXITCODE -ne 0) { throw 'Hosted fixture administrator creation failed' }
        & (Join-Path $PostgresBin 'psql.exe') -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p $taskPort -U schema_test -d postgres -c 'CREATE DATABASE hosted_setup OWNER hosted_admin;'
        if ($LASTEXITCODE -ne 0) { throw 'Hosted fixture creation failed' }
        & (Join-Path $PostgresBin 'psql.exe') -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p $taskPort -U hosted_admin -d hosted_setup -f (Join-Path $PSScriptRoot 'Setup-Supabase.psql')
        if ($LASTEXITCODE -ne 0) { throw 'Hosted schema setup failed' }
        & (Join-Path $PostgresBin 'psql.exe') -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p $taskPort -U hosted_admin -d hosted_setup -f (Join-Path $PSScriptRoot 'schema_test.sql')
        if ($LASTEXITCODE -ne 0) { throw 'Hosted schema behavior checks failed' }
        & (Join-Path $PostgresBin 'psql.exe') -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p $taskPort -U hosted_admin -d hosted_setup -f (Join-Path $PSScriptRoot 'Setup-Supabase.psql') 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { throw 'Hosted setup unexpectedly accepted an existing installation' }
    }

    foreach ($sqlFile in @('001_initial.sql', '002_api.sql', '003_whatsapp_accounts.sql', '004_contact_names.sql', '005_transaction_confirmations.sql', '006_passwordless_sessions.sql', '007_whatsapp_number_checks.sql', '008_signed_transaction_quantity.sql', 'schema_test.sql')) {
        $taskSqlPath = Join-Path $PSScriptRoot $sqlFile
        & (Join-Path $PostgresBin 'psql.exe') --no-psqlrc --set ON_ERROR_STOP=1 --host=127.0.0.1 --port $taskPort --username=schema_test --dbname=postgres --file $taskSqlPath
        if ($LASTEXITCODE -ne 0) { throw "Schema validation failed: $sqlFile" }
    }
    if ($WithApiTests) {
        $taskPreviousApiUrl = [Environment]::GetEnvironmentVariable('IPO_TEST_DATABASE_URL', 'Process')
        $env:IPO_TEST_DATABASE_URL = "postgresql://schema_test@127.0.0.1:$taskPort/postgres?sslmode=disable"
        Push-Location (Join-Path $PSScriptRoot '../backend')
        try {
            & $GoExecutable test -count=1 -v ./...
            if ($LASTEXITCODE -ne 0) { throw 'API integration checks failed' }
        } finally {
            Pop-Location
            [Environment]::SetEnvironmentVariable('IPO_TEST_DATABASE_URL', $taskPreviousApiUrl, 'Process')
        }
    }
    if ($WithSessionTests) {
        $taskPreviousTestUrl = [Environment]::GetEnvironmentVariable('WHATSAPP_TEST_DATABASE_URL', 'Process')
        $env:WHATSAPP_TEST_DATABASE_URL = "postgresql://schema_test@127.0.0.1:$taskPort/postgres?sslmode=disable"
        Push-Location (Join-Path $PSScriptRoot '../examples/login')
        try {
            & $GoExecutable test -run '^TestPostgresSessionPersistence$' -count=1 -v
            if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL session integration checks failed' }
        }
        finally {
            Pop-Location
            [Environment]::SetEnvironmentVariable('WHATSAPP_TEST_DATABASE_URL', $taskPreviousTestUrl, 'Process')
        }
    }
    Write-Output 'PostgreSQL schema and behavior checks passed.'
}
finally {
    if ($started -or (Test-Path -LiteralPath (Join-Path $taskData 'postmaster.pid'))) {
        & (Join-Path $PostgresBin 'pg_ctl.exe') -D $taskData -m fast -w -t 20 stop
        if ($LASTEXITCODE -ne 0) { throw "Temporary server could not stop; retained $taskTempRoot" }
    }
    # Use one shell and verify the exact resolved target before recursive cleanup.
    $taskResolvedRoot = [IO.Path]::GetFullPath($taskTempRoot)
    $taskParent = [IO.Path]::GetDirectoryName($taskResolvedRoot).TrimEnd('\')
    if ($taskParent -ne $taskTempBase.TrimEnd('\') -or [IO.Path]::GetFileName($taskResolvedRoot) -notmatch '^whatsmeow-ipo-schema-[a-f0-9]{32}$') {
        throw "Refusing cleanup outside the designated temporary directory: $taskResolvedRoot"
    }
    Remove-Item -LiteralPath $taskResolvedRoot -Recurse -Force
}
