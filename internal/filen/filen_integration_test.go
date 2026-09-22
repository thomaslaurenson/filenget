//go:build integration

package filen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thomaslaurenson/filenget/internal/filen"
)

// networkEnv gates every test here. They fetch from Filen's live public-link
// API, which a CI runner has no business reaching and a developer offline
// cannot.
const networkEnv = "FILENGET_TEST_NETWORK"

// The shares these tests run against, published from /public/filenget. Both
// links and the checksums below describe the same two files; recreate the
// shares and all of these constants change together.
const (
	folderLink = "https://drive.filen.io/f/fc725ed4-a559-4c33-99fe-645586e36e89#t_hNgrc6vavw0i9EnjAPyKUXJKEuO2J0"
	fileLink   = "https://drive.filen.io/d/66cc6b61-8438-4599-a257-df5c5227c2fa#oTOCCbortnvQUXyuSjiwU28J5gpxeGDh"
)

// fixture is one file behind the shares, and what it must arrive as.
type fixture struct {
	name   string
	size   int64
	sha256 string
}

var (
	// helloFixture is a single chunk; chunksFixture is deliberately larger than
	// Filen's chunk size, so the loop that reassembles a file is exercised.
	helloFixture  = fixture{name: "hello.txt", size: 31, sha256: "f06048ab96a70363dacbdaf6edab83d1cb546790f99f3e9263fa64bb0fd8fc00"}
	chunksFixture = fixture{name: "chunks.bin", size: 3145728, sha256: "e76a2372fcc8e6d36e3a9c015f2b9f1eda31d8469667bee9dac03f79c2812e8b"}
)

func TestFetchFolderLink(t *testing.T) {
	requireNetwork(t)
	t.Parallel()

	client := liveClient()
	dir := filepath.Join(t.TempDir(), "dest")

	var progress bytes.Buffer
	if err := client.Fetch(t.Context(), &progress, dir, []string{folderLink}); err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	for _, want := range []fixture{helloFixture, chunksFixture} {
		assertFixture(t, filepath.Join(dir, want.name), want)
	}

	// A second run over the same destination must fetch nothing.
	var second bytes.Buffer
	if err := client.Fetch(t.Context(), &second, dir, []string{folderLink}); err != nil {
		t.Fatalf("second Fetch returned error: %v", err)
	}
	if strings.Contains(second.String(), "fetching") {
		t.Errorf("second run progress = %q, want every file reported as present", second.String())
	}
}

func TestFilesReportsMoreThanOneChunk(t *testing.T) {
	requireNetwork(t)
	t.Parallel()

	link, err := filen.ParseLink(folderLink)
	if err != nil {
		t.Fatalf("ParseLink returned error: %v", err)
	}
	files, err := liveClient().Files(t.Context(), link)
	if err != nil {
		t.Fatalf("Files returned error: %v", err)
	}
	for _, f := range files {
		if f.Name != chunksFixture.name {
			continue
		}
		// The point of the larger fixture: a single-chunk file would not catch
		// a fault in the order or the count of the chunks fetched.
		if f.Chunks < 2 {
			t.Errorf("%q reports %d chunk(s), want more than one", f.Name, f.Chunks)
		}
		return
	}
	t.Errorf("the share does not hold %q", chunksFixture.name)
}

func TestFetchFileLink(t *testing.T) {
	requireNetwork(t)
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "dest")
	if err := liveClient().Fetch(t.Context(), &bytes.Buffer{}, dir, []string{fileLink}); err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	assertFixture(t, filepath.Join(dir, chunksFixture.name), chunksFixture)
}

func TestFetchRejectsDeadLink(t *testing.T) {
	requireNetwork(t)
	t.Parallel()

	dead := "https://drive.filen.io/f/00000000-0000-0000-0000-000000000000#" + strings.Repeat("a", 32)
	dir := filepath.Join(t.TempDir(), "dest")

	if err := liveClient().Fetch(t.Context(), &bytes.Buffer{}, dir, []string{dead}); err == nil {
		t.Error("Fetch through a dead link returned nil error")
	}
}

func TestFetchRejectsWrongKey(t *testing.T) {
	requireNetwork(t)
	t.Parallel()

	uuid, _, _ := strings.Cut(strings.TrimPrefix(folderLink, "https://drive.filen.io/f/"), "#")
	wrong := "https://drive.filen.io/f/" + uuid + "#" + strings.Repeat("a", 32)
	dir := filepath.Join(t.TempDir(), "dest")

	// The key authenticates as well as decrypts, so the wrong one fails before
	// anything is written rather than producing a file of rubbish.
	if err := liveClient().Fetch(t.Context(), &bytes.Buffer{}, dir, []string{wrong}); err == nil {
		t.Fatal("Fetch with the wrong key returned nil error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("destination holds %d entries, want none", len(entries))
	}
}

// requireNetwork skips unless the live API is meant to be reached.
func requireNetwork(t *testing.T) {
	t.Helper()

	if os.Getenv(networkEnv) == "" {
		t.Skipf("%s not set; these tests fetch from Filen's live public-link API", networkEnv)
	}
}

// liveClient builds a client pointed at Filen, with the timeout the command
// layer uses.
func liveClient() *filen.Client {
	return filen.New(&http.Client{Timeout: 10 * time.Minute}, filen.DefaultGateway, filen.DefaultEgest)
}

// assertFixture checks a downloaded file against its published size and digest.
func assertFixture(t *testing.T, path string, want fixture) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if info.Size() != want.size {
		t.Errorf("%q is %d bytes, want %d", want.name, info.Size(), want.size)
	}
	if got := sha256Of(t, path); got != want.sha256 {
		t.Errorf("%q sha256 = %s, want %s", want.name, got, want.sha256)
	}
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %q: %v", path, err)
	}
	defer f.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
