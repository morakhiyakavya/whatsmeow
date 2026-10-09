param(
    [string]$PostgresBin = 'C:\Program Files\PostgreSQL\17\bin',
    [string]$AdminHost = 'localhost',
    [string]$AdminUser = 'postgres'
)
$ErrorActionPreference = 'Stop'
$taskRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$taskEnvPath = Join-Path $taskRoot '.env'
if (Test-Path -LiteralPath $taskEnvPath) { throw '.env already exists; preserved it. See backend/README.md for applying migrations to an existing database.' }
$taskPsql = Join-Path $PostgresBin 'psql.exe'
if (-not (Test-Path -LiteralPath $taskPsql)) { throw "psql not found: $taskPsql" }
function Invoke-LocalSql([string]$Database, [string]$Sql) {
    $Sql | & $taskPsql -X -w -h $AdminHost -U $AdminUser -d $Database -v ON_ERROR_STOP=1 -q
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL setup failed. Existing databases and credentials were preserved.' }
}
function New-LocalSecret {
    $taskBytes = New-Object byte[] 32
    $taskRng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $taskRng.GetBytes($taskBytes) } finally { $taskRng.Dispose() }
    return ([Convert]::ToBase64String($taskBytes)).TrimEnd('=').Replace('+','-').Replace('/','_')
}
$taskExisting = & $taskPsql -X -w -h $AdminHost -U $AdminUser -d postgres -Atc "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='ipo_platform') OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname IN ('ipo_platform_owner','ipo_platform_api'));"
if ($LASTEXITCODE -ne 0) { throw 'Cannot authenticate to PostgreSQL. Configure your local pgpass.conf or PGPASSWORD first.' }
if ($taskExisting -ne 'f') { throw 'The requested database or roles already exist; no existing resources were changed.' }
$taskOwnerPassword = New-LocalSecret
$taskApiPassword = New-LocalSecret
$taskAdminPassword = New-LocalSecret
# Passwords contain only URL-safe characters. SQL is sent on stdin, not arguments.
Invoke-LocalSql 'postgres' "CREATE ROLE ipo_platform_owner LOGIN PASSWORD '$taskOwnerPassword'; CREATE ROLE ipo_platform_api LOGIN PASSWORD '$taskApiPassword'; CREATE DATABASE ipo_platform OWNER ipo_platform_owner;"
# Save credentials immediately so a migration failure does not lose access.
$taskEnvText = @"
# Local development credentials. Ignored by Git; keep this file private.
DATABASE_URL=postgresql://ipo_platform_api:$taskApiPassword@localhost:5432/ipo_platform?sslmode=disable
MIGRATION_DATABASE_URL=postgresql://ipo_platform_owner:$taskOwnerPassword@localhost:5432/ipo_platform?sslmode=disable
WHATSAPP_DATABASE_URL=postgresql://ipo_platform_owner:$taskOwnerPassword@localhost:5432/ipo_platform?sslmode=disable
API_ADDR=127.0.0.1:8180
BOOTSTRAP_BUSINESS_NAME=Local IPO Business
BOOTSTRAP_ADMIN_NAME=Local Administrator
BOOTSTRAP_ADMIN_PHONE=+919000000001
BOOTSTRAP_ADMIN_PASSWORD=$taskAdminPassword
"@
[IO.File]::WriteAllText($taskEnvPath, $taskEnvText, [Text.UTF8Encoding]::new($false))
# Only the current Windows user and SYSTEM may read the credential file.
$taskAcl = New-Object Security.AccessControl.FileSecurity
$taskAcl.SetAccessRuleProtection($true, $false)
$taskIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().User
$taskAcl.SetOwner($taskIdentity)
$taskAcl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($taskIdentity, 'FullControl', 'Allow'))
$taskAcl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-18'), 'FullControl', 'Allow'))
Set-Acl -LiteralPath $taskEnvPath -AclObject $taskAcl
foreach ($taskMigration in @('001_initial.sql', '002_api.sql')) {
    $taskSql = Get-Content -Raw -LiteralPath (Join-Path $taskRoot "database/$taskMigration")
    Invoke-LocalSql 'ipo_platform' ("SET ROLE ipo_platform_owner;`n" + $taskSql)
}
Invoke-LocalSql 'ipo_platform' @'
REVOKE ALL ON DATABASE ipo_platform FROM PUBLIC;
GRANT CONNECT ON DATABASE ipo_platform TO ipo_platform_api;
GRANT USAGE ON SCHEMA ipo_pos TO ipo_platform_api;
GRANT SELECT ON ALL TABLES IN SCHEMA ipo_pos TO ipo_platform_api;
GRANT INSERT ON ipo_pos.users, ipo_pos.businesses, ipo_pos.business_memberships,
    ipo_pos.user_credentials, ipo_pos.ipos, ipo_pos.manual_transaction_requests,
    ipo_pos.transaction_versions TO ipo_platform_api;
GRANT INSERT, UPDATE ON ipo_pos.customers, ipo_pos.transactions TO ipo_platform_api;
GRANT INSERT, DELETE ON ipo_pos.login_sessions TO ipo_platform_api;
-- SELECT ... FOR SHARE in transaction guards needs UPDATE on at least one column.
GRANT UPDATE (id) ON ipo_pos.businesses, ipo_pos.users TO ipo_platform_api;
GRANT UPDATE (user_id) ON ipo_pos.business_memberships TO ipo_platform_api;
'@
Write-Output "Created ipo_platform, separate migration/API roles, and $taskEnvPath. Passwords were not printed."
