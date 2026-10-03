// Package journalbackup defines a portable owner-scoped snapshot with verified
// media. Archives contain no authentication or live publication grants.
package journalbackup

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path"
	"strings"
	"unicode"

	"github.com/pkg/errors"
)

const (
	Format                 = "personal-life-journal"
	Version                = 1
	MaxArchiveBytes  int64 = 1 << 30
	MaxExpandedBytes int64 = 2 << 30
	MaxEntryBytes    int64 = 256 << 20
	MaxManifestBytes int64 = 16 << 20
	MaxEntries             = 30000
)

type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Memo struct {
	UID         string          `json:"uid"`
	CreatedTs   int64           `json:"createdTs"`
	UpdatedTs   int64           `json:"updatedTs"`
	ModifiedTs  int64           `json:"modifiedTs,omitempty"`
	EnteredTs   int64           `json:"enteredTs,omitempty"`
	State       string          `json:"state"`
	ContentPath string          `json:"contentPath"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	RelatedUIDs []string        `json:"relatedUids,omitempty"`
}
type Attachment struct {
	UID          string          `json:"uid"`
	MemoUID      string          `json:"memoUid,omitempty"`
	Filename     string          `json:"filename"`
	Type         string          `json:"type"`
	CreatedTs    int64           `json:"createdTs"`
	UpdatedTs    int64           `json:"updatedTs"`
	Path         string          `json:"path,omitempty"`
	OriginalPath string          `json:"originalPath,omitempty"`
	ExternalLink string          `json:"externalLink,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}
type Document struct {
	Kind    string          `json:"kind"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}
type Manifest struct {
	Format           string       `json:"format"`
	Version          int          `json:"version"`
	CreatedTs        int64        `json:"createdTs"`
	GeneratorVersion string       `json:"generatorVersion"`
	IncludeHistory   bool         `json:"includeHistory"`
	Memos            []Memo       `json:"memos"`
	Attachments      []Attachment `json:"attachments"`
	Documents        []Document   `json:"documents"`
	Entries          []Entry      `json:"entries"`
	Warnings         []string     `json:"warnings"`
}

// SafePath permits only normalized archive-relative paths, never filesystem paths.
func SafePath(name string) bool {
	return name != "" && name != "." && len(name) < 1024 && strings.IndexFunc(name, unicode.IsControl) < 0 && !strings.ContainsAny(name, "\\:") && !strings.HasPrefix(name, "/") && !strings.HasSuffix(name, "/") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}

type Writer struct {
	zip     *zip.Writer
	entries []Entry
	seen    map[string]bool
	bytes   int64
}

func NewWriter(w io.Writer) *Writer { return &Writer{zip: zip.NewWriter(w), seen: map[string]bool{}} }
func (w *Writer) Add(name string, source io.Reader) error {
	if !SafePath(name) || name == "manifest.json" || w.seen[name] || len(w.entries) >= MaxEntries-1 {
		return errors.New("invalid or duplicate backup entry")
	}
	w.seen[name] = true
	dest, err := w.zip.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dest, hash), io.LimitReader(source, MaxEntryBytes+1))
	if err != nil {
		return err
	}
	if n > MaxEntryBytes {
		return errors.New("one backup file exceeds 256 MiB")
	}
	w.bytes += n
	if w.bytes > MaxArchiveBytes-(32<<20) {
		return errors.New("backup exceeds the 1 GiB portable archive limit")
	}
	w.entries = append(w.entries, Entry{Path: name, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))})
	return nil
}
func (w *Writer) Close(m *Manifest) error {
	m.Format = Format
	m.Version = Version
	m.Entries = w.entries
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if len(encoded) > int(MaxManifestBytes) {
		return errors.New("backup manifest is too large")
	}
	dest, err := w.zip.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Store})
	if err != nil {
		return err
	}
	if _, err = dest.Write(encoded); err != nil {
		return err
	}
	return w.zip.Close()
}

type Archive struct {
	Manifest Manifest
	files    map[string]*zip.File
}

func Read(reader io.ReaderAt, size int64) (*Archive, error) {
	if size <= 0 || size > MaxArchiveBytes {
		return nil, errors.New("backup archive must be at most 1 GiB")
	}
	z, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, errors.Wrap(err, "invalid backup ZIP")
	}
	if len(z.File) > MaxEntries {
		return nil, errors.New("backup has too many entries")
	}
	a := &Archive{files: map[string]*zip.File{}}
	var expanded uint64
	for _, f := range z.File {
		if !SafePath(f.Name) || a.files[f.Name] != nil || !f.Mode().IsRegular() {
			return nil, errors.New("backup contains an unsafe or duplicate path")
		}
		if f.UncompressedSize64 > uint64(MaxEntryBytes) {
			return nil, errors.New("backup entry exceeds limit")
		}
		expanded += f.UncompressedSize64
		if expanded > uint64(MaxExpandedBytes) {
			return nil, errors.New("backup expanded size exceeds limit")
		}
		a.files[f.Name] = f
	}
	manifest, ok := a.files["manifest.json"]
	if !ok || manifest.UncompressedSize64 > uint64(MaxManifestBytes) {
		return nil, errors.New("backup manifest is missing or too large")
	}
	r, err := manifest.Open()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(io.LimitReader(r, MaxManifestBytes+1))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&a.Manifest)
	if err == nil && decoder.Decode(&struct{}{}) != io.EOF {
		err = errors.New("trailing manifest data")
	}
	closeErr := r.Close()
	if err != nil {
		return nil, errors.Wrap(err, "invalid backup manifest")
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if a.Manifest.Format != Format || a.Manifest.Version != Version {
		return nil, errors.New("unsupported backup format/version")
	}
	if len(a.Manifest.Entries) != len(a.files)-1 {
		return nil, errors.New("backup entry inventory mismatch")
	}
	seen := map[string]bool{}
	for _, entry := range a.Manifest.Entries {
		f := a.files[entry.Path]
		if f == nil || entry.Path == "manifest.json" || seen[entry.Path] || entry.Size < 0 || uint64(entry.Size) != f.UncompressedSize64 {
			return nil, errors.New("backup contains missing or conflicting entries")
		}
		seen[entry.Path] = true
		file, err := f.Open()
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(file, MaxEntryBytes+1))
		closeErr := file.Close()
		if err != nil {
			return nil, errors.Wrap(err, "backup entry could not be read")
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return nil, errors.New("backup checksum mismatch")
		}
	}
	return a, nil
}
func (a *Archive) Open(name string) (io.ReadCloser, error) {
	f := a.files[name]
	if f == nil || name == "manifest.json" {
		return nil, errors.New("backup file is missing")
	}
	return f.Open()
}
func (a *Archive) Text(name string) ([]byte, error) {
	r, err := a.Open(name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, MaxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > int(MaxManifestBytes) {
		return nil, errors.New("record text exceeds limit")
	}
	return data, nil
}
func (a *Archive) Has(name string) bool { return name != "manifest.json" && a.files[name] != nil }
