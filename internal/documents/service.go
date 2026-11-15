package documents

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	AddLink(ctx context.Context, q db.Querier, partID int64, url, label string) error
	DeleteLink(ctx context.Context, id int64) error
	UploadDocument(ctx context.Context, q db.Querier, partID int64, file io.Reader, filename string) (string, error)
	DeleteDocument(ctx context.Context, id int64) error
}

type service struct {
	store  db.Store
	logger *slog.Logger
}

func NewService(store db.Store, logger *slog.Logger) Service {
	return &service{
		store:  store,
		logger: logger,
	}
}

// ValidateLinkURL verifies that a part link uses http/https with a non-empty host
// and returns the trimmed URL string.
func ValidateLinkURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("invalid link URL: empty URL")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid link URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid link URL: must use http or https with a valid host")
	}
	return trimmed, nil
}

func (s *service) AddLink(ctx context.Context, q db.Querier, partID int64, rawURL, label string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}
	validURL, err := ValidateLinkURL(trimmed)
	if err != nil {
		return err
	}
	s.logger.Debug("adding part link", "part_id", partID, "url", validURL)
	return q.CreatePartLink(ctx, db.CreatePartLinkParams{
		PartID: partID,
		Url:    validURL,
		Label:  sql.NullString{String: label, Valid: label != ""},
	})
}

func (s *service) DeleteLink(ctx context.Context, id int64) error {
	return s.store.DeletePartLink(ctx, id)
}

func (s *service) UploadDocument(ctx context.Context, q db.Querier, partID int64, file io.Reader, filename string) (string, error) {
	s.logger.Debug("saving part document", "part_id", partID, "filename", filename)
	savedWebPath, err := s.saveDocument(file, filename)
	if err != nil {
		return "", fmt.Errorf("failed to save document %s: %w", filename, err)
	}

	err = q.CreatePartDoc(ctx, db.CreatePartDocParams{
		PartID:   partID,
		FilePath: savedWebPath,
		FileName: filename,
	})
	if err != nil {
		s.deleteFile(savedWebPath)
		return "", fmt.Errorf("failed to create doc record: %w", err)
	}

	return savedWebPath, nil
}

func (s *service) DeleteDocument(ctx context.Context, id int64) error {
	doc, err := s.store.GetPartDoc(ctx, id)
	if err == nil {
		s.deleteFile(doc.FilePath)
	}
	return s.store.DeletePartDoc(ctx, id)
}

var allowedDocMimeTypes = map[string]string{
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".md":   "text/plain",
	".csv":  "text/plain",
	".json": "text/plain",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

func validateDocumentUpload(src io.Reader, filename string) (string, []byte, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	expectedMime, ok := allowedDocMimeTypes[ext]
	if !ok {
		return "", nil, fmt.Errorf("unsupported document extension %q", ext)
	}

	sniffBuf := make([]byte, 512)
	n, err := io.ReadFull(src, sniffBuf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", nil, fmt.Errorf("failed to read document header: %w", err)
	}
	head := sniffBuf[:n]

	detected := http.DetectContentType(head)
	mediaType, _, parseErr := mime.ParseMediaType(detected)
	if parseErr != nil {
		mediaType = strings.TrimSpace(strings.SplitN(detected, ";", 2)[0])
	}
	if mediaType != expectedMime {
		return "", nil, fmt.Errorf("document content type %q does not match extension %q", mediaType, ext)
	}

	return ext, head, nil
}

func (s *service) saveDocument(src io.Reader, filename string) (string, error) {
	ext, head, err := validateDocumentUpload(src, filename)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(config.DirUploadsDocs, 0755); err != nil {
		return "", err
	}

	rawExt := filepath.Ext(filename)
	base := strings.TrimSuffix(filepath.Base(filename), rawExt)
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, base)
	if base == "" {
		base = "document"
	}

	cleanName := fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext)
	destPath := filepath.Join(config.DirUploadsDocs, cleanName)

	dst, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if len(head) > 0 {
		if _, err := dst.Write(head); err != nil {
			return "", err
		}
	}

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}

	return config.UrlPrefixDocs + cleanName, nil
}

func (s *service) deleteFile(webPath string) {
	if strings.HasPrefix(webPath, config.UrlPrefixUploads) {
		relPath := strings.TrimPrefix(webPath, config.UrlPrefixUploads)
		diskPath := filepath.Join(config.DirUploads, relPath)
		err := os.Remove(diskPath)
		if err != nil {
			s.logger.Warn("failed to delete doc file", "path", diskPath, "err", err)
		}
	}
}
