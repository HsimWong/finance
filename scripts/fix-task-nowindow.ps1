# 把 FamilyFinance 计划任务改成无窗口启动（避免每 15 分钟闪一个黑窗）
# 用 Register -Force 重建：Set-ScheduledTask 要求完整 XML，容易报
# "任务 XML 缺少必需元素"。这里显式重建触发器，保持原有行为不变。
$ErrorActionPreference = 'Stop'
$vbs = "C:\Users\WangXin\WorkBuddy\财务管理\scripts\run-hidden.vbs"
$bash = "C:\Program Files\Git\bin\bash.exe"
$wd   = "C:\Users\WangXin\WorkBuddy\财务管理"

$act = New-ScheduledTaskAction -Execute "C:\Windows\System32\wscript.exe" `
    -Argument "//nologo `"$vbs`" `"$bash`" scripts/start.sh" `
    -WorkingDirectory $wd

$trgLogon  = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERNAME"
$trgRepeat = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(2) `
    -RepetitionInterval (New-TimeSpan -Minutes 15) `
    -RepetitionDuration (New-TimeSpan -Days 3650)

$set = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -StartWhenAvailable

Register-ScheduledTask -TaskName "FamilyFinance" -Action $act -Trigger $trgLogon, $trgRepeat `
    -Settings $set -Description "家庭财务系统守护（无窗口，15分钟复查）" -Force | Out-Null

"已重建 FamilyFinance 任务"
(Get-ScheduledTask -TaskName "FamilyFinance").Actions |
    Select-Object Execute, Arguments, WorkingDirectory | Format-List
"触发器:"
(Get-ScheduledTask -TaskName "FamilyFinance").Triggers | ForEach-Object { "  $($_.CimClass.CimClassName)" }
