#Requires -Version 5.1
<# Repairs only existing shortcuts targeting this router; leaves other apps alone. #>
[CmdletBinding(SupportsShouldProcess)]
param(
    [string]$InstallRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
    [string]$StateRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router Data'),
    [string[]]$ShortcutPaths
)
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'ShortcutIdentity.psm1') -Force
$launcher = [IO.Path]::GetFullPath((Join-Path $InstallRoot 'ChatGPT.exe'))
$child = [IO.Path]::GetFullPath((Join-Path $InstallRoot 'ChatGPT.real.exe'))
if (-not (Test-Path -LiteralPath $launcher -PathType Leaf)) { throw 'Router launcher is missing.' }
$appId = 'com.openai.codex.subscription-router'
if (-not $ShortcutPaths) {
    $directories = @(
        [Environment]::GetFolderPath('Desktop'),
        [Environment]::GetFolderPath('Programs'),
        (Join-Path $env:APPDATA 'Microsoft\Internet Explorer\Quick Launch\User Pinned\TaskBar')
    )
    $ShortcutPaths = @($directories | ForEach-Object {
        Get-ChildItem -LiteralPath $_ -Filter '*.lnk' -File -ErrorAction SilentlyContinue
    } | Select-Object -ExpandProperty FullName)
}
$shell = New-Object -ComObject WScript.Shell
try {
    foreach ($path in $ShortcutPaths) {
        $shortcut = $shell.CreateShortcut($path)
        try {
            if ($shortcut.TargetPath -ne $launcher -and $shortcut.TargetPath -ne $child) { continue }
            $identity = Get-CsrShortcutAppUserModelId -Path $path
            if ($shortcut.TargetPath -eq $launcher -and $shortcut.IconLocation -eq "$launcher,0" -and $identity -ceq $appId) {
                Write-Output "Already correct: $path"
                continue
            }
            if (-not $PSCmdlet.ShouldProcess($path, 'Back up and repair router target, icon and grouping identity')) { continue }
            $backupDirectory = Join-Path $StateRoot ('shortcut-backups\branding-' + [Guid]::NewGuid().ToString('N'))
            [void](New-Item -ItemType Directory -Path $backupDirectory)
            $backup = Join-Path $backupDirectory ([IO.Path]::GetFileName($path))
            Copy-Item -LiteralPath $path -Destination $backup
            try {
                $shortcut.TargetPath = $launcher
                $shortcut.WorkingDirectory = $InstallRoot
                $shortcut.IconLocation = "$launcher,0"
                $shortcut.Save()
                Set-CsrShortcutAppUserModelId -Path $path -AppUserModelId $appId
                if ((Get-CsrShortcutAppUserModelId -Path $path) -cne $appId) { throw 'Shortcut identity verification failed.' }
            }
            catch {
                Copy-Item -LiteralPath $backup -Destination $path -Force
                throw
            }
            Write-Output "Repaired: $path; backup: $backup"
        }
        finally { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) }
    }
}
finally { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) }
