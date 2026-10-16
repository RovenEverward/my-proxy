; NSIS 安装脚本:把 mpx.exe 装进 $PROGRAMFILES64\mpx,注册卸载项,并把该目录加进
; 当前用户的 PATH(卸载时移除)。由 .github/workflows/release.yml 的 pkg-windows
; job 调用:
;
;   makensis -INPUTCHARSET UTF8 -DVERSION=0.7.2 \
;     -DSRCEXE="$(pwd)/mpx.exe" -DOUTFILE="$(pwd)/mpx_0.7.2_windows_amd64_setup.exe" \
;     deploy/windows/mpx.nsi
;
; 不签名。只用 NSIS 自带的东西(MUI2 / LogicLib / WinMessages / x64),不依赖
; EnVar、NsProcess 这类需要另外下载的插件:
;   - 改 PATH 是直接读改写 HKCU\Environment\Path,比对时给整串两侧补 ';',按
;     ";<目录>;" 精确匹配,既不误伤同名前缀的目录,也不会留下多余的 ';';
;   - 让已开的程序(资源管理器等)看到新 PATH 靠内置 SendMessage 广播
;     WM_SETTINGCHANGE,带超时兜底,不会卡死。

; NSIS 默认的字符串缓冲区只有 1024 字符,而用户的 PATH 完全可能更长。缓冲区不够
; 时 ReadRegStr 会截断,后面再写回去就把 PATH 截短了(丢用户数据)。这里在 include
; 之前把它抬到 8192 —— 必须放在所有 include 之前,NSIS 才认。
!undef NSIS_MAX_STRLEN
!define NSIS_MAX_STRLEN 8192

Unicode true

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "WinMessages.nsh"
!include "x64.nsh"

!ifndef VERSION
  !error "缺少 VERSION:makensis 要带 -DVERSION=<tag 去掉 v>"
!endif
!ifndef SRCEXE
  !error "缺少 SRCEXE:makensis 要带 -DSRCEXE=<未打包的 mpx.exe 路径>"
!endif
!ifndef OUTFILE
  !error "缺少 OUTFILE:makensis 要带 -DOUTFILE=<setup.exe 文件名>"
!endif

!define APPNAME "mpx"
!define PUBLISHER "RovenEverward"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\mpx"

Name "${APPNAME}"
Caption "${APPNAME} ${VERSION} 安装"
BrandingText "${APPNAME} ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\mpx"
InstallDirRegKey HKLM "${UNINST_KEY}" "InstallLocation"
RequestExecutionLevel admin
SetCompressor /SOLID lzma

; ==== PATH 读改写用的全局变量(NSIS 的函数没有局部变量,只能用全局的) ====
Var PathValue      ; 被搜索/被替换的整串(两侧补 ';')
Var PathNeedle     ; 要找的子串,形如 ";<目录>;"
Var PathReplacement
Var PathResult
Var PathIndex
Var PathNeedleLen
Var PathSlice
Var PathSliceLen
Var PathFound
Var PathTmp
Var PathLen

!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ; 发布件是 amd64 二进制,32 位 Windows 上跑不了。
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "${APPNAME} 只提供 64 位 Windows 安装包,当前系统不是 64 位。"
    Abort
  ${EndIf}
FunctionEnd

; 在 $PathValue 里找 $PathNeedle(StrCmp 大小写不敏感),命中则 $PathFound=1。
Function PathContains
  StrCpy $PathFound 0
  StrLen $PathNeedleLen $PathNeedle
  StrCpy $PathIndex 0
  ${If} $PathNeedleLen == 0
    StrCpy $PathFound 1
    Return
  ${EndIf}
  PathContains_loop:
    StrCpy $PathSlice $PathValue $PathNeedleLen $PathIndex
    StrLen $PathSliceLen $PathSlice
    ${If} $PathSliceLen < $PathNeedleLen
      Return
    ${EndIf}
    StrCmp $PathSlice $PathNeedle 0 PathContains_next
      StrCpy $PathFound 1
      Return
    PathContains_next:
      IntOp $PathIndex $PathIndex + 1
      Goto PathContains_loop
FunctionEnd

; 下面两个函数只被卸载段调用。NSIS 要求卸载段能 Call 的函数名以 un. 开头,所以名
; 字带 un. 前缀(前缀不等于「自动属于卸载器」,定义与调用都要显式写)。
; 把 $PathValue 里所有 $PathNeedle 换成 $PathReplacement,结果放 $PathResult。
Function un.PathRemoveAll
  StrCpy $PathResult ""
  StrLen $PathNeedleLen $PathNeedle
  StrCpy $PathIndex 0
  ${If} $PathNeedleLen == 0
    StrCpy $PathResult $PathValue
    Return
  ${EndIf}
  un.PathRemove_loop:
    StrCpy $PathSlice $PathValue $PathNeedleLen $PathIndex
    StrLen $PathSliceLen $PathSlice
    ${If} $PathSliceLen < $PathNeedleLen
      StrCpy $PathSlice $PathValue 8192 $PathIndex
      StrCpy $PathResult "$PathResult$PathSlice"
      Return
    ${EndIf}
    StrCmp $PathSlice $PathNeedle 0 un.PathRemove_next
      StrCpy $PathResult "$PathResult$PathReplacement"
      IntOp $PathIndex $PathIndex + $PathNeedleLen
      Goto un.PathRemove_loop
    un.PathRemove_next:
      StrCpy $PathSlice $PathValue 1 $PathIndex
      StrCpy $PathResult "$PathResult$PathSlice"
      IntOp $PathIndex $PathIndex + 1
      Goto un.PathRemove_loop
FunctionEnd

; 去掉 $PathValue 两侧各一个字符(补上的 ';'),结果放 $PathTmp。空串则给空。
Function un.PathUnwrap
  StrLen $PathLen $PathValue
  ${If} $PathLen <= 2
    StrCpy $PathTmp ""
  ${Else}
    IntOp $PathLen $PathLen - 2
    StrCpy $PathTmp $PathValue $PathLen 1
  ${EndIf}
FunctionEnd

Section "Install"
  SetOutPath "$INSTDIR"
  File "${SRCEXE}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; ==== 当前用户 PATH:没有 $INSTDIR 这一项才追加,然后广播 WM_SETTINGCHANGE ====
  ReadRegStr $PathValue HKCU "Environment" "Path"
  StrCpy $PathTmp "$PathValue"                 ; 原值先留一份
  StrCpy $PathValue ";$PathTmp;"               ; 两侧补 ';' 后按整项匹配
  StrCpy $PathNeedle ";$INSTDIR;"
  Call PathContains
  ${If} $PathFound == 0
    ${If} $PathTmp == ""
      StrCpy $PathTmp "$INSTDIR"
    ${Else}
      StrCpy $PathTmp "$PathTmp;$INSTDIR"
    ${EndIf}
    WriteRegExpandStr HKCU "Environment" "Path" "$PathTmp"
    ; 广播:让已经开着的进程(资源管理器、终端)重新读环境变量。/TIMEOUT 防
    ; 某个窗口不响应时把安装卡住。
    SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
  ${EndIf}

  ; ==== 「程序和功能」登记(64 位视图) ====
  SetRegView 64
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\mpx.exe"
  WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
  SetRegView 32
SectionEnd

Section "Uninstall"
  ; ==== 从当前用户 PATH 里移除安装目录 ====
  ReadRegStr $PathValue HKCU "Environment" "Path"
  StrCpy $PathValue ";$PathValue;"             ; 两侧补 ';'
  StrCpy $PathNeedle ";$INSTDIR;"
  StrCpy $PathReplacement ";"
  Call un.PathRemoveAll
  StrCpy $PathValue $PathResult
  Call un.PathUnwrap                          ; -> $PathTmp
  WriteRegExpandStr HKCU "Environment" "Path" "$PathTmp"
  SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000

  Delete "$INSTDIR\mpx.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  SetRegView 64
  DeleteRegKey HKLM "${UNINST_KEY}"
  SetRegView 32
SectionEnd
