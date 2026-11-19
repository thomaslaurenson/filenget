package filen

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFilesFolderLink(t *testing.T) {
	t.Parallel()
	files := []testFile{
		{name: "first.bin", key: testFileKey, data: []byte("first file contents")},
		{name: "second.bin", key: strings.Repeat("b", keyLength), data: []byte("second")},
	}
	server := newShareServer(t, testFolderKey, files)
	client := newTestClient(server)

	got, err := client.Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: testFolderKey})
	if err != nil {
		t.Fatalf("Files returned error: %v", err)
	}
	if len(got) != len(files) {
		t.Fatalf("Files returned %d files, want %d", len(got), len(files))
	}
	for i, want := range files {
		if got[i].Name != want.name {
			t.Errorf("file %d name = %q, want %q", i, got[i].Name, want.name)
		}
		if got[i].Size != int64(len(want.data)) {
			t.Errorf("file %d size = %d, want %d", i, got[i].Size, len(want.data))
		}
		// The folder key unlocks the blob; the key inside it is the file's own.
		if got[i].Key != want.key {
			t.Errorf("file %d key = %q, want %q", i, got[i].Key, want.key)
		}
		if got[i].Chunks != want.chunkCount() {
			t.Errorf("file %d chunks = %d, want %d", i, got[i].Chunks, want.chunkCount())
		}
	}
}

func TestFilesFileLink(t *testing.T) {
	t.Parallel()
	file := testFile{name: "only.bin", key: testFileKey, data: []byte("payload that spans chunks")}
	server := newShareServer(t, testFolderKey, []testFile{file})
	client := newTestClient(server)

	// A file link's key is the file's own, and unlocks each field separately.
	got, err := client.Files(t.Context(), Link{Kind: FileLink, UUID: "share", Key: file.key})
	if err != nil {
		t.Fatalf("Files returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Files returned %d files, want 1", len(got))
	}
	if got[0].Name != file.name {
		t.Errorf("name = %q, want %q", got[0].Name, file.name)
	}
	if got[0].Size != int64(len(file.data)) {
		t.Errorf("size = %d, want %d", got[0].Size, len(file.data))
	}
	if got[0].Key != file.key {
		t.Errorf("key = %q, want %q", got[0].Key, file.key)
	}
}

func TestFilesWrongKey(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "first.bin", key: testFileKey, data: []byte("contents")},
	})
	client := newTestClient(server)

	_, err := client.Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: "wrong-key"})
	if err == nil {
		t.Fatal("Files with the wrong key returned nil error")
	}
	if !strings.Contains(err.Error(), "share") {
		t.Errorf("error = %v, want it to name the link", err)
	}
}

func TestFilesReportsGatewayFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply func(w http.ResponseWriter)
		want  string
	}{
		{
			name: "the API says the link is gone",
			reply: func(w http.ResponseWriter) {
				w.Write([]byte(`{"status":false,"message":"link not found"}`))
			},
			want: "link not found",
		},
		{
			name: "a reply that is not the envelope at all",
			reply: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte("<html>gateway error</html>"))
			},
			want: "502",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tc.reply(w)
			}))
			t.Cleanup(server.Close)

			_, err := newTestClient(server).Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: testFolderKey})
			if err == nil {
				t.Fatal("Files returned nil error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// newServerWithAnUnreadableFile serves a folder share holding a file whose
// metadata does not decrypt, listed before one that downloads as "good.bin".
func newServerWithAnUnreadableFile(t *testing.T) *httptest.Server {
	t.Helper()

	good, err := json.Marshal(map[string]any{"name": "good.bin", "size": 4, "key": testFileKey})
	if err != nil {
		t.Fatalf("encode metadata: %v", err)
	}
	dirInfo := envelope(t, map[string]string{
		"parent":   "parent-uuid",
		"metadata": encryptMetadata(t, `{"name":"share"}`, testFolderKey),
	})
	dirContent := envelope(t, map[string]any{"files": []map[string]any{
		{"uuid": "bad", "region": testRegion, "bucket": testBucket, "chunks": 1, "version": dataVersion,
			"metadata": encryptMetadata(t, "{}", "another-key")},
		{"uuid": "good", "region": testRegion, "bucket": testBucket, "chunks": 1, "version": dataVersion,
			"metadata": encryptMetadata(t, string(good), testFolderKey)},
	}})
	chunk := encryptChunk(t, []byte("kept"), testFileKey)

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+dirLinkInfoPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(dirInfo) })
	mux.HandleFunc("POST "+dirLinkContentPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(dirContent) })
	mux.HandleFunc("GET /{region}/{bucket}/good/0", func(w http.ResponseWriter, _ *http.Request) { w.Write(chunk) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestFilesListsPastAnUnreadableFile(t *testing.T) {
	t.Parallel()
	server := newServerWithAnUnreadableFile(t)

	files, err := newTestClient(server).Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: testFolderKey})
	if err == nil {
		t.Error("Files returned nil error for an unreadable file")
	}
	if len(files) != 1 || files[0].Name != "good.bin" {
		t.Errorf("Files = %+v, want only good.bin", files)
	}
}

// newListingServer serves a folder share whose listing holds records, for a
// test that needs the reply in a shape newShareServer does not write.
func newListingServer(t *testing.T, records ...map[string]any) *httptest.Server {
	t.Helper()

	dirInfo := envelope(t, map[string]string{
		"parent":   "parent-uuid",
		"metadata": encryptMetadata(t, `{"name":"share"}`, testFolderKey),
	})
	dirContent := envelope(t, map[string]any{"files": records})
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+dirLinkInfoPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(dirInfo) })
	mux.HandleFunc("POST "+dirLinkContentPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(dirContent) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestLooseInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		json    string
		want    looseInt
		wantErr bool
	}{
		{name: "integer", json: `6`, want: 6},
		{name: "zero", json: `0`, want: 0},
		{name: "float with no fraction", json: `6.0`, want: 6},
		{name: "float with a fraction is truncated", json: `6.9`, want: 6},
		{name: "exponent", json: `1e3`, want: 1000},
		{name: "numeric string", json: `"6"`, want: 6},
		{name: "numeric string with spaces", json: `" 6 "`, want: 6},
		{name: "float string", json: `"6.5"`, want: 6},
		{name: "negative integer", json: `-1`, wantErr: true},
		{name: "negative string", json: `"-1"`, wantErr: true},
		{name: "not a number", json: `"six"`, wantErr: true},
		{name: "NaN", json: `"NaN"`, wantErr: true},
		{name: "too large", json: `1e300`, wantErr: true},
		{name: "null", json: `null`, wantErr: true},
		{name: "bool", json: `true`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got looseInt
			err := json.Unmarshal([]byte(tc.json), &got)
			if tc.wantErr {
				if err == nil {
					t.Errorf("unmarshal %s = %d, want an error", tc.json, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("unmarshal %s = %d (%v), want %d", tc.json, got, err, tc.want)
			}
		})
	}
}

func TestFilesReadsASizeThatIsNotAnInteger(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		size string
	}{
		{name: "float", size: `4.0`},
		{name: "string", size: `"4"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			blob := `{"name":"loose.bin","size":` + tc.size + `,"key":"` + testFileKey + `"}`
			server := newListingServer(t, map[string]any{
				"uuid": "u", "region": testRegion, "bucket": testBucket, "chunks": 1, "version": dataVersion,
				"metadata": encryptMetadata(t, blob, testFolderKey),
			})

			files, err := newTestClient(server).Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: testFolderKey})
			if err != nil {
				t.Fatalf("Files returned error: %v", err)
			}
			if len(files) != 1 || files[0].Size != 4 {
				t.Errorf("Files = %+v, want one file of 4 bytes", files)
			}
		})
	}
}

func TestFilesReadsAChunkCountSentAsAString(t *testing.T) {
	t.Parallel()
	blob := `{"name":"file.bin","size":4,"key":"` + testFileKey + `"}`
	server := newListingServer(t, map[string]any{
		"uuid": "u", "region": testRegion, "bucket": testBucket, "chunks": "1", "version": dataVersion,
		"metadata": encryptMetadata(t, blob, testFolderKey),
	})

	files, err := newTestClient(server).Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: testFolderKey})
	if err != nil {
		t.Fatalf("Files returned error: %v", err)
	}
	if len(files) != 1 || files[0].Chunks != 1 {
		t.Errorf("Files = %+v, want one file in 1 chunk", files)
	}
}

func TestFilesRejectsUnsupportedMetadataVersion(t *testing.T) {
	t.Parallel()
	dirInfo := envelope(t, map[string]string{
		"parent":   "parent-uuid",
		"metadata": encryptMetadata(t, `{"name":"share"}`, testFolderKey),
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == dirLinkInfoPath {
			w.Write(dirInfo)
			return
		}
		// Version 3 metadata, which the SDK defines with hex keys.
		w.Write([]byte(`{"status":true,"data":{"files":[{"uuid":"u","metadata":"003nonce1234567AAAA"}]}}`))
	}))
	t.Cleanup(server.Close)

	_, err := newTestClient(server).Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: testFolderKey})
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("Files error = %v, want ErrUnsupportedVersion", err)
	}
}

func TestFilesHonoursCancellation(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "first.bin", key: testFileKey, data: []byte("contents")},
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := newTestClient(server).Files(ctx, Link{Kind: FolderLink, UUID: "share", Key: testFolderKey}); !errors.Is(err, context.Canceled) {
		t.Errorf("Files error = %v, want context.Canceled", err)
	}
}

func TestFilesRejectsUnreadableFileLinkMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		size    string
		nameKey string
		want    string
	}{
		{name: "a size that is not a number", size: "not a number", nameKey: testFileKey, want: "size"},
		{name: "a name sealed under another key", size: "8", nameKey: "a-different-key", want: "name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := envelope(t, map[string]any{
				"uuid": "uuid-0", "region": testRegion, "bucket": testBucket,
				"chunks": 1, "version": dataVersion,
				"name": encryptMetadata(t, "payload.bin", tc.nameKey),
				"size": encryptMetadata(t, tc.size, testFileKey),
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write(body)
			}))
			t.Cleanup(server.Close)

			_, err := newTestClient(server).Files(t.Context(), Link{Kind: FileLink, UUID: "share", Key: testFileKey})
			if err == nil {
				t.Fatal("Files returned nil error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestFilesReportsAPasswordProtectedLink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		kind  Kind
		reply func(w http.ResponseWriter, path string)
	}{
		{
			name: "a folder link, which says so up front",
			kind: FolderLink,
			reply: func(w http.ResponseWriter, _ string) {
				w.Write([]byte(`{"status":true,"data":{"parent":"p","hasPassword":true}}`))
			},
		},
		{
			name: "a file link, which only says so when asked",
			kind: FileLink,
			reply: func(w http.ResponseWriter, path string) {
				if path == fileLinkPasswordPath {
					w.Write([]byte(`{"status":true,"data":{"hasPassword":true}}`))
					return
				}
				w.Write([]byte(`{"status":false,"message":"wrong password"}`))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.reply(w, r.URL.Path)
			}))
			t.Cleanup(server.Close)

			_, err := newTestClient(server).Files(t.Context(), Link{Kind: tc.kind, UUID: "share", Key: testFolderKey})
			if !errors.Is(err, ErrPasswordProtected) {
				t.Errorf("Files error = %v, want ErrPasswordProtected", err)
			}
		})
	}
}

func TestFilesReportsAFileLinkFailureThatIsNotAPassword(t *testing.T) {
	t.Parallel()
	// The password endpoint says there is none, so the original complaint is
	// what the user needs to see.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == fileLinkPasswordPath {
			w.Write([]byte(`{"status":true,"data":{"hasPassword":false}}`))
			return
		}
		w.Write([]byte(`{"status":false,"message":"link not found"}`))
	}))
	t.Cleanup(server.Close)

	_, err := newTestClient(server).Files(t.Context(), Link{Kind: FileLink, UUID: "share", Key: testFileKey})
	if errors.Is(err, ErrPasswordProtected) {
		t.Fatalf("Files reported a password for a link that has none: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "link not found") {
		t.Errorf("Files error = %v, want the API's own message", err)
	}
}

func TestFilesReportsAReplyCarryingNoData(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply string
	}{
		{name: "the data field is missing", reply: `{"status":true}`},
		{name: "the data field is null", reply: `{"status":true,"data":null}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(tc.reply))
			}))
			t.Cleanup(server.Close)

			_, err := newTestClient(server).Files(t.Context(), Link{Kind: FolderLink, UUID: "share", Key: testFolderKey})
			if err == nil {
				t.Fatal("Files returned nil error")
			}
			if !strings.Contains(err.Error(), "no data") {
				t.Errorf("error = %v, want it to report the empty reply", err)
			}
		})
	}
}
