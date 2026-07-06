; WUT Installer Script for Inno Setup
; This script creates a Windows installer for WUT - AI-Powered Command Helper

#define MyAppName "WUT"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "Thirawat27"
#define MyAppURL "https://github.com/thirawat27/wut"
#define MyAppExeName "wut.exe"

[Setup]
; Application information
AppId={{8A5F3B2C-4D7E-4F8A-9B1C-3D5E6F7A8B9C}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}

; Default installation directory
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}

; Output settings
OutputDir=..\dist
OutputBaseFilename=wut-setup

; Compression
Compression=lzma
SolidCompression=yes

; Appearance
WizardStyle=modern
; SetupIconFile=..\assets\icon.ico

; Privileges (admin required for system-wide installation)
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog

; Metadata
VersionInfoVersion={#MyAppVersion}
VersionInfoCompany={#MyAppPublisher}
VersionInfoDescription={#MyAppName} - AI-Powered Command Helper
VersionInfoCopyright=Copyright (C) 2024 {#MyAppPublisher}

; Architecture (x64, x86, or both)
ArchitecturesAllowed=x64 x86
ArchitecturesInstallIn64BitMode=x64

; Uninstaller
UninstallDisplayIcon={app}\{#MyAppExeName}
UninstallDisplayName={#MyAppName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "addtopath"; Description: "Add to PATH environment variable"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
; Main executable - pulled from build/windows/
Source: "..\build\windows\{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion

; License file
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

; README
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
; Start Menu shortcuts
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"

; Desktop shortcut (optional)
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
; Bootstrap config + shell integration on first install so users do not need to run `wut install`
Filename: "{app}\{#MyAppExeName}"; Parameters: "init --quick"; Flags: runhidden waituntilterminated skipifdoesntexist
; Optional: Run application after installation
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#StringChange(MyAppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent

[Code]
// Function to add application directory to PATH
procedure EnvAddPath(Path: string);
var
  Paths: string;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Paths) then
    Paths := '';
  
  // Check if path already exists
  if Pos(';' + Path + ';', ';' + Paths + ';') > 0 then
    exit;
    
  // Add path
  Paths := Paths + ';' + Path;
  
  RegWriteStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Paths);
end;

// Function to remove application directory from PATH
procedure EnvRemovePath(Path: string);
var
  Paths: string;
  P: Integer;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Paths) then
    exit;
    
  // Find and remove path
  P := Pos(';' + Path + ';', ';' + Paths + ';');
  if P > 0 then
  begin
    Delete(Paths, P - 1, Length(Path) + 1);
    RegWriteStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Paths);
  end;
end;

// Called after installation is complete
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    if WizardIsTaskSelected('addtopath') then
    begin
      EnvAddPath(ExpandConstant('{app}'));
    end;
  end;
end;

// Called before uninstallation
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
  begin
    EnvRemovePath(ExpandConstant('{app}'));
  end;
end;
