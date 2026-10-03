package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/pkg/errors"

	"github.com/usememos/memos/internal/identifier"
	"github.com/usememos/memos/store"
)

func attachmentOriginalPath(data, uid string) (string, error) {
	if !identifier.UIDMatcher.MatchString(uid) {
		return "", errors.New("invalid attachment uid")
	}
	return filepath.Join(data, "originals", uid), nil
}

// preserveAttachmentOriginal atomically retains the incoming bytes before any
// metadata stripping. Originals are private archival assets, never public file
// route variants. A failed upload cannot overwrite an existing UID's original.
func preserveAttachmentOriginal(ctx context.Context, data string, create *store.Attachment, source io.ReadSeeker) (string, error) {
	path, err := attachmentOriginalPath(data, create.UID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".original-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := io.Copy(file, &attachmentContextReader{ctx: ctx, reader: source}); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	// Link is an atomic no-replace install on the same filesystem.
	if err := os.Link(file.Name(), path); err != nil {
		return "", errors.Wrap(err, "failed to retain original file")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func originalFileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
