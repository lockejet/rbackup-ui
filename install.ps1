# rbackup-ui Windows 安装器（PowerShell 5.1+）
#
# 推荐用法（先下载再执行，避免 Windows PowerShell 5.1 的管道编码问题）：
#   irm https://github.com/lockejet/rbackup-ui/releases/latest/download/install.ps1 -OutFile install.ps1
#   powershell -ExecutionPolicy Bypass -File .\install.ps1
#
# 也可直接运行（若执行策略允许）：
#   .\install.ps1 [-Version latest] [-InstallDir DIR] [-AddToPath] [-Uninstall] [-DryRun]
#
# 注意：不要用 `irm ... | iex`。Windows PowerShell 5.1 会把没有 charset 的下载内容
#       按 Latin-1 解码，中文提示会乱码（安装本身仍会执行）；请用上面的 -OutFile 方式。
#
# 说明：
#   - 下载官方 Release 的 rbackup-ui-<ver>-windows-amd64.zip，校验 SHA256 后解压安装；
#   - 安装 rbackup-tui.exe / rbackup-web.exe / rbackup-cli + 配置样例到 InstallDir，
#     配置骨架写到 %APPDATA%\rbackup-ui\config.ini；
#   - -AddToPath 把 InstallDir 加入当前用户 PATH（幂等）；
#   - -Uninstall 按安装清单删除程序，保留配置与日志；
#   - -DryRun 只打印将要执行的动作。
#
# 依赖提示：备份任务需要 bash（Git for Windows 或 MSYS2）与 rsync（MSYS2: pacman -S rsync）。
#           C:\Windows\System32\bash.exe 是 WSL 启动器，不是 Git Bash/MSYS2，程序会跳过它。

[CmdletBinding()]
param(
    [string]$Version = "latest",
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\rbackup-ui",
    [switch]$AddToPath,
    [switch]$Uninstall,
    [switch]$DryRun
)

# 控制台输出切到 UTF-8：中文 Windows 默认 GBK，不切换会让中文/符号乱码。
try {
    [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $null = chcp.com 65001
} catch {
    # 无控制台（如某些 CI / 重定向场景）时忽略
}

$ErrorActionPreference = "Stop"
$Repo = "lockejet/rbackup-ui"
$ManifestName = "install-manifest.txt"

function Info($msg) { Write-Host ">>> $msg" }
function Warn($msg) { Write-Host "警告: $msg" -ForegroundColor Yellow }
function Die($msg) { Write-Host "错误: $msg" -ForegroundColor Red; exit 1 }
function Step($msg) { if ($DryRun) { Write-Host "[dry-run] $msg" } else { Info $msg } }

# 查找 Git for Windows / MSYS2 的 bash（优先于 PATH，避免误选 WSL 启动器）。
function Find-GitBash {
    $candidates = @()
    foreach ($envName in @("ProgramFiles", "ProgramW6432", "ProgramFiles(x86)")) {
        $root = [Environment]::GetEnvironmentVariable($envName)
        if ($root) {
            $candidates += (Join-Path $root "Git\bin\bash.exe")
            $candidates += (Join-Path $root "Git\usr\bin\bash.exe")
        }
    }
    $localAppData = [Environment]::GetEnvironmentVariable("LOCALAPPDATA")
    if ($localAppData) {
        $candidates += (Join-Path $localAppData "Programs\Git\bin\bash.exe")
        $candidates += (Join-Path $localAppData "Programs\Git\usr\bin\bash.exe")
    }
    $candidates += "C:\msys64\usr\bin\bash.exe", "D:\msys64\usr\bin\bash.exe"
    foreach ($c in $candidates) {
        if ($c -and (Test-Path $c)) { return $c }
    }
    return $null
}

# bash 是否为 WSL 启动器（System32/Sysnative 下的 bash.exe）。
function Test-WSLBash($path) {
    if (-not $path) { return $false }
    return ($path -like "*\System32\bash.exe") -or ($path -like "*\Sysnative\bash.exe")
}

# 用 bash 探测命令是否存在（成功返回 $true）。
function Test-BashCommand($bash, $name) {
    try {
        & $bash -c "command -v $name" *> $null
        return ($LASTEXITCODE -eq 0)
    } catch {
        return $false
    }
}

if ($PSVersionTable.PSVersion.Major -lt 5) { Die "需要 PowerShell 5.1 或更高版本" }

# ---------- 卸载 ----------
if ($Uninstall) {
    $manifest = Join-Path $InstallDir $ManifestName
    if (-not (Test-Path $manifest)) {
        Die "找不到安装清单：$manifest（可能未通过本脚本安装）"
    }
    $removed = 0
    foreach ($line in (Get-Content $manifest -Encoding UTF8)) {
        $f = $line.Trim()
        if ($f -eq "" -or $f.StartsWith("#")) { continue }
        if (Test-Path $f) {
            Step "删除 $f"
            if (-not $DryRun) { Remove-Item -Force $f }
            $removed++
        }
    }
    if (-not $DryRun) {
        Remove-Item -Force $manifest -ErrorAction SilentlyContinue
        if ((Test-Path $InstallDir) -and -not (Get-ChildItem $InstallDir -Force -ErrorAction SilentlyContinue)) {
            Remove-Item -Force $InstallDir -ErrorAction SilentlyContinue
        }
        Info "卸载完成，共移除 $removed 个文件"
        Info "配置与日志已保留：%APPDATA%\rbackup-ui、%LOCALAPPDATA%\rbackup-ui\Logs"
    }
    exit 0
}

# ---------- 解析版本 ----------
if ($Version -eq "latest") {
    Step "查询最新版本（$Repo）"
    $rel = Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/$Repo/releases/latest" `
        -Headers @{ "User-Agent" = "rbackup-ui-installer" }
    $Version = $rel.tag_name
    if (-not $Version) { Die "无法解析最新版本号" }
}
if (-not $Version.StartsWith("v")) { $Version = "v$Version" }
$Asset = "rbackup-ui-$Version-windows-amd64.zip"
$BaseUrl = "https://github.com/$Repo/releases/download/$Version"
Info "版本: $Version  安装目录: $InstallDir"

# ---------- 下载与校验 ----------
$Tmp = Join-Path $env:TEMP ("rbackup-ui-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
    $sumsPath = Join-Path $Tmp "SHA256SUMS"
    $zipPath = Join-Path $Tmp $Asset
    Step "下载 SHA256SUMS"
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/SHA256SUMS" -OutFile $sumsPath
    Step "下载 $Asset"
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$Asset" -OutFile $zipPath

    $expected = $null
    foreach ($line in (Get-Content $sumsPath -Encoding UTF8)) {
        if ($line -match ("^([0-9a-fA-F]{64})\s+\*?" + [regex]::Escape($Asset) + "$")) {
            $expected = $Matches[1].ToLower()
            break
        }
    }
    if (-not $expected) { Die "SHA256SUMS 中找不到 $Asset" }
    $actual = (Get-FileHash -Algorithm SHA256 -Path $zipPath).Hash.ToLower()
    if ($actual -ne $expected) { Die "SHA256 校验失败`n  期望: $expected`n  实际: $actual" }
    Info "SHA256 校验通过"

    $extract = Join-Path $Tmp "extract"
    Expand-Archive -Force -Path $zipPath -DestinationPath $extract
    $exe = Get-ChildItem -Path $extract -Recurse -Filter "rbackup-tui.exe" | Select-Object -First 1
    if (-not $exe) { Die "压缩包内找不到 rbackup-tui.exe（v2 起主程序拆分为 rbackup-tui / rbackup-web）" }
    $root = $exe.Directory.FullName

    if ($DryRun) {
        Step "将安装 $exe 到 $InstallDir\rbackup-tui.exe（并生成 rbackup-web.exe）"
        Step "将复制 rbackup-cli / 配置样例到 $InstallDir"
        Step "配置骨架: $env:APPDATA\rbackup-ui\config.ini（不存在才创建）"
        if ($AddToPath) { Step "将把 $InstallDir 加入用户 PATH" }
        exit 0
    }

    # ---------- 安装 ----------
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    # 升级清理：v1 的 rbackup-ui.exe / rbackup.sh 不再使用
    foreach ($legacy in @("rbackup-ui.exe", "rbackup.sh")) {
        $p = Join-Path $InstallDir $legacy
        if (Test-Path $p) { Remove-Item -Force $p }
    }
    foreach ($name in @("rbackup-tui.exe", "rbackup-web.exe", "rbackup-cli")) {
        $p = Join-Path $InstallDir $name
        if (Test-Path $p) { Remove-Item -Force $p }
    }
    Copy-Item -Force $exe.FullName (Join-Path $InstallDir "rbackup-tui.exe")
    $installed = @(Join-Path $InstallDir "rbackup-tui.exe")

    # rbackup-web.exe 与 rbackup-tui.exe 是同一实现：优先硬链接（同一 inode）
    $webSrc = Join-Path $root "rbackup-web.exe"
    $webDst = Join-Path $InstallDir "rbackup-web.exe"
    if (Test-Path $webSrc) {
        Copy-Item -Force $webSrc $webDst
    } else {
        try {
            New-Item -ItemType HardLink -Path $webDst -Target (Join-Path $InstallDir "rbackup-tui.exe") | Out-Null
        } catch {
            Copy-Item -Force (Join-Path $InstallDir "rbackup-tui.exe") $webDst
        }
    }
    $installed += $webDst

    foreach ($name in @("rbackup-cli")) {
        $src = Join-Path $root $name
        if (Test-Path $src) {
            Copy-Item -Force $src (Join-Path $InstallDir $name)
            $installed += (Join-Path $InstallDir $name)
        }
    }
    $examplesDir = Join-Path $InstallDir "examples"
    New-Item -ItemType Directory -Force -Path $examplesDir | Out-Null
    foreach ($name in @("config1.ini.example", "config2.ini.example")) {
        $src = Join-Path $root $name
        if (Test-Path $src) {
            Copy-Item -Force $src (Join-Path $examplesDir $name)
            $installed += (Join-Path $examplesDir $name)
        }
    }

    # 配置骨架：只补不覆盖
    $configDir = Join-Path $env:APPDATA "rbackup-ui"
    $configPath = Join-Path $configDir "config.ini"
    if (-not (Test-Path $configPath)) {
        New-Item -ItemType Directory -Force -Path $configDir | Out-Null
        $sample = Join-Path $examplesDir "config2.ini.example"
        if (Test-Path $sample) {
            Copy-Item -Force $sample $configPath
            Info "已生成配置骨架 $configPath（请修改 HOST / SSH_KEY）"
        } else {
            Warn "缺少配置样例，请手工创建 $configPath"
        }
    } else {
        Info "已存在配置，未覆盖: $configPath"
    }

    # 安装清单
    $manifestPath = Join-Path $InstallDir $ManifestName
    $lines = @("# rbackup-ui install manifest", "# version=$Version", "# date=$((Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ"))") + $installed
    Set-Content -Path $manifestPath -Value $lines -Encoding UTF8
    $installed += $manifestPath

    # PATH（用户级，幂等）
    if ($AddToPath) {
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        $parts = @()
        if ($userPath) { $parts = $userPath -split ";" | Where-Object { $_ -ne "" } }
        if ($parts -notcontains $InstallDir) {
            $newPath = (@($parts) + $InstallDir) -join ";"
            [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
            Info "已把 $InstallDir 加入用户 PATH（新开终端生效）"
        } else {
            Info "PATH 已包含 $InstallDir"
        }
    }

    # ---------- 依赖提示 ----------
    # 优先用 Git/MSYS2 的 bash：PATH 里的 C:\Windows\System32\bash.exe 是 WSL 启动器。
    $bash = Find-GitBash
    $bashIsWSL = $false
    if (-not $bash) {
        $cmd = Get-Command bash -ErrorAction SilentlyContinue
        if ($cmd) {
            $bash = $cmd.Source
            $bashIsWSL = Test-WSLBash $bash
        }
    }
    Write-Host ""
    if (-not $bash) {
        Warn "未找到 bash：备份任务需要 Git for Windows 或 MSYS2 提供 bash"
        Warn "  安装 Git for Windows: https://git-scm.com/download/win"
        Warn "  或 MSYS2（并安装 rsync）: pacman -S rsync"
        Warn "  装好后可运行: $InstallDir\rbackup-tui.exe --doctor 复检"
    } elseif ($bashIsWSL) {
        Warn "只找到 WSL 启动器 bash：$bash"
        Warn "  它不是 Git Bash/MSYS2，不能直接用于备份任务；请安装 Git for Windows 或 MSYS2"
        Warn "  已安装但不在常见路径？运行时可用 --bash 指定，例如："
        Warn "  $InstallDir\rbackup-tui.exe --bash C:\msys64\usr\bin\bash.exe"
    } else {
        Info "bash: $bash"
        if (Test-BashCommand $bash "rsync") {
            Info "rsync: 已找到"
        } else {
            Warn "未在 bash 环境中找到 rsync；如用 MSYS2 请执行: pacman -S rsync"
            Warn "（Git for Windows 默认不带 rsync，可用 MSYS2 或 cwRsync）"
        }
        foreach ($tool in @("ssh", "sed", "awk")) {
            if (-not (Test-BashCommand $bash $tool)) {
                Warn "未找到 $tool（备份任务需要）"
            }
        }
    }

    Write-Host ""
    Info "安装完成：$InstallDir\rbackup-tui.exe（$Version）"
    Info "自检:   $InstallDir\rbackup-tui.exe --doctor"
    Info "Web:    $InstallDir\rbackup-web.exe    然后浏览器打开 http://127.0.0.1:30031"
    Info "TUI:    $InstallDir\rbackup-tui.exe"
    Info "CLI:    $InstallDir\rbackup-cli"
    Info "卸载:   .\install.ps1 -Uninstall"
} finally {
    if (Test-Path $Tmp) { Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue }
}
