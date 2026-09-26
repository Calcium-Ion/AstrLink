$ErrorActionPreference = "Stop"

$bundleDir = Join-Path $env:RUNNER_TEMP "astrlink-bundle-scan"
$smokeDir = Join-Path $env:RUNNER_TEMP "astrlink-smoke"
$installDir = Join-Path $env:RUNNER_TEMP "astrlink-installed"
Remove-Item $bundleDir, $smokeDir, $installDir -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $bundleDir, $smokeDir, $installDir | Out-Null

$installer = Get-ChildItem "src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/*.exe" | Select-Object -First 1
if (-not $installer) { throw "No Windows NSIS installer was produced" }
Write-Host "Installer SHA-256: $((Get-FileHash $installer.FullName -Algorithm SHA256).Hash.ToLowerInvariant())"

& 7z x $installer.FullName "-o$bundleDir" -y | Out-Null
if ($LASTEXITCODE -ne 0) { throw "Unable to inspect installer" }
$archives = @(Get-ChildItem $bundleDir -Recurse -Filter "*.7z")
foreach ($archive in $archives) {
  $inner = Join-Path $bundleDir ("inner-" + [guid]::NewGuid().ToString("N"))
  New-Item -ItemType Directory -Force -Path $inner | Out-Null
  & 7z x $archive.FullName "-o$inner" -y | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Unable to inspect embedded archive" }
}

& bun ../../scripts/check-no-production-models.mjs --packaged $bundleDir
if ($LASTEXITCODE -ne 0) { throw "Production model artifact found" }
$vc = Get-Content "scripts/windows-vc-runtime.json" -Raw | ConvertFrom-Json
$redists = @(Get-ChildItem $bundleDir -Recurse -Filter "vc_redist.x64.exe")
if ($redists.Count -ne 1) { throw "Expected one bundled VC runtime" }
if ((Get-FileHash $redists[0].FullName -Algorithm SHA256).Hash.ToLowerInvariant() -ne $vc.sha256) { throw "Bundled VC runtime hash mismatch" }
$directml = @(Get-ChildItem $bundleDir -Recurse -Filter "DirectML.dll")
if ($directml.Count -ne 1) { throw "Expected one bundled DirectML.dll" }

$install = Start-Process -FilePath $installer.FullName -ArgumentList @("/S", "/D=$installDir") -PassThru
if (-not $install.WaitForExit(120000)) {
  $install.Kill()
  throw "Silent installer timed out"
}
if ($install.ExitCode -ne 0) { throw "Silent installer failed: $($install.ExitCode)" }
$repository = (Resolve-Path (Join-Path $PSScriptRoot "../../..")).Path
foreach ($name in @("LICENSE", "LICENSING.md", "LICENSES/AGPL-3.0.txt")) {
  $installed = Join-Path $installDir "licenses/$name"
  $source = Join-Path $repository $name
  if ((Get-FileHash $installed -Algorithm SHA256).Hash -ne (Get-FileHash $source -Algorithm SHA256).Hash) {
    throw "Packaged $name does not match the repository license file"
  }
}
$names = @("astrlink-desktop.exe", "astrlink-core.exe", "astrlink-mcp.exe", "astrlink-privacy-worker.exe", "astrlink-classifier-worker.exe", "DirectML.dll")
$files = @{}
foreach ($name in $names) {
  $matches = @(Get-ChildItem $installDir -Recurse -File -Filter $name)
  if ($matches.Count -ne 1) { throw "Expected one installed $name" }
  $files[$name] = $matches[0].FullName
}

foreach ($name in @("astrlink-desktop.exe", "astrlink-core.exe", "astrlink-mcp.exe", "astrlink-privacy-worker.exe", "astrlink-classifier-worker.exe")) {
  $bytes = [IO.File]::ReadAllBytes($files[$name])
  if ($bytes.Length -lt 64 -or $bytes[0] -ne 0x4D -or $bytes[1] -ne 0x5A) { throw "$name is not PE" }
  $offset = [BitConverter]::ToInt32($bytes, 0x3C)
  if ($offset -lt 0 -or $offset + 24 -gt $bytes.Length -or $bytes[$offset] -ne 0x50 -or $bytes[$offset + 1] -ne 0x45) { throw "$name has invalid PE header" }
  if ([BitConverter]::ToUInt16($bytes, $offset + 4) -ne 0x8664) { throw "$name is not x64" }
}

function Start-WorkerSmoke([string]$path) {
  $stdout = Join-Path $smokeDir ((Split-Path $path -Leaf) + ".stdout.log")
  $stderr = Join-Path $smokeDir ((Split-Path $path -Leaf) + ".stderr.log")
  $process = Start-Process -FilePath $path -WorkingDirectory (Split-Path $path) -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
  # No model is passed: reaching the argument validator proves that the native
  # loader succeeded. Both workers deliberately exit 1 in this case.
  if (-not $process.WaitForExit(15000)) {
    $process.Kill()
    throw "$(Split-Path $path -Leaf) startup timed out"
  }
  if ($process.ExitCode -ne 1 -or (Get-Content $stderr -Raw) -notmatch "startup_or_protocol_failure") {
    throw "$(Split-Path $path -Leaf) failed to load its runtime: exit $($process.ExitCode)"
  }
  Write-Host "Verified worker loader: $(Split-Path $path -Leaf)"
}
Start-WorkerSmoke $files["astrlink-privacy-worker.exe"]
Start-WorkerSmoke $files["astrlink-classifier-worker.exe"]

$desktopStdout = Join-Path $smokeDir "astrlink-desktop.stdout.log"
$desktopStderr = Join-Path $smokeDir "astrlink-desktop.stderr.log"
$desktop = Start-Process -FilePath $files["astrlink-desktop.exe"] -WorkingDirectory (Split-Path $files["astrlink-desktop.exe"]) -RedirectStandardOutput $desktopStdout -RedirectStandardError $desktopStderr -PassThru
try {
  $ready = $false
  for ($attempt = 0; $attempt -lt 30; $attempt++) {
    Start-Sleep -Seconds 1
    try {
      $response = Invoke-WebRequest -Uri "http://127.0.0.1:18317/v1/models" -NoProxy -SkipHttpErrorCheck -TimeoutSec 2
      if ($response.StatusCode -in @(200, 401, 403)) { $ready = $true; break }
    } catch { }
    if ($desktop.HasExited) { throw "astrlink-desktop exited during startup" }
  }
  if (-not $ready) { throw "desktop did not expose /v1/models" }
} finally {
  if (-not $desktop.HasExited) { Stop-Process -Id $desktop.Id -Force -ErrorAction SilentlyContinue }
}
Write-Host "Windows installer verification passed"
