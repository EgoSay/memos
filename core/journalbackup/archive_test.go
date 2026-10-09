package journalbackup

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArchiveRoundtripKeepsExactBytes(t *testing.T) {
	var result bytes.Buffer
	w := NewWriter(&result)
	content := "生活原文\r\n#tag\n"
	require.NoError(t, w.Add("memos/first.md", strings.NewReader(content)))
	m := Manifest{Memos: []Memo{{UID: "first", ContentPath: "memos/first.md", State: "NORMAL"}}}
	require.NoError(t, w.Close(&m))
	a, err := Read(bytes.NewReader(result.Bytes()), int64(result.Len()))
	require.NoError(t, err)
	actual, err := a.Text("memos/first.md")
	require.NoError(t, err)
	require.Equal(t, []byte(content), actual)
	require.Equal(t, Version, a.Manifest.Version)
}

func TestArchiveRejectsUnsafeOrUnverifiableEntries(t *testing.T) {
	for _, test := range []struct {
		name, path                         string
		mode                               fs.FileMode
		badHash, duplicate, extra, version bool
	}{
		{name: "traversal", path: "../private"}, {name: "absolute", path: "/tmp/private"},
		{name: "windows", path: `C:\private`}, {name: "normalized", path: "a/../private"},
		{name: "control", path: "a\nprivate"}, {name: "symlink", path: "media", mode: fs.ModeSymlink | 0600},
		{name: "checksum", path: "memo.md", badHash: true}, {name: "duplicate", path: "memo.md", duplicate: true},
		{name: "unlisted", path: "memo.md", extra: true}, {name: "version", path: "memo.md", version: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var result bytes.Buffer
			w := zip.NewWriter(&result)
			add := func(name string, body []byte, mode fs.FileMode) {
				h := &zip.FileHeader{Name: name, Method: zip.Deflate}
				if mode != 0 {
					h.SetMode(mode)
				}
				dest, err := w.CreateHeader(h)
				require.NoError(t, err)
				_, err = dest.Write(body)
				require.NoError(t, err)
			}
			body := []byte("private record")
			add(test.path, body, test.mode)
			if test.duplicate {
				add(test.path, body, 0)
			}
			if test.extra {
				add("extra", body, 0)
			}
			hash := sha256.Sum256(body)
			m := Manifest{Format: Format, Version: Version, Entries: []Entry{{Path: test.path, Size: int64(len(body)), SHA256: hex.EncodeToString(hash[:])}}}
			if test.badHash {
				m.Entries[0].SHA256 = strings.Repeat("0", 64)
			}
			if test.version {
				m.Version++
			}
			payload, err := json.Marshal(m)
			require.NoError(t, err)
			add("manifest.json", payload, 0)
			require.NoError(t, w.Close())
			_, err = Read(bytes.NewReader(result.Bytes()), int64(result.Len()))
			require.Error(t, err)
		})
	}
}

func TestArchiveRejectsOversizeBeforeReading(t *testing.T) {
	_, err := Read(bytes.NewReader(nil), MaxArchiveBytes+1)
	require.ErrorContains(t, err, "at most")
	var result bytes.Buffer
	w := zip.NewWriter(&result)
	_, err = w.CreateRaw(&zip.FileHeader{Name: "bomb", Method: zip.Store, UncompressedSize64: uint64(MaxEntryBytes + 1), CompressedSize64: 0})
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = Read(bytes.NewReader(result.Bytes()), int64(result.Len()))
	require.ErrorContains(t, err, "entry exceeds")
}
