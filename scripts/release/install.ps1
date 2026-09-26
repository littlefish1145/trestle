[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Trestle\alpha'),
    [switch]$NoUserPath
)

$ErrorActionPreference = 'Stop'
$source = Join-Path (Join-Path $PSScriptRoot 'bin') 'trestle.exe'
if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
    throw 'bin\trestle.exe is missing from this package.'
}

$binDir = Join-Path $InstallDir 'bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
Copy-Item -LiteralPath $source -Destination (Join-Path $binDir 'trestle.exe') -Force

function Test-PathEntry([string]$PathValue, [string]$Candidate) {
    $normalizedCandidate = $Candidate.TrimEnd('\')
    foreach ($entry in ($PathValue -split ';')) {
        if ($entry.Trim().TrimEnd('\') -ieq $normalizedCandidate) {
            return $true
        }
    }
    return $false
}

if ($env:GITHUB_PATH) {
    if (-not (Test-PathEntry $env:PATH $binDir)) {
        Add-Content -LiteralPath $env:GITHUB_PATH -Value $binDir -Encoding utf8
    }
    Write-Host "Installed Trestle in $binDir and added it to GITHUB_PATH."
} elseif (-not $NoUserPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (Test-PathEntry $userPath $binDir)) {
        $newPath = if ([string]::IsNullOrWhiteSpace($userPath)) { $binDir } else { "$userPath;$binDir" }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    }
    Write-Host "Installed Trestle in $binDir and added it to the user PATH."
    Write-Host 'Open a new terminal before invoking trestle.'
} else {
    Write-Host "Installed Trestle in $binDir. Add that directory to PATH before invoking trestle."
}
