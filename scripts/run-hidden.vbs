' ============================================================================
'  run-hidden.vbs —— 通用无窗口启动器（家庭财务系统用）
'
'  用法: wscript.exe //nologo run-hidden.vbs <命令> [参数...]
'  原理: WScript.Shell.Run(cmd, 0, False) —— 0=隐藏窗口, False=异步
'  每个参数自动加双引号，带空格的路径也能正确传参。
' ============================================================================
Option Explicit

Dim sh, cmd, i, arg

If WScript.Arguments.Count < 1 Then
    WScript.Echo "用法: wscript //nologo run-hidden.vbs <命令> [参数...]"
    WScript.Quit 2
End If

cmd = ""
For i = 0 To WScript.Arguments.Count - 1
    arg = WScript.Arguments(i)
    If i > 0 Then cmd = cmd & " "
    If Len(arg) > 1 And Left(arg, 1) = """" And Right(arg, 1) = """" Then
        cmd = cmd & arg
    Else
        cmd = cmd & """" & arg & """"
    End If
Next

Set sh = CreateObject("WScript.Shell")
sh.Run cmd, 0, False

WScript.Quit 0
