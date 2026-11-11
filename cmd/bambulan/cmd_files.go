package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// FileCmd groups file management subcommands (ls, download, download-dir, upload, rm, mkdir, mv).
type FileCmd struct {
	Ls          FileLsCmd          `cmd:"" help:"List files"`
	Download    FileDownloadCmd    `cmd:"" help:"Download file"`
	DownloadDir FileDownloadDirCmd `cmd:"" help:"Download directory"`
	Upload      FileUploadCmd      `cmd:"" help:"Upload file"`
	Rm          FileRmCmd          `cmd:"" help:"Remove file or directory"`
	Mkdir       FileMkdirCmd       `cmd:"" help:"Make directory"`
	Mv          FileMvCmd          `cmd:"" help:"Move/Rename file"`
}

// FileLsCmd represents the CLI command to list files in a remote directory.
type FileLsCmd struct {
	Path      string `arg:"" optional:"" default:"/" help:"Directory to list"`
	Extension string `help:"Filter by extension (e.g. .3mf)" short:"e"`
}

// Run executes the file ls command.
func (c *FileLsCmd) Run(ctx *Context) error {
	fmt.Printf("Listing files in %s...\n", c.Path)
	if c.Extension != "" {
		files, err := ctx.Client.File.GetFiles(context.Background(), c.Path, c.Extension)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Println(f)
		}
	} else {
		files, err := ctx.Client.File.ListFiles(context.Background(), c.Path)
		if err != nil {
			return err
		}
		for _, f := range files {
			t := "FILE"
			if f.Type == "dir" { // Directory
				t = "DIR "
			}
			fmt.Printf("%s %-10d %s\n", t, f.Size, f.Name)
		}
	}
	return nil
}

// FileDownloadCmd represents the CLI command to download a remote file from the printer.
type FileDownloadCmd struct {
	Remote string `arg:"" help:"Remote file path"`
	Local  string `arg:"" optional:"" help:"Local destination path"`
}

// Run executes the file download command.
func (c *FileDownloadCmd) Run(ctx *Context) error {
	local := c.Local
	if local == "" {
		local = filepath.Base(c.Remote)
	}

	fmt.Printf("Downloading %s to %s...\n", c.Remote, local)
	start := time.Now()

	progressFunc := func(current, total int64) {
		if total > 0 {
			percent := float64(current) / float64(total) * 100
			fmt.Printf("\rDownload: %.1f%% (%d/%d bytes)", percent, current, total)
		} else {
			fmt.Printf("\rDownload: %d bytes (unknown total)", current)
		}
	}

	if err := ctx.Client.File.DownloadFile(context.Background(), c.Remote, local, progressFunc); err != nil {
		fmt.Println()
		return err
	}
	fmt.Printf("\nDownloaded in %v\n", time.Since(start))
	return nil
}

// FileDownloadDirCmd represents the CLI command to download a remote directory from the printer.
type FileDownloadDirCmd struct {
	Remote    string `arg:"" help:"Remote directory path"`
	Local     string `arg:"" help:"Local destination directory path"`
	Recursive bool   `help:"Recursive download" short:"r"`
}

// Run executes the file download-dir command.
func (c *FileDownloadDirCmd) Run(ctx *Context) error {
	fmt.Printf("Downloading directory %s to %s (recursive: %v)...\n", c.Remote, c.Local, c.Recursive)
	start := time.Now()

	progressFunc := func(filename string, current, total int64) {
		if total > 0 {
			percent := float64(current) / float64(total) * 100
			fmt.Printf("\rDownload [%s]: %.1f%% (%d/%d bytes)          ", filename, percent, current, total)
		} else {
			fmt.Printf("\rDownload [%s]: %d bytes (unknown total)          ", filename, current)
		}
	}

	if err := ctx.Client.File.DownloadDirectory(context.Background(), c.Remote, c.Local, c.Recursive, progressFunc); err != nil {
		fmt.Println()
		return err
	}
	fmt.Printf("\nDownloaded in %v\n", time.Since(start))
	return nil
}

// FileUploadCmd represents the CLI command to upload a local file to the printer.
type FileUploadCmd struct {
	Local  string `arg:"" help:"Local file path"`
	Remote string `arg:"" optional:"" help:"Remote destination path"`
}

// Run executes the file upload command.
func (c *FileUploadCmd) Run(ctx *Context) error {
	remote := c.Remote
	if remote == "" {
		remote = "/" + filepath.Base(c.Local)
	}

	fmt.Printf("Uploading %s to %s...\n", c.Local, remote)
	start := time.Now()

	progressFunc := func(current, total int64) {
		if total > 0 {
			percent := float64(current) / float64(total) * 100
			fmt.Printf("\rUpload: %.1f%% (%d/%d bytes)", percent, current, total)
		} else {
			fmt.Printf("\rUpload: %d bytes", current)
		}
	}

	if err := ctx.Client.File.UploadFile(context.Background(), c.Local, remote, progressFunc); err != nil {
		fmt.Println()
		return err
	}
	fmt.Printf("\nUploaded in %v\n", time.Since(start))
	return nil
}

// FileRmCmd represents the CLI command to remove a remote file or directory.
type FileRmCmd struct {
	Path      string `arg:"" help:"Path to remove"`
	Recursive bool   `help:"Recursive delete" short:"r"`
}

// Run executes the file rm command.
func (c *FileRmCmd) Run(ctx *Context) error {
	if c.Recursive {
		fmt.Printf("Recursively removing %s...\n", c.Path)
		if err := ctx.Client.File.RemoveAll(context.Background(), c.Path); err != nil {
			return err
		}
	} else {
		fmt.Printf("Removing file %s...\n", c.Path)
		if err := ctx.Client.File.Delete(context.Background(), c.Path); err != nil {
			return err
		}
	}
	fmt.Println("Done.")
	return nil
}

// FileMkdirCmd represents the CLI command to create a remote directory on the printer.
type FileMkdirCmd struct {
	Path string `arg:"" help:"Directory path to create"`
}

// Run executes the file mkdir command.
func (c *FileMkdirCmd) Run(ctx *Context) error {
	fmt.Printf("Creating directory %s...\n", c.Path)
	if err := ctx.Client.File.MakeDirectory(context.Background(), c.Path); err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}

// FileMvCmd represents the CLI command to move or rename a remote file or directory.
type FileMvCmd struct {
	Source string `arg:"" help:"Source path"`
	Dest   string `arg:"" help:"Destination path"`
}

// Run executes the file mv command.
func (c *FileMvCmd) Run(ctx *Context) error {
	fmt.Printf("Moving %s to %s...\n", c.Source, c.Dest)
	if err := ctx.Client.File.Rename(context.Background(), c.Source, c.Dest); err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}
