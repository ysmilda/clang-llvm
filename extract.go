package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Songmu/retry"
	"github.com/google/go-github/v63/github"
	"github.com/mholt/archiver/v4"
)

const (
	llvmOwner = "llvm"
	llvmRepo  = "llvm-project"
)

var (
	path = flag.String("path", "./archives", "The path to the file to extract.")

	token = os.Getenv("GITHUB_TOKEN")
	owner = os.Getenv("GITHUB_REPOSITORY_OWNER")
	repo  = os.Getenv("GITHUB_REPOSITORY")[len(owner)+1:]
	tag   = os.Getenv("GITHUB_REF_NAME")
)

func main() {
	flag.Parse()

	fmt.Printf("Owner: %s, Repo: %s, Tag: %s, Token: %s\n", owner, repo, tag, token)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Create a temporary directory to store the extracted files.
	dir, err := os.MkdirTemp(".", "*")
	if err != nil && !os.IsExist(err) {
		fmt.Printf("Failed to create release directory: %s\n", err)
		os.Exit(1)
	}

	// Creating two clients as the GitHub Action token doesn't have access to other repositories.
	llvmClient := github.NewClient(nil)
	mirrorClient := github.NewClient(nil).WithAuthToken(token)

	// Get the llvm release.
	llvmRelease, _, err := llvmClient.Repositories.GetReleaseByTag(ctx, llvmOwner, llvmRepo, tag)
	if err != nil {
		fmt.Printf("Failed to get llvm release: %s\n", err)
		os.Exit(1)
	}

	// Create the mirror release, if it already exists append to it. This way we can update the release as the LLVM release expands.
	var mirrorRelease *github.RepositoryRelease
	mirrorRelease, _, err = mirrorClient.Repositories.CreateRelease(ctx, owner, repo, &github.RepositoryRelease{
		TagName: &tag,
		Name:    llvmRelease.Name,
		Body:    llvmRelease.Body,
	})
	if err != nil && strings.Contains(err.Error(), "already_exists") {
		mirrorRelease, _, err = mirrorClient.Repositories.GetReleaseByTag(ctx, owner, repo, tag)
		if err != nil {
			fmt.Printf("Failed to get mirror release: %s\n", err)
			os.Exit(1)
		}
	} else if err != nil {
		fmt.Printf("Failed to create mirror release: %s\n", err)
		os.Exit(1)
	}

	// Download every archive asset into memory, extract it and upload the binaries to the mirror release.
	for _, asset := range llvmRelease.Assets {
		name := asset.GetName()
		if !strings.HasPrefix(name, "clang+llvm") || !(strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.xz")) {
			continue
		}

		fmt.Println("Downloading", name)

		archive, _, err := llvmClient.Repositories.DownloadReleaseAsset(ctx, llvmOwner, llvmRepo, asset.GetID(), http.DefaultClient)
		if err != nil {
			fmt.Printf("Failed to download asset: %s\n", err)
			os.Exit(1)
		}

		extension := name[len(name)-7:]
		fileName := name[:len(name)-len(extension)]
		archVersion := strings.TrimPrefix(fileName, "clang+llvm-")

		fmt.Printf("Extension: %s, FileName: %s, ArchVersion: %s\n", extension, fileName, archVersion)

		// Decompress the file according to the file extension.
		var decompressor io.ReadCloser
		switch extension {
		case ".tar.gz":
			decompressor, err = archiver.Gz{}.OpenReader(archive)
		case ".tar.xz":
			decompressor, err = archiver.Xz{}.OpenReader(archive)
		default:
			fmt.Printf("Unknown extension: %s\n", extension)
			os.Exit(1)
		}

		// We only need the bin directory from the archive. The structure of the archive is as follows:
		// - Dir matching fileName
		//   - bin
		fileList := []string{fileName + "/bin"}

		// Any file we extract that is not a directory or symlink should be written to disk so we can upload it.
		handler := func(ctx context.Context, af archiver.File) error {
			// Skip the bin folder itself.
			if af.IsDir() {
				return nil
			}

			// Skip files without size e.g. symlinks.
			if af.Size() == 0 {
				return nil
			}

			// For now we limit the release to clang and llvm binaries.
			if !strings.HasPrefix(af.Name(), "clang") && !strings.HasPrefix(af.Name(), "llvm") {
				return nil
			}

			fmt.Println("\tExtracting", af.Name())

			name := af.Name()
			extension := filepath.Ext(name)
			fileName := name[:len(name)-len(extension)]

			// Create a new file to write the extracted file to.
			// By doing this is will be easier to upload using the GitHub library, however it is not fully in memory anymore.
			tempFile, err := os.CreateTemp(dir, "*")
			if err != nil {
				return fmt.Errorf("Failed to create file: %s", err)
			}
			defer func() {
				tempFile.Close()
				os.Remove(tempFile.Name())
			}()

			// Open the file from the archive.
			archiveFile, err := af.Open()
			if err != nil {
				return fmt.Errorf("Failed to open file from archive: %s", err)
			}
			defer archiveFile.Close()

			// Copy the file from the archive to the new file.
			_, err = io.Copy(tempFile, archiveFile)
			if err != nil {
				return fmt.Errorf("Failed to copy file from archive to disk: %s", err)
			}

			// This needs to be retried as you would otherwise run into the following error: https://github.com/google/go-github/issues/2113
			return retry.Retry(3, 3*time.Second, func() error {
				// Open the file ones more to upload it to the mirror release.
				f, err := os.Open(tempFile.Name())
				if err != nil {
					return fmt.Errorf("Failed to open file: %s", err)
				}
				defer f.Close()

				// Upload the file to the mirror release.
				_, _, err = mirrorClient.Repositories.UploadReleaseAsset(ctx, owner, repo, *mirrorRelease.ID, &github.UploadOptions{
					Name: fmt.Sprintf("%s-%s%s", fileName, archVersion, extension),
				}, f)
				if err != nil && strings.Contains(err.Error(), "already_exists") {
					// For now we ignore the error as we don't want to overwrite existing assets.
					fmt.Printf("\tAsset already exists\n")
					return nil
				} else if err != nil {
					return fmt.Errorf("Failed to upload asset: %s", err)
				}
				return nil
			})
		}

		fmt.Printf("Extracting %s\n", fileName)
		// Extract the files from the archive.
		format := archiver.Tar{}
		err = format.Extract(ctx, decompressor, fileList, handler)
		if err != nil {
			fmt.Printf("Failed to extract archive: %s\n", err)
			os.Exit(1)
		}
	}
}
