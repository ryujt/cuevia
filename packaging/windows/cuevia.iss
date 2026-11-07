#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef PayloadDir
  #error PayloadDir is required
#endif
#ifndef ReleaseDir
  #error ReleaseDir is required
#endif
#ifndef InstallerAppId
  #define InstallerAppId "{{B22463BB-4483-46F5-836A-BA7A915D73B2}"
#endif
#ifndef InstallerGroup
  #define InstallerGroup "Cuevia"
#endif
#ifndef InstallerMutex
  #define InstallerMutex "Local\Cuevia.WindowsPreview"
#endif

[Setup]
AppId={#InstallerAppId}
AppName=Cuevia
AppVersion={#AppVersion}
AppPublisher=tools.camp
AppPublisherURL=https://tools.camp
DefaultDirName={localappdata}\Programs\Cuevia
DefaultGroupName={#InstallerGroup}
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763
AppMutex={#InstallerMutex}
SetupMutex=Local\Cuevia.WindowsPreview.Setup
UninstallDisplayIcon={app}\Cuevia.ico
SetupIconFile={#PayloadDir}\Cuevia.ico
OutputDir={#ReleaseDir}
OutputBaseFilename=Cuevia-{#AppVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
DisableProgramGroupPage=yes
InfoBeforeFile={#PayloadDir}\INSTALL.txt
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "korean"; MessagesFile: "compiler:Languages\Korean.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; Flags: unchecked

[Files]
Source: "{#PayloadDir}\cuevia.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\Cuevia-launcher.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\Cuevia.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\WindowsUI.cs"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\Cuevia.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\INSTALL.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\GO-LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Cuevia"; Filename: "{app}\Cuevia-launcher.exe"; WorkingDir: "{app}"; IconFilename: "{app}\Cuevia.ico"
Name: "{group}\Cuevia - Read me"; Filename: "{app}\INSTALL.txt"
Name: "{autodesktop}\Cuevia"; Filename: "{app}\Cuevia-launcher.exe"; WorkingDir: "{app}"; IconFilename: "{app}\Cuevia.ico"; Tasks: desktopicon

[Run]
Filename: "{app}\Cuevia-launcher.exe"; Description: "Launch Cuevia"; Flags: nowait postinstall skipifsilent runhidden
