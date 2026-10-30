[CmdletBinding()]
param([string[]]$Arguments = @('version'))
. (Join-Path $PSScriptRoot 'Common.ps1')
$webhookGoVersion = '1.27.3'
$webhookGoDirectory = Join-Path $script:WebhookRoot ('.local/tools/go' + $webhookGoVersion)
$webhookGoExe = Join-Path $webhookGoDirectory 'go/bin/go.exe'
$webhookGoReady = Join-Path $webhookGoDirectory '.ready'
$webhookGoHash = 'f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff'
if (-not (Test-Path -LiteralPath $webhookGoReady)) {
    New-Item -ItemType Directory -Path $webhookGoDirectory -Force | Out-Null
    $webhookArchive = Join-Path $webhookGoDirectory 'go.zip'
    $webhookPartial = $webhookArchive + '.partial'
    if (-not (Test-Path -LiteralPath $webhookArchive) -or (Get-FileHash -LiteralPath $webhookArchive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $webhookGoHash) {
        $webhookPriorProgress = $ProgressPreference
        try {
            $ProgressPreference = 'SilentlyContinue'
            Invoke-WebRequest -Uri ('https://go.dev/dl/go' + $webhookGoVersion + '.windows-amd64.zip') -UseBasicParsing -OutFile $webhookPartial
        } finally { $ProgressPreference = $webhookPriorProgress }
        if ((Get-FileHash -LiteralPath $webhookPartial -Algorithm SHA256).Hash.ToLowerInvariant() -ne $webhookGoHash) { throw 'Checksum Go tidak cocok; arsip tidak diekstrak.' }
        Move-Item -LiteralPath $webhookPartial -Destination $webhookArchive -Force
    }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $webhookZip = [System.IO.Compression.ZipFile]::OpenRead($webhookArchive)
    $webhookExtractRoot = [System.IO.Path]::GetFullPath($webhookGoDirectory).TrimEnd('\') + '\'
    try {
        foreach ($webhookEntry in $webhookZip.Entries) {
            if ($webhookEntry.FullName.EndsWith('/')) { continue }
            $webhookTarget = [System.IO.Path]::GetFullPath((Join-Path $webhookGoDirectory $webhookEntry.FullName))
            if (-not $webhookTarget.StartsWith($webhookExtractRoot,[StringComparison]::OrdinalIgnoreCase)) { throw 'Path arsip Go berada di luar folder SDK.' }
            [System.IO.Directory]::CreateDirectory([System.IO.Path]::GetDirectoryName($webhookTarget)) | Out-Null
            [System.IO.Compression.ZipFileExtensions]::ExtractToFile($webhookEntry,$webhookTarget,$true)
        }
    } finally { $webhookZip.Dispose() }
    [System.IO.File]::WriteAllText($webhookGoReady,$webhookGoVersion)
}
$webhookVariables = @('GOPATH','GOMODCACHE','GOCACHE','GOTOOLCHAIN','GOMAXPROCS','GOMEMLIMIT','GOGC','GOFLAGS')
$webhookPrevious = @{}
foreach ($webhookName in $webhookVariables) { $webhookPrevious[$webhookName] = [Environment]::GetEnvironmentVariable($webhookName,'Process') }
try {
    $env:GOPATH = Join-Path $script:WebhookRoot '.local/go'
    $env:GOMODCACHE = Join-Path $script:WebhookRoot '.local/go-mod'
    $env:GOCACHE = Join-Path $script:WebhookRoot '.local/go-cache'
    $env:GOTOOLCHAIN = 'local'
    $env:GOMAXPROCS = '2'
    $env:GOMEMLIMIT = '384MiB'
    $env:GOGC = '50'
    $env:GOFLAGS = '-p=2'
    & $webhookGoExe @Arguments
    if ($LASTEXITCODE -ne 0) { throw 'Perintah Go gagal. Periksa keluaran di atas.' }
} finally {
    foreach ($webhookName in $webhookVariables) { [Environment]::SetEnvironmentVariable($webhookName,$webhookPrevious[$webhookName],'Process') }
}
