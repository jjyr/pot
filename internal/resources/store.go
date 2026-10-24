// Package resources owns immutable, assistant-scoped display originals. It does
// not execute HTML or decide whether an original is model-readable.
package resources

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	_ "golang.org/x/image/webp"
)

const FileLimit = 64 << 20
const BundleLimit = 16 << 20
const ChunkSize = 192 << 10
const StoreLimit = 1 << 30

type File = core.ResourceFile
type Resource = core.Resource
type Presentation = core.ResourcePresentation
type Store struct {
	ValidateOwner func(context.Context, string) error
	db            *sql.DB
	root          string
	mu            sync.Mutex
}

func Open(root, database string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	db, err := storage.OpenDatabase(database, storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, root: root}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Receipt(ctx context.Context, owner, request, hash string) (*Resource, error) {
	if s.ValidateOwner != nil {
		if err := s.ValidateOwner(ctx, owner); err != nil {
			return nil, err
		}
	}
	var raw, storedOwner, storedHash string
	err := s.db.QueryRowContext(ctx, "SELECT owner,input_hash,metadata FROM display_resources WHERE request_id=?", request).Scan(&storedOwner, &storedHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if owner != storedOwner || hash != storedHash {
		return nil, errors.New("resource registration identity conflict")
	}
	var resource Resource
	err = json.Unmarshal([]byte(raw), &resource)
	return &resource, err
}
func (s *Store) Get(ctx context.Context, owner, id string) (Resource, error) {
	var resource Resource
	if !core.Identifier(owner) || !core.Identifier(id) {
		return resource, errors.New("invalid resource identity")
	}
	if s.ValidateOwner != nil {
		if err := s.ValidateOwner(ctx, owner); err != nil {
			return resource, err
		}
	}
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT metadata FROM display_resources WHERE id=? AND owner=?", id, owner).Scan(&raw)
	if err != nil {
		return resource, errors.New("resource unavailable")
	}
	err = json.Unmarshal([]byte(raw), &resource)
	return resource, err
}

// Register commits metadata only after all original bytes have been saved. A
// resource ID is independent of its Task workspace and survives Task cleanup.
func (s *Store) Register(ctx context.Context, owner, request, hash, name, entry string, paths []string, contents [][]byte) (Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, err := s.Receipt(ctx, owner, request, hash); err != nil {
		return Resource{}, err
	} else if previous != nil {
		return *previous, nil
	}
	resource := Resource{ID: storage.ID(), Name: name, Entry: entry}
	if name == "" {
		return resource, errors.New("resource.name must be a nonempty display label")
	}
	if !core.Identifier(owner) || len(paths) == 0 || len(paths) > 64 || len(paths) != len(contents) {
		return resource, errors.New("invalid resource manifest")
	}
	found := false
	seen := map[string]bool{}
	for index, path := range paths {
		if !protocol.ValidFilePath(path) || seen[path] || len(contents[index]) > FileLimit {
			return resource, errors.New("invalid resource file")
		}
		seen[path] = true
		data := contents[index]
		digest := sha256.Sum256(data)
		file := File{Path: path, MIMEType: DetectType(path, data), Size: uint64(len(data)), Digest: hex.EncodeToString(digest[:])}
		if file.MIMEType == "image/png" || file.MIMEType == "image/jpeg" || file.MIMEType == "image/webp" || file.MIMEType == "image/gif" {
			config, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || len(data) > 16<<20 || config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || uint64(config.Width)*uint64(config.Height) > 32_000_000 {
				return resource, errors.New("resource image exceeds raster preview limits")
			}
		}
		resource.Files = append(resource.Files, file)
		resource.Size += file.Size
		if path == entry {
			found = true
			resource.MIMEType = file.MIMEType
		}
	}
	if !found || resource.Size > FileLimit || len(paths) > 1 && (resource.MIMEType != "text/html" || resource.Size > BundleLimit) {
		return resource, errors.New("resource exceeds file or HTML bundle limits")
	}
	var archive []byte
	for _, file := range resource.Files {
		if file.Path == entry {
			resource.Download = file
			resource.Download.Path = core.DownloadFilename(filepath.Base(file.Path))
		}
	}
	if len(paths) > 1 {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		for index, path := range paths {
			file, err := writer.Create(path)
			if err != nil {
				return resource, err
			}
			if _, err = file.Write(contents[index]); err != nil {
				return resource, err
			}
		}
		if err := writer.Close(); err != nil {
			return resource, err
		}
		archive = buffer.Bytes()
		digest := sha256.Sum256(archive)
		downloadName := core.DownloadFilename(name)
		if ext := filepath.Ext(downloadName); ext != downloadName {
			downloadName = strings.TrimSuffix(downloadName, ext)
		}
		downloadName = core.DownloadFilename(downloadName + ".zip")
		resource.Download = File{Path: downloadName, MIMEType: "application/zip", Size: uint64(len(archive)), Digest: hex.EncodeToString(digest[:])}
	}
	var total, count int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(size),0),COUNT(*) FROM display_resources").Scan(&total, &count); err != nil {
		return resource, err
	}
	if total+int64(resource.Size)+int64(len(archive)) > StoreLimit || count >= 4096 {
		return resource, errors.New("resource storage limit reached")
	}
	directory := filepath.Join(s.root, resource.ID)
	if err := os.Mkdir(directory, 0700); err != nil {
		return resource, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(directory)
		}
	}()
	for index, file := range resource.Files {
		if err := ctx.Err(); err != nil {
			return resource, err
		}
		if err := os.WriteFile(filepath.Join(directory, file.Digest), contents[index], 0600); err != nil {
			return resource, err
		}
	}
	if archive != nil {
		if err := os.WriteFile(filepath.Join(directory, resource.Download.Digest), archive, 0600); err != nil {
			return resource, err
		}
	}
	raw, err := json.Marshal(resource)
	if err != nil {
		return resource, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO display_resources VALUES(?,?,?,?,?,?,?)", resource.ID, owner, request, hash, string(raw), resource.Size+uint64(len(archive)), storage.Now())
	committed = err == nil
	return resource, err
}
func DetectType(path string, data []byte) string {
	detected := strings.Split(http.DetectContentType(data), ";")[0]
	extension := strings.ToLower(filepath.Ext(path))
	if len(data) >= 4 && string(data[:4]) == "fLaC" {
		return "audio/flac"
	}
	if len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 {
		if extension == ".aac" && data[1]&0xf6 == 0xf0 {
			return "audio/aac"
		}
		if extension == ".mp3" && data[1]&0x06 != 0 {
			return "audio/mpeg"
		}
	}
	// Active formats are explicit file kinds and are always read as inert bytes
	// outside the isolated renderer. Raster/media types require signatures.
	if (extension == ".html" || extension == ".htm") && (detected == "text/plain" || detected == "text/html") {
		return "text/html"
	}
	if extension == ".css" && detected == "text/plain" {
		return "text/css"
	}
	if (extension == ".js" || extension == ".mjs") && detected == "text/plain" {
		return "text/javascript"
	}
	if strings.HasPrefix(string(data), "%PDF-") {
		return "application/pdf"
	}
	if len(data) > 12 && string(data[4:8]) == "ftyp" {
		if extension == ".m4a" {
			return "audio/mp4"
		}
		return "video/mp4"
	}
	if detected == "video/webm" && extension == ".weba" {
		return "audio/webm"
	}
	if detected == "application/ogg" {
		if extension == ".ogv" {
			return "video/ogg"
		}
		return "audio/ogg"
	}
	if detected == "text/plain" || extension == ".json" {
		return "text/plain"
	}
	return detected
}
func (s *Store) OpenFile(ctx context.Context, owner, id, path string) (*os.File, File, error) {
	resource, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, File{}, err
	}
	if path == "" {
		path = resource.Entry
	}
	if path == "@download" {
		f, err := os.Open(filepath.Join(s.root, resource.ID, resource.Download.Digest))
		return f, resource.Download, err
	}
	for _, file := range resource.Files {
		if file.Path == path {
			f, err := os.Open(filepath.Join(s.root, resource.ID, file.Digest))
			return f, file, err
		}
	}
	return nil, File{}, errors.New("resource file unavailable")
}
func (s *Store) Chunk(ctx context.Context, owner, id, path string, offset uint64) (protocol.FileChunk, error) {
	f, metadata, err := s.OpenFile(ctx, owner, id, path)
	if err != nil {
		return protocol.FileChunk{}, err
	}
	defer f.Close()
	if offset > metadata.Size {
		return protocol.FileChunk{}, errors.New("invalid resource offset")
	}
	data := make([]byte, min(ChunkSize, metadata.Size-offset))
	if _, err = io.ReadFull(io.NewSectionReader(f, int64(offset), int64(len(data))), data); err != nil {
		return protocol.FileChunk{}, err
	}
	return protocol.FileChunk{Size: metadata.Size, Offset: offset, Revision: metadata.Digest, Data: base64.StdEncoding.EncodeToString(data)}, nil
}
