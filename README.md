# Clang-LLVM

The purpose of this repository is to act as a "mirror" for the releases of the [LLVM Project](https://github.com/llvm/llvm-project) with the notable difference that it exposes the different clang and llvm binaries as seperate files instead of a combined archive. The creation of a release is a manual process as the release archives are not always directly present when a release is made. This repository is updated on a best-effort basis.

This repository is not affiliated with the LLVM Project in any way.

## Fork

If you want to fork this repository and create your own mirror, you need to make sure the following requirements are met:
 - The repository has a self hosted runner available. (see repository settings > Actions > Add runner)
 - The `GITHUB_TOKEN` has read and write permissions to the repository. (see repository settings > Actions > Generatl > Workflow permissions)


