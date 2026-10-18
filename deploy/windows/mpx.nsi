; NSIS 安装脚本:把 mpx.exe 装进 $PROGRAMFILES64\mpx,注册卸载项,并把该目录加进
; 当前用户的 PATH(卸载时移除)。由 .github/workflows/release.yml 的 pkg-windows
; job 调用:
;
;   makensis -INPUTCHARSET UTF8 -DVERSION=0.7.2 \
;     -DSRCEXE="$(pwd)/mpx.exe" -DOUTFILE="$(pwd)/mpx_0.7.2_windows_amd64_setup.exe" \
;     deploy/windows/mpx.nsi
;
; 不签名。只用 NSIS 自带的东西(MUI2 / LogicLib / x64 / nsExec)。
;
; 用户 PATH 不在 NSIS 里读改写,交给刚装好的 `mpx.exe winpath add|remove <目录>`:
; NSIS 的字符串缓冲区是编译 NSIS 时定死的(CI 用的 Ubuntu nsis 包是 1024 字符,
; 脚本里 !define NSIS_MAX_STRLEN 改不了它)。PATH 超过这个长度时 ReadRegStr 读失败
; 返回空串,旧脚本把空串当成没有 PATH,写回去只剩安装目录 —— v0.7.4 到 v0.9.11 的
; setup.exe 因此会清空或截断长 PATH。mpx winpath 没有长度上限,不展开 %VAR%、
; 保留 REG_EXPAND_SZ,只动我们那一项,写前备份,任何一步出错都不写。

Unicode true

!include "MUI2.nsh"
!include "LogicLib.nsh"
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

Section "Install"
  SetOutPath "$INSTDIR"
  File "${SRCEXE}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; ==== 当前用户 PATH:交给 mpx winpath(见文件头)。失败时 PATH 一字不改,提示手动加 ====
  nsExec::ExecToLog '"$INSTDIR\mpx.exe" winpath add "$INSTDIR"'
  Pop $0
  ${If} $0 != 0
    DetailPrint "没有改动用户 PATH(退出码 $0)。请手动把 $INSTDIR 加进用户环境变量 Path。"
    ${IfNot} ${Silent}
      MessageBox MB_ICONEXCLAMATION "没能把 $INSTDIR 加进用户 PATH,你的 PATH 没有被改动。$\r$\n请在「编辑账户的环境变量」里手动把它加进 Path。"
    ${EndIf}
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
  ; ==== 从当前用户 PATH 里移除安装目录:同样交给 mpx winpath,失败就不动 PATH ====
  nsExec::ExecToLog '"$INSTDIR\mpx.exe" winpath remove "$INSTDIR"'
  Pop $0
  ${If} $0 != 0
    DetailPrint "没有改动用户 PATH(退出码 $0)。如果 Path 里还有 $INSTDIR,可以手动删掉。"
  ${EndIf}

  Delete "$INSTDIR\mpx.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  SetRegView 64
  DeleteRegKey HKLM "${UNINST_KEY}"
  SetRegView 32
SectionEnd
