param([string]$Image = '', [string]$DockerBinary = 'docker', [string]$OpskitBinary = '')
# Windows PowerShell + Docker Desktop integration test. Uses only unique test resources.
if ($env:OS -ne 'Windows_NT') { throw 'This verification script requires Windows PowerShell / PowerShell on Windows.' }
$ErrorActionPreference = 'Stop'
$OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))
$docker = $DockerBinary
if (-not $OpskitBinary) { $OpskitBinary = Join-Path $repoRoot '.opskit/build/opskit_windows_amd64.exe' }
$opskit = [IO.Path]::GetFullPath($OpskitBinary)
$runId = [Guid]::NewGuid().ToString('N').Substring(0,10)
$prefix = 'opskit-e2e-' + $runId
$root = Join-Path (Join-Path $repoRoot '.opskit/mysql-e2e') $runId
$auth = Join-Path $root 'auth'
$network = $prefix + '-net'
$source = $prefix + '-source'
$restore = $prefix + '-restore'
$created = New-Object 'System.Collections.Generic.List[string]'
$checks = New-Object 'System.Collections.Generic.List[string]'
$networkCreated = $false
$report = [ordered]@{ run_id=$runId; started_at=[DateTime]::UtcNow.ToString('o'); status='running'; checks=$checks; artifact_directory=$root }
New-Item -ItemType Directory -Path $auth -Force | Out-Null
$acl = New-Object System.Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true,$false)
$currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
foreach ($sid in @($currentSid, (New-Object Security.Principal.SecurityIdentifier('S-1-5-18')))) {
    $rule = New-Object Security.AccessControl.FileSystemAccessRule($sid,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
    $acl.AddAccessRule($rule)
}
Set-Acl -LiteralPath $auth -AclObject $acl
$password = 'E2e-' + [Guid]::NewGuid().ToString('N') + '#test'
$encoding = New-Object System.Text.UTF8Encoding($false)
[IO.File]::WriteAllText((Join-Path $auth 'root-password'),$password,$encoding)
[IO.File]::WriteAllText((Join-Path $auth 'client.cnf'),"[client]`nuser=root`npassword=`"$password`"`n",$encoding)
$config = Join-Path $root 'config.yaml'
[IO.File]::WriteAllText($config,"schema_version: 1`n",$encoding)
$previousPassword = $env:MYSQL_BACKUP_PASSWORD
$env:MYSQL_BACKUP_PASSWORD = $password

function Invoke-Docker {
    param([string[]]$DockerArgs,[switch]$AllowFailure,[string]$InputText)
    $oldPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        if ($PSBoundParameters.ContainsKey('InputText')) { $lines = $InputText | & $docker @DockerArgs 2>&1 }
        else { $lines = & $docker @DockerArgs 2>&1 }
        $code = $LASTEXITCODE
    } finally { $ErrorActionPreference = $oldPreference }
    $text = ($lines | ForEach-Object { $_.ToString() }) -join "`n"
    if ($code -ne 0 -and -not $AllowFailure) { throw "Docker failed ($code): $text" }
    return [pscustomobject]@{ Code=$code; Text=$text }
}
function Invoke-SQL {
    param([string]$Container,[string]$SQL)
    $result = Invoke-Docker -DockerArgs @('exec','-i',$Container,'mysql','--defaults-file=/tmp/e2e-client.cnf','--batch','--skip-column-names','--default-character-set=utf8mb4') -InputText $SQL
    return $result.Text.Trim()
}
function Start-TestServer {
    param([string]$Name,[string]$ImageId)
    $created.Add($Name)
    $null = Invoke-Docker -DockerArgs @('run','--detach','--name',$Name,'--network',$network,'--label',"opskit.e2e=$runId",'--mount',"type=bind,source=$auth,target=/e2e-auth,readonly",'--env','MYSQL_ROOT_PASSWORD_FILE=/e2e-auth/root-password','--env','MYSQL_ROOT_HOST=%',$ImageId)
    $null = Invoke-Docker -DockerArgs @('exec',$Name,'/bin/sh','-c','umask 077; cp /e2e-auth/client.cnf /tmp/e2e-client.cnf; chmod 0600 /tmp/e2e-client.cnf')
    $deadline = [DateTime]::UtcNow.AddMinutes(3)
    while ([DateTime]::UtcNow -lt $deadline) {
        $ready = Invoke-Docker -DockerArgs @('exec',$Name,'mysql','--defaults-file=/tmp/e2e-client.cnf','--protocol=TCP','--host=127.0.0.1','--batch','--skip-column-names','--execute=SELECT 1') -AllowFailure
        if ($ready.Code -eq 0 -and $ready.Text.Trim() -eq '1') { Write-Output "Ready: $Name"; return }
        Start-Sleep -Seconds 2
    }
    throw "MySQL startup timed out: $Name"
}
function Invoke-Backup {
    param([string]$Label,[string[]]$ExtraArgs,[int]$ExpectedCode=0)
    $directory = Join-Path $root $Label
    $backupArgs = @('mysql','backup','--config',$config,'--client-mode','docker','--image',$script:imageId,'--pull-policy','never','--docker-bin',$docker,'--docker-network',$network,'--host',$source,'--user','root','--directory',$directory,'--timeout','2m','--mysql-client','not-installed','--mysqldump','not-installed') + $ExtraArgs
    $diagFile = Join-Path $root ($Label + '.stderr.txt')
    $oldPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { $lines = & $opskit @backupArgs 2> $diagFile; $code = $LASTEXITCODE }
    finally { $ErrorActionPreference = $oldPreference }
    if ($code -ne $ExpectedCode) { throw "Backup '$Label' exit $code (expected $ExpectedCode); diagnostics: $([IO.File]::ReadAllText($diagFile))" }
    if ($ExpectedCode -ne 0) {
        if (Get-ChildItem -LiteralPath $directory -Filter manifest.json -Recurse) { throw "Failed backup published a manifest: $Label" }
        return
    }
    $manifest = (($lines -join "`n") | ConvertFrom-Json)
    $tasks = @(Get-ChildItem -LiteralPath $directory -Directory)
    if ($tasks.Count -ne 1) { throw 'Unexpected backup task count' }
    $file = Join-Path $tasks[0].FullName $manifest.file
    $hash = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($hash -ne $manifest.sha256 -or (Get-Item -LiteralPath $file).Length -ne $manifest.size) { throw 'Backup hash/length mismatch' }
    if (Get-ChildItem -LiteralPath $tasks[0].FullName -Filter '.mysql-auth-*' -Force) { throw 'Temporary backup credentials retained' }
    return [pscustomobject]@{ File=$file; Manifest=$manifest }
}

try {
    if (-not $Image) {
        $plan = & $opskit mysql backup --config $config --client-mode docker --database opskit_e2e --host db.internal --dry-run | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0) { throw 'Cannot read default image from embedded template' }
        $Image = $plan.image
    }
    $script:imageId = (Invoke-Docker -DockerArgs @('image','inspect',$Image,'--format','{{.Id}}')).Text.Trim()
    $report.image = $Image
    $report.image_id = $script:imageId
    $null = Invoke-Docker -DockerArgs @('network','create','--internal','--label',"opskit.e2e=$runId",$network)
    $networkCreated = $true
    Start-TestServer -Name $source -ImageId $script:imageId
    $report.server_version = Invoke-SQL -Container $source -SQL 'SELECT VERSION();'
    $seed = @'
CREATE DATABASE opskit_e2e CHARACTER SET utf8mb4;
CREATE DATABASE opskit_logs CHARACTER SET utf8mb4;
CREATE TABLE opskit_e2e.ledger(id INT PRIMARY KEY, note VARCHAR(100), amount DECIMAL(12,2), payload VARBINARY(20)) ENGINE=InnoDB;
INSERT INTO opskit_e2e.ledger VALUES (1,CONVERT(0xe4b8ade69687 USING utf8mb4),12.34,0x000127ff),(2,'quoted '' value',-99.50,0x00ff);
CREATE VIEW opskit_e2e.ledger_view AS SELECT id,note FROM opskit_e2e.ledger;
CREATE TRIGGER opskit_e2e.ledger_default BEFORE INSERT ON opskit_e2e.ledger FOR EACH ROW SET NEW.note=COALESCE(NEW.note,'default');
CREATE PROCEDURE opskit_e2e.row_count() SELECT COUNT(*) FROM opskit_e2e.ledger;
CREATE EVENT opskit_e2e.disabled_event ON SCHEDULE EVERY 1 DAY DISABLE DO INSERT INTO opskit_e2e.ledger VALUES(999,'event',0,0x00);
CREATE TABLE opskit_logs.entries(id INT PRIMARY KEY, message TEXT) ENGINE=InnoDB;
INSERT INTO opskit_logs.entries VALUES(7,'second database');
'@
    $null = Invoke-SQL -Container $source -SQL $seed
    $checks.Add('seed: two InnoDB databases, UTF-8, quotes, binary bytes, decimals, view, routine, trigger and disabled event')
    $backup = Invoke-Backup -Label 'gzip' -ExtraArgs @('--database','opskit_e2e','--database','opskit_logs')
    $checks.Add('Docker gzip backup without local mysql/mysqldump; SHA-256 and length match; credentials cleaned')
    $plain = Invoke-Backup -Label 'plain' -ExtraArgs @('--database','opskit_e2e','--gzip=false')
    if ($plain.Manifest.file -ne 'backup.sql') { throw 'Unexpected plain SQL file name' }
    $checks.Add('Docker uncompressed SQL backup and checksum')
    $caFile = Join-Path $root 'mysql-ca.pem'
    $null = Invoke-Docker -DockerArgs @('cp',($source+':/var/lib/mysql/ca.pem'),$caFile)
    $tlsBackup = Invoke-Backup -Label 'tls-ca' -ExtraArgs @('--database','opskit_e2e','--ssl-mode','VERIFY_CA','--ssl-ca',$caFile)
    $checks.Add('VERIFY_CA backup using the real server CA staged into the client tmpfs')
    $null = Invoke-SQL -Container $source -SQL 'CREATE TABLE opskit_e2e.nontransactional(id INT) ENGINE=MyISAM;'
    Invoke-Backup -Label 'rejected-myisam' -ExtraArgs @('--database','opskit_e2e') -ExpectedCode 1
    $checks.Add('non-InnoDB table rejected without published manifest')
    $null = Invoke-SQL -Container $source -SQL 'DROP TABLE opskit_e2e.nontransactional;'
    Start-TestServer -Name $restore -ImageId $script:imageId
    $wrongCA = Join-Path $root 'other-server-ca.pem'
    $null = Invoke-Docker -DockerArgs @('cp',($restore+':/var/lib/mysql/ca.pem'),$wrongCA)
    Invoke-Backup -Label 'rejected-ca' -ExtraArgs @('--database','opskit_e2e','--ssl-mode','VERIFY_CA','--ssl-ca',$wrongCA) -ExpectedCode 1
    $checks.Add('unrelated server CA rejected without published manifest')
    $sqlFile = Join-Path $root 'restore.sql'
    $inputStream = [IO.File]::OpenRead($backup.File)
    $gzipStream = New-Object IO.Compression.GzipStream($inputStream,[IO.Compression.CompressionMode]::Decompress)
    $outputStream = [IO.File]::Create($sqlFile)
    try { $gzipStream.CopyTo($outputStream) } finally { $outputStream.Dispose(); $gzipStream.Dispose(); $inputStream.Dispose() }
    # Avoid Windows PowerShell's native stdin encoding conversion for UTF-8 dump bytes.
    $null = Invoke-Docker -DockerArgs @('cp',$sqlFile,($restore+':/tmp/e2e-restore.sql'))
    $null = Invoke-Docker -DockerArgs @('exec',$restore,'mysql','--defaults-file=/tmp/e2e-client.cnf','--default-character-set=utf8mb4','--execute=source /tmp/e2e-restore.sql')
    $verify = @'
SELECT CONCAT(id,':',HEX(note),':',amount,':',HEX(payload)) FROM opskit_e2e.ledger ORDER BY id;
SELECT CONCAT(id,':',HEX(message)) FROM opskit_logs.entries ORDER BY id;
SELECT COUNT(*) FROM information_schema.views WHERE table_schema='opskit_e2e';
SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema='opskit_e2e';
SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema='opskit_e2e';
SELECT COUNT(*) FROM information_schema.events WHERE event_schema='opskit_e2e' AND status='DISABLED';
CALL opskit_e2e.row_count();
SELECT HEX(note) FROM opskit_e2e.ledger_view WHERE id=1;
INSERT INTO opskit_e2e.ledger VALUES(3,NULL,0,0x00);
SELECT HEX(note) FROM opskit_e2e.ledger WHERE id=3;
DELETE FROM opskit_e2e.ledger WHERE id=3;
'@
    $expected = Invoke-SQL -Container $source -SQL $verify
    $actual = Invoke-SQL -Container $restore -SQL $verify
    if ($expected -ne $actual) { throw "Restore data/object comparison failed`nSource: $expected`nRestore: $actual" }
    $report.verification_rows = $actual
    $checks.Add('isolated restore: exact rows/binary/UTF-8/decimal values, object counts, routine call, view query and trigger behavior match')
    $report.status = 'passed'
    Write-Output 'PASS: Docker MySQL backup and isolated restore'
} catch {
    $report.status = 'failed'
    $report.error = $_.Exception.Message
    Write-Output ('FAIL: ' + $_.Exception.Message)
} finally {
    $cleanupOk = $true
    foreach ($name in $created) {
        $removed = Invoke-Docker -DockerArgs @('rm','--force','--volumes',$name) -AllowFailure
        if ($removed.Code -ne 0) { $cleanupOk = $false; Write-Output "Cleanup failed: $name" }
    }
    if ($networkCreated) {
        $removed = Invoke-Docker -DockerArgs @('network','rm',$network) -AllowFailure
        if ($removed.Code -ne 0) { $cleanupOk = $false; Write-Output "Cleanup failed: $network" }
    }
    foreach ($file in @('root-password','client.cnf')) { Remove-Item -LiteralPath (Join-Path $auth $file) -ErrorAction SilentlyContinue }
    if ($null -eq $previousPassword) { Remove-Item Env:MYSQL_BACKUP_PASSWORD -ErrorAction SilentlyContinue } else { $env:MYSQL_BACKUP_PASSWORD = $previousPassword }
    $report.cleanup_passed = $cleanupOk
    $report.finished_at = [DateTime]::UtcNow.ToString('o')
    $reportFile = Join-Path $root 'report.json'
    [IO.File]::WriteAllText($reportFile,($report | ConvertTo-Json -Depth 6),$encoding)
    Write-Output "Report: $reportFile"
}
if ($report.status -ne 'passed' -or -not $report.cleanup_passed) { exit 1 }
