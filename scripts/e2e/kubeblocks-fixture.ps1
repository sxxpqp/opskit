param(
    [string]$Directory = 'dist/.kubeblocks-e2e',
    [string]$Date = (Get-Date -Format 'yyyyMMdd'),
    [string]$Node = 'worker1'
)
$ErrorActionPreference = 'Stop'
if ($Date -notmatch '^\d{8}$' -or $Node -notmatch '^(master1|worker1)$') {
    throw 'Only dedicated Vagrant node names and an eight-digit date are accepted.'
}
# Generate fixtures only. Applying them and creating the VM directories are separate, explicit steps.
$runName = "opskit-kb-e2e-$Date"
$resolvedDirectory = [IO.Path]::GetFullPath($Directory)
New-Item -ItemType Directory -Path $resolvedDirectory -Force | Out-Null
function Write-Fixture([string]$Name, [string]$Text) {
    $target = Join-Path $resolvedDirectory $Name
    if (Test-Path -LiteralPath $target) { throw "Refuse to overwrite $target; use an empty directory." }
    [IO.File]::WriteAllText($target, $Text, (New-Object Text.UTF8Encoding($false)))
}
$objects = @(
    @{apiVersion='v1'; kind='Namespace'; metadata=@{name=$runName}},
    @{apiVersion='storage.k8s.io/v1'; kind='StorageClass'; metadata=@{name=$runName};
      provisioner='kubernetes.io/no-provisioner'; volumeBindingMode='WaitForFirstConsumer'; reclaimPolicy='Retain'}
)
for ($index = 1; $index -le 16; $index++) {
    $objects += @{
        apiVersion='v1'; kind='PersistentVolume'
        metadata=@{name="$runName-pv-$index"; labels=@{'opskit.io/e2e'="kubeblocks-$Date"}}
        spec=@{
            capacity=@{storage='2Gi'}; accessModes=@('ReadWriteOnce'); storageClassName=$runName
            persistentVolumeReclaimPolicy='Retain'
            hostPath=@{path="/var/lib/opskit-e2e/kubeblocks-$Date/pv-$index"; type='Directory'}
            nodeAffinity=@{required=@{nodeSelectorTerms=@(@{matchExpressions=@(
                @{key='kubernetes.io/hostname'; operator='In'; values=@($Node)}
            )})}}
        }
    }
}
Write-Fixture 'storage.json' (@{apiVersion='v1'; kind='List'; items=$objects} | ConvertTo-Json -Depth 20)
Write-Fixture 'cluster.yaml' @"
schema_version: 1
kubeblocks:
  namespace: $runName
  name: opskit-kb-$Date
  storage_class: $runName
  storage_size: 1Gi
  allow_colocation: true
  mysql: {cpu: 100m, memory: 256Mi, limit_cpu: '1', limit_memory: 768Mi}
  redis: {cpu: 50m, memory: 128Mi, limit_cpu: '1', limit_memory: 384Mi}
  minio: {cpu: 50m, memory: 256Mi, limit_cpu: '1', limit_memory: 768Mi}
"@
foreach ($container in @('manager', 'dataprotection')) {
    $patch = @{spec=@{template=@{spec=@{containers=@(@{
        name=$container
        resources=@{requests=@{cpu='100m'; memory='256Mi'}; limits=@{cpu='500m'; memory='1Gi'}}
    })}}}}
    Write-Fixture "$container.json" ($patch | ConvertTo-Json -Depth 12)
}
$service = @{
    apiVersion='v1'; kind='Service'; metadata=@{name="$runName-minio-api"; namespace=$runName}
    spec=@{type='NodePort'; selector=@{'app.kubernetes.io/instance'="opskit-kb-$Date-minio";
        'apps.kubeblocks.io/component-name'='minio'}
        ports=@(@{name='s3'; port=9000; targetPort=9000; nodePort=31090})}
}
Write-Fixture 'minio-nodeport.json' ($service | ConvertTo-Json -Depth 10)
Write-Output "Generated dedicated test fixtures in $resolvedDirectory; no cluster or VM was modified."
