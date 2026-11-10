# Releasing

The [Build workflow](.github/workflows/build.yml) builds Linux x64 and Windows x64 on pushes to main and pull requests. A version tag runs the same build jobs with Windows installer packaging enabled. Once both jobs succeed, the workflow collects their artifacts into a draft release.

## Prepare a release

1. Update the version in [mygo.config.ts](mygo.config.ts) and add a section to [CHANGELOG.md](CHANGELOG.md).
2. Push the reviewed commit to your fork. Create and push the matching tag, such as v0.1.23 for version 0.1.23. To retry, rerun the workflow for that tag.
3. Open the draft in the fork's Releases page, check its notes and assets, then publish it.

The draft contains the Linux tarball and Debian package, the Windows NSIS installer, SHA256SUMS, and the installation scripts. Download the script from the published release you want, then run sh install.sh on Linux x64 or run install.ps1 from PowerShell on Windows x64. Each script downloads and verifies the package for that release. For offline Linux installation, download install-local.sh and its matching tarball, then pass the tarball to that script.

## Podcast caption libraries

The sherpa-onnx libraries have their own prerelease tags. See the [README](README.md) for that process.
