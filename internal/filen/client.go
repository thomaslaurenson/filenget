package filen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const (
	// DefaultGateway is Filen's public API.
	DefaultGateway = "https://gateway.filen.io"
	// DefaultEgest is where Filen serves the encrypted chunks.
	DefaultEgest = "https://egest.filen.io"

	fileLinkInfoPath     = "/v3/file/link/info"
	fileLinkPasswordPath = "/v3/file/link/password"
	dirLinkInfoPath      = "/v3/dir/link/info"
	dirLinkContentPath   = "/v3/dir/link/content"

	// noPassword is what the API expects in the password field of a link that
	// has none. An empty string is rejected; the literal word is not.
	noPassword = "empty"
)

// ErrPasswordProtected reports a share whose link carries a password. Deriving
// the hash the API expects is not implemented, so the link fails by name rather
// than relaying a complaint about a credential that was never sent.
var ErrPasswordProtected = errors.New("password-protected links are not supported")

// File is one file behind a share link: its decrypted name and size, the key
// its chunks are encrypted with, and where those chunks are served from.
type File struct {
	Name    string
	Size    int64
	Key     string
	UUID    string
	Region  string
	Bucket  string
	Chunks  int
	Version int
}

// location says where a file's chunks live, in the shape every reply uses.
type location struct {
	UUID    string `json:"uuid"`
	Region  string `json:"region"`
	Bucket  string `json:"bucket"`
	Chunks  int    `json:"chunks"`
	Version int    `json:"version"`
}

// file completes a location with the metadata that had to be decrypted first.
func (l location) file(name string, size int64, key string) File {
	return File{
		Name:    name,
		Size:    size,
		Key:     key,
		UUID:    l.UUID,
		Region:  l.Region,
		Bucket:  l.Bucket,
		Chunks:  l.Chunks,
		Version: l.Version,
	}
}

// Client fetches and decrypts the files behind Filen public share links.
type Client struct {
	http      *http.Client
	gateway   string
	egest     string
	chunkSize int64
}

// New builds a Client that reads shares through the given gateway and egest
// base URLs. They are arguments rather than constants so a test can drive the
// whole protocol through httptest without touching the network.
func New(httpClient *http.Client, gateway, egest string) *Client {
	return &Client{
		http:      httpClient,
		gateway:   gateway,
		egest:     egest,
		chunkSize: protocolChunkSize,
	}
}

// Files lists every file the link reaches, with its metadata decrypted. A
// folder link lists the files directly inside the folder; subfolders are not
// descended.
func (c *Client) Files(ctx context.Context, link Link) ([]File, error) {
	if link.Kind == FileLink {
		return c.linkedFile(ctx, link)
	}
	return c.folderFiles(ctx, link)
}

// linkedFile reads a file link, whose key is the file's own key and unlocks
// each metadata field separately.
func (c *Client) linkedFile(ctx context.Context, link Link) ([]File, error) {
	var info struct {
		location
		Name string `json:"name"`
		Size string `json:"size"`
	}
	body := map[string]string{"uuid": link.UUID, "password": noPassword}
	if err := c.post(ctx, fileLinkInfoPath, body, &info); err != nil {
		// The API rejects the absent password without saying that is what was
		// missing, so ask the endpoint that does say before relaying a message
		// the user cannot act on.
		if c.needsPassword(ctx, link.UUID) {
			return nil, fmt.Errorf("file link %q: %w", link.UUID, ErrPasswordProtected)
		}
		return nil, err
	}
	name, err := decryptMetadata(info.Name, link.Key)
	if err != nil {
		return nil, fmt.Errorf("file link %q: name: %w", link.UUID, err)
	}
	sizeText, err := decryptMetadata(info.Size, link.Key)
	if err != nil {
		return nil, fmt.Errorf("file link %q: size: %w", link.UUID, err)
	}
	size, err := strconv.ParseInt(string(sizeText), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("file link %q: size %q: %w", link.UUID, sizeText, err)
	}
	return []File{info.location.file(string(name), size, link.Key)}, nil
}

// needsPassword reports whether a file link is password-protected. It runs only
// on a path that has already failed, so a failure here leaves the original
// error to be reported rather than replacing it with one about the diagnosis.
func (c *Client) needsPassword(ctx context.Context, uuid string) bool {
	var status struct {
		HasPassword bool `json:"hasPassword"`
	}
	if err := c.post(ctx, fileLinkPasswordPath, map[string]string{"uuid": uuid}, &status); err != nil {
		return false
	}
	return status.HasPassword
}

// folderFiles reads a folder link, whose key unlocks one JSON blob per file
// that carries the file's own key.
func (c *Client) folderFiles(ctx context.Context, link Link) ([]File, error) {
	var root struct {
		Parent      string `json:"parent"`
		HasPassword bool   `json:"hasPassword"`
	}
	if err := c.post(ctx, dirLinkInfoPath, map[string]string{"uuid": link.UUID}, &root); err != nil {
		return nil, err
	}
	if root.HasPassword {
		return nil, fmt.Errorf("folder link %q: %w", link.UUID, ErrPasswordProtected)
	}
	var content struct {
		Files []struct {
			location
			Metadata string `json:"metadata"`
		} `json:"files"`
	}
	body := map[string]string{"uuid": link.UUID, "password": noPassword, "parent": root.Parent}
	if err := c.post(ctx, dirLinkContentPath, body, &content); err != nil {
		return nil, err
	}
	files := make([]File, 0, len(content.Files))
	for _, record := range content.Files {
		blob, err := decryptMetadata(record.Metadata, link.Key)
		if err != nil {
			return nil, fmt.Errorf("folder link %q: file %q: %w", link.UUID, record.UUID, err)
		}
		var metadata struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			Key  string `json:"key"`
		}
		if err := json.Unmarshal(blob, &metadata); err != nil {
			return nil, fmt.Errorf("folder link %q: file %q: metadata: %w", link.UUID, record.UUID, err)
		}
		files = append(files, record.location.file(metadata.Name, metadata.Size, metadata.Key))
	}
	return files, nil
}

// post calls one gateway endpoint and decodes the data field of its reply
// into out.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request for %q: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gateway+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request for %q: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("post %q: %w", path, err)
	}
	defer response.Body.Close()

	reply, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read reply from %q: %w", path, err)
	}
	var envelope struct {
		Status  bool            `json:"status"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	// Decode before looking at the status code: the API reports a dead or
	// unknown link in this envelope, and its message says more than the code.
	if err := json.Unmarshal(reply, &envelope); err != nil {
		return fmt.Errorf("post %q: http %s: %w", path, response.Status, err)
	}
	if !envelope.Status {
		return fmt.Errorf("post %q: %s", path, envelope.Message)
	}
	// A success carrying no data would otherwise reach the caller as a complaint
	// about truncated JSON, which names neither the endpoint nor what was wrong.
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return fmt.Errorf("post %q: reply carried no data", path)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode reply from %q: %w", path, err)
	}
	return nil
}
