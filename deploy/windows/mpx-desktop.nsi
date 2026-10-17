; NSIS 安装脚本:MPX 桌面版,按当前用户装到 $LOCALAPPDATA\Programs\MPX。
; 由 .github/workflows/release.yml 的 desktop-windows job 调用:
;
;   makensis -INPUTCHARSET UTF8 -DVERSION=0.7.2 \
;     -DSRCEXE="$(pwd)/mpx.exe" -DICONFILE="$(pwd)/deploy/desktop/icon.ico" \
;     -DOUTFILE="$(pwd)/MPX_0.7.2_windows_amd64_setup.exe" \
;     deploy/windows/mpx-desktop.nsi
;
; 与命令行版的 mpx.nsi 刻意分开:
;   - 装的是桌面二进制,改名 MPX.exe(无参数启动就开窗口);
;   - RequestExecutionLevel user,只写 HKCU,不需要管理员;
;   - 不改 PATH,入口是开始菜单和桌面快捷方式。
; 不签名。只用 NSIS 自带的东西(MUI2 / LogicLib),不依赖额外插件。

Unicode true

!include "MUI2.nsh"
!include "LogicLib.nsh"

!ifndef VERSION
  !error "缺少 VERSION:makensis 要带 -DVERSION=<tag 去掉 v>"
!endif
!ifndef SRCEXE
  !error "缺少 SRCEXE:makensis 要带 -DSRCEXE=<未打包的 mpx.exe 路径>"
!endif
!ifndef ICONFILE
  !error "缺少 ICONFILE:makensis 要带 -DICONFILE=<deploy/desktop/icon.ico 路径>"
!endif
!ifndef OUTFILE
  !error "缺少 OUTFILE:makensis 要带 -DOUTFILE=<setup.exe 文件名>"
!endif

!define APPNAME "MPX"
!define EXENAME "MPX.exe"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\MPX"

Name "${APPNAME}"
Caption "${APPNAME} ${VERSION} 安装"
BrandingText "${APPNAME} ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\MPX"
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation"
RequestExecutionLevel user
SetCompressor /SOLID lzma

; 安装器与快捷方式都用同一枚 icon.ico。快捷方式复制一份到安装目录,卸载时删掉。
!define MUI_ICON "${ICONFILE}"
!define MUI_UNICON "${ICONFILE}"

!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXENAME}"
!define MUI_FINISHPAGE_RUN_TEXT "运行 MPX"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  ; 按用户安装:快捷方式写当前用户的开始菜单与桌面,注册表只写 HKCU。
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  File "/oname=${EXENAME}" "${SRCEXE}"
  File "/oname=MPX.ico" "${ICONFILE}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; 开始菜单与桌面快捷方式,图标用刚复制进来的 MPX.ico。
  CreateShortCut "$SMPROGRAMS\MPX.lnk" "$INSTDIR\${EXENAME}" "" "$INSTDIR\MPX.ico" 0
  CreateShortCut "$DESKTOP\MPX.lnk" "$INSTDIR\${EXENAME}" "" "$INSTDIR\MPX.ico" 0

  ; 「设置 → 应用」里的卸载项,只在当前用户下(HKCU),不需要管理员。
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "RovenEverward"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\MPX.ico"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  Delete "$SMPROGRAMS\MPX.lnk"
  Delete "$DESKTOP\MPX.lnk"

  Delete "$INSTDIR\${EXENAME}"
  Delete "$INSTDIR\MPX.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
