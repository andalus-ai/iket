$ErrorActionPreference = 'Stop'

$packageName = 'iket'
$url64 = 'https://github.com/bhangun/iket/releases/latest/download/iket-windows-amd64.exe'
$checksum64 = ''  # Will be filled in during packaging

$toolsDir = "$(Split-Path -parent $MyInvocation.MyCommand.Definition)"

$packageArgs = @{
  packageName    = $packageName
  fileType       = 'exe'
  url64bit       = $url64
  checksum64     = $checksum64
  checksumType64 = 'sha256'
  silentArgs     = '/S'
  validExitCodes = @(0)
}

Install-ChocolateyPackage @packageArgs