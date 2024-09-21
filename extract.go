package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/mholt/archiver/v4"
)

var (
	path = flag.String("path", "./archives", "The path to the file to extract.")
)

func main() {
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := os.Mkdir("./release", os.ModePerm)
	if err != nil {
		fmt.Printf("Failed to create release directory: %s\n", err)
		os.Exit(1)
	}

	// Walk over all files in the specified path.
	filepath.WalkDir(*path, func(path string, d os.DirEntry, err error) error {
		if d.IsDir() {
			// Skip directories.
			return nil
		}

		name := d.Name()
		extension := name[len(name)-7:]
		fileName := name[:len(name)-len(extension)]
		archVersion := strings.TrimPrefix(fileName, "clang+llvm-")

		// Skip files that are not tar.gz or tar.xz files.
		if extension != ".tar.gz" && extension != ".tar.xz" {
			return nil
		}

		fmt.Println("Processing", name)

		// Open the file so we can decompress it.
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("Failed to open file: %s", err)
		}
		defer file.Close()

		// Decompress the file according to the file extension.
		var decompressor io.ReadCloser
		switch extension {
		case ".tar.gz":
			decompressor, err = archiver.Gz{}.OpenReader(file)
		case ".tar.xz":
			decompressor, err = archiver.Xz{}.OpenReader(file)
		}

		// We only need the bin directory from the archive. The structure of the archive is as follows:
		// - Dir matching fileName
		//   - bin
		fileList := []string{fileName + "/bin"}

		// Any file we extract that is not a directory or symlink should be written to disk so we can upload it.
		handler := func(ctx context.Context, af archiver.File) error {
			if af.IsDir() {
				// This is the bin folder itself, skip it.
				return nil
			}

			if af.Size() == 0 {
				// This is an empty file, skip it.
				return nil
			}

			if !strings.HasPrefix(af.Name(), "clang") && !strings.HasPrefix(af.Name(), "llvm") {
				// For now we limit the release to clang and llvm binaries.
				return nil
			}

			fmt.Println("\tExtracting", af.Name())

			name := af.Name()
			extension := filepath.Ext(name)
			fileName := name[:len(name)-len(extension)]

			// Create a new file to write the extracted file to.
			newFile, err := os.Create(fmt.Sprintf("./release/%s-%s%s", fileName, archVersion, extension))
			if err != nil {
				return fmt.Errorf("Failed to create file: %s", err)
			}
			defer newFile.Close()

			// Open the file from the archive.
			archiveFile, err := af.Open()
			if err != nil {
				return fmt.Errorf("Failed to open file from archive: %s", err)
			}
			defer archiveFile.Close()

			// Copy the file from the archive to the new file.
			_, err = io.Copy(newFile, archiveFile)
			if err != nil {
				return fmt.Errorf("Failed to copy file from archive to disk: %s", err)
			}

			return nil
		}

		// Extract the files from the archive.
		format := archiver.Tar{}
		err = format.Extract(ctx, decompressor, fileList, handler)

		return nil
	})
}
