package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gonzalop/bambulan/internal/bambu3mf"
)

func (s *WebServer) fetchMetadataAsync(session *Session, filename string) {
	if !session.tryStartFetch(filename) {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		defer session.finishFetch(filename)

		slog.Info("Async fetching metadata for", "file", filename)

		tmpFile, size, tryFile, err := s.downloadMetadataWithRetry(ctx, session, filename)
		if err != nil {
			slog.Error("Failed to download metadata after retry", "file", filename, "error", err)
			if count := session.recordFetchFailure(filename); count >= 3 {
				slog.Debug("Skipping metadata fetch, max retries reached", "file", filename, "count", count)
			}
			return
		}

		// Cleanup logic for successful/parsed file at end of scope
		defer func() {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
		}()

		rdr, err := bambu3mf.NewReader(tmpFile, size)
		if err != nil {
			slog.Warn("Failed to open 3mf reader", "error", err)
			return
		}
		rdr.MaxFileSize = s.maxFileSize
		defer rdr.Close()

		md, err := rdr.ParseMetadata()
		if err != nil {
			slog.Warn("Failed to parse metadata", "error", err)
			return
		}

		s.updateSessionMetadata(session, filename, tryFile, md, rdr)
		slog.Debug("Successfully fetched metadata", "file", tryFile)
	}()
}

func (s *WebServer) handleAPIThumbnail(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getSession(w, r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "Missing key", http.StatusBadRequest)
		return
	}

	session.mu.RLock()
	data, exists := session.thumbnailCache[key]
	session.mu.RUnlock()
	if !exists {
		http.Error(w, "Thumbnail not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if _, err := w.Write(data); err != nil {
		slog.Error("Failed to write thumbnail", "error", err)
	}
}

func (s *WebServer) processUploaded3MF(session *Session, file multipart.File, header *multipart.FileHeader) {
	if filepath.Ext(header.Filename) != ".3mf" {
		return
	}

	ra, ok := file.(io.ReaderAt)
	if !ok {
		return
	}

	rdr, err := bambu3mf.NewReader(ra, header.Size)
	if err != nil {
		slog.Warn("Failed to create 3MF reader", "error", err)
		return
	}
	rdr.MaxFileSize = s.maxFileSize
	defer rdr.Close()

	md, err := rdr.ParseMetadata()
	if err != nil {
		slog.Warn("Failed to parse 3MF metadata", "error", err)
		return
	}

	slog.Info("Parsed 3MF metadata", "file", header.Filename, "plates", len(md.Plates))

	s.updateSessionMetadata(session, header.Filename, header.Filename, md, rdr)
	session.mu.Lock()
	session.currentMetadata = md
	session.currentMetadataFilename = header.Filename
	session.mu.Unlock()
}

// metadataCandidates returns the remote paths to try when fetching 3MF metadata
// for a print job name reported in printer status.
func metadataCandidates(filename string) []string {
	var names []string
	// The printer often reports "_foo.3mf" when the actual file is "foo.3mf".
	if after, ok := strings.CutPrefix(filename, "_"); ok {
		names = append(names, after)
	}
	names = append(names, filename)
	// Job names without an extension: .gcode.3mf is the most likely for print jobs.
	if filepath.Ext(filename) == "" {
		names = append(names, filename+".gcode.3mf", filename+".3mf")
	}
	return names
}

func (s *WebServer) downloadMetadataWithRetry(ctx context.Context, session *Session, filename string) (*os.File, int64, string, error) {
	var lastErr error
	for _, name := range metadataCandidates(filename) {
		t, sz, err := s.downloadToTemp(ctx, session, name)
		if err == nil {
			return t, sz, name, nil
		}
		slog.Warn("Metadata download failed", "file", name, "error", err)
		lastErr = err
	}
	return nil, 0, "", lastErr
}

func (s *WebServer) downloadToTemp(ctx context.Context, session *Session, target string) (*os.File, int64, error) {
	rc, err := session.client.File.Download(ctx, target)
	if err != nil {
		return nil, 0, err
	}
	defer rc.Close()

	tFile, err := os.CreateTemp("", "bambulan_meta_*.3mf")
	if err != nil {
		return nil, 0, err
	}

	success := false
	defer func() {
		if !success {
			os.Remove(tFile.Name())
			tFile.Close()
		}
	}()

	session.mu.Lock()
	session.metadataDownloadStatus = MetadataDownloadStatus{
		Downloading: true,
		Filename:    target,
	}
	session.mu.Unlock()

	// Create a progress tracking writer
	var written int64
	pw := &progressWriter{
		writer: tFile,
		onProgress: func(n int64) {
			written += n
			// We don't know total size for FTP, so just show bytes downloaded
			session.setMetadataDownloadProgress(written, 0)
		},
	}

	sz, err := io.Copy(pw, rc)
	if err != nil {
		return nil, 0, err
	}

	session.setMetadataDownloadProgress(sz, sz)

	if _, err := tFile.Seek(0, 0); err != nil {
		return nil, 0, err
	}

	success = true
	return tFile, sz, nil
}

// progressWriter wraps an io.Writer and calls onProgress after each write.
type progressWriter struct {
	writer     io.Writer
	onProgress func(int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	if n > 0 && pw.onProgress != nil {
		pw.onProgress(int64(n))
	}
	return n, err
}

func (s *WebServer) updateSessionMetadata(session *Session, filename string, tryFile string, md *bambu3mf.Metadata, rdr *bambu3mf.Reader) {
	session.mu.Lock()
	defer session.mu.Unlock()

	if session.metadataCache == nil {
		session.metadataCache = make(map[string]*bambu3mf.Metadata)
	}
	if session.thumbnailCache == nil {
		session.thumbnailCache = make(map[string][]byte)
	}

	session.metadataCache[filename] = md
	if tryFile != filename {
		session.metadataCache[tryFile] = md
	}

	// Helper to strip leading slash for comparison
	stripSlash := func(s string) string {
		return strings.TrimPrefix(s, "/")
	}

	if stripSlash(session.status.GcodeFile) == filename || stripSlash(session.status.SubtaskName) == filename {
		session.currentMetadata = md
		session.currentMetadataFilename = tryFile
	}

	for _, p := range md.Plates {
		if p.ThumbnailSmall != "" {
			if small, err := rdr.ReadFile(p.ThumbnailSmall); err == nil {
				key := fmt.Sprintf("%s/%d_small", filename, p.ID)
				session.thumbnailCache[key] = small
				if tryFile != filename {
					key2 := fmt.Sprintf("%s/%d_small", tryFile, p.ID)
					session.thumbnailCache[key2] = small
				}
			}
		}
		if p.ThumbnailPath != "" {
			if large, err := rdr.ReadFile(p.ThumbnailPath); err == nil {
				key := fmt.Sprintf("%s/%d", filename, p.ID)
				session.thumbnailCache[key] = large
				if tryFile != filename {
					key2 := fmt.Sprintf("%s/%d", tryFile, p.ID)
					session.thumbnailCache[key2] = large
				}
			}
		}
	}
}
