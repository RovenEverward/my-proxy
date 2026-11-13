; NSIS 安装脚本:MPX 桌面版,按当前用户装到 $LOCALAPPDATA\Programs\MPX。
; 由 .github/workflows/release.yml 的 desktop-windows job 调用:
;
;   makensis -INPUTCHARSET UTF8 -DVERSION=0.7.2 \
;     -DSRCEXE="$(pwd)/mpx.exe" -DICONFILE="$(pwd)/deploy/desktop/icon.ico" \
;     -DOUTFILE="$(pwd)/mpx-desktop_0.7.2_windows_amd64_setup.exe" \
;     deploy/windows/mpx-desktop.nsi
;
; 发布前 publish job 会把它改名为 MPX-<ver>-Windows-x64-setup.exe(另有固定名副本)。
;
; 与命令行版的 mpx.nsi 刻意分开:
;   - 装的是桌面二进制,改名 MPX.exe(无参数启动就开窗口;带子命令时照 CLI
;     工作,例如 setup 调用的 winpath);
;   - RequestExecutionLevel user,只写 HKCU,不需要管理员;
;   - 装完把安装目录加进当前用户 PATH(卸载时移除),让 mpx 在终端里可用。
;     入口仍是开始菜单和桌面快捷方式。
; 不签名。只用 NSIS 自带的东西(MUI2 / LogicLib),不依赖额外插件。
;
; 用户 PATH 不在 NSIS 里读改写,交给刚装好的 `MPX.exe winpath add|remove`:
; 原因与 mpx.nsi 相同 —— NSIS 读 PATH 超过编译定死的缓冲区(默认 1024 字)
; 会拿到空串,读写回写都会弄坏 PATH;MPX.exe winpath 没有长度上限,写前备份、
; 写完读回核对,任何一步出错 PATH 一字不改。见 mpx.nsi 文件头与 ISS-02。

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

  ; ==== 当前用户 PATH:交给 MPX.exe winpath(见文件头)。失败时 PATH 一字不改,
  ; 提示手动加 ====
  nsExec::ExecToLog '"$INSTDIR\${EXENAME}" winpath add "$INSTDIR"'
  Pop $0
  ${If} $0 != 0
    DetailPrint "没有改动用户 PATH(退出码 $0)。请手动把 $INSTDIR 加进用户环境变量 Path。"
    ${IfNot} ${Silent}
      MessageBox MB_ICONEXCLAMATION "没能把 $INSTDIR 加进用户 PATH,你的 PATH 没有被改动。$\r$\n请在「编辑账户的环境变量」里手动把它加进 Path。"
    ${EndIf}
  ${EndIf}

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

  ; ==== 从当前用户 PATH 里移除安装目录:同样交给 MPX.exe winpath,失败就不动 ====
  nsExec::ExecToLog '"$INSTDIR\${EXENAME}" winpath remove "$INSTDIR"'
  Pop $0
  ${If} $0 != 0
    DetailPrint "没有改动用户 PATH(退出码 $0)。如果 Path 里还有 $INSTDIR,可以手动删掉。"
  ${EndIf}

  Delete "$SMPROGRAMS\MPX.lnk"
  Delete "$DESKTOP\MPX.lnk"

  Delete "$INSTDIR\${EXENAME}"
  Delete "$INSTDIR\MPX.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
