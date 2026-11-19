package filen

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shareLink builds the folder link a fake share is reached by.
func shareLink(key string) string {
	return "https://drive.filen.io/f/0f8fad5b-d9cb-469f-a165-70867728950e#" + key
}

func TestFetch(t *testing.T) {
	t.Parallel()
	files := []testFile{
		{name: "first.bin", key: testFileKey, data: bytes.Repeat([]byte("a"), testChunkSize*2+1)},
		{name: "second.bin", key: strings.Repeat("b", keyLength), data: []byte("second")},
	}
	server := newShareServer(t, testFolderKey, files)
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")

	var progress bytes.Buffer
	if err := client.Fetch(t.Context(), &progress, dir, []string{shareLink(testFolderKey)}); err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	for _, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, want.name))
		if err != nil {
			t.Fatalf("read %q: %v", want.name, err)
		}
		if !bytes.Equal(got, want.data) {
			t.Errorf("%q = %q, want %q", want.name, got, want.data)
		}
		if !strings.Contains(progress.String(), want.name) {
			t.Errorf("progress = %q, want it to mention %q", progress.String(), want.name)
		}
	}
	if !strings.Contains(progress.String(), "[*]") {
		t.Errorf("progress = %q, want the [*] marker on every line", progress.String())
	}
}

func TestFetchSkipsFilesAlreadyPresent(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "first.bin", key: testFileKey, data: []byte("contents")},
	})
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")
	link := []string{shareLink(testFolderKey)}

	if err := client.Fetch(t.Context(), &bytes.Buffer{}, dir, link); err != nil {
		t.Fatalf("first Fetch returned error: %v", err)
	}
	var progress bytes.Buffer
	if err := client.Fetch(t.Context(), &progress, dir, link); err != nil {
		t.Fatalf("second Fetch returned error: %v", err)
	}
	if !strings.Contains(progress.String(), "already present") {
		t.Errorf("second run progress = %q, want it to report the file as present", progress.String())
	}
}

func TestFetchKeepsAFileNamedLikeAPartFile(t *testing.T) {
	t.Parallel()
	// The ".part" file comes first, so a download of the second file that
	// wrote to "<name>.part" would empty it and then rename it away.
	files := []testFile{
		{name: "data.part", key: testFileKey, data: []byte("the share's own .part file")},
		{name: "data", key: testFileKey, data: []byte("the data")},
	}
	server := newShareServer(t, testFolderKey, files)
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")

	if err := client.Fetch(t.Context(), &bytes.Buffer{}, dir, []string{shareLink(testFolderKey)}); err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	for _, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, want.name))
		if err != nil {
			t.Fatalf("read %q: %v", want.name, err)
		}
		if !bytes.Equal(got, want.data) {
			t.Errorf("%q = %q, want %q", want.name, got, want.data)
		}
	}
}

func TestFetchRejectsBadLinkBeforeTouchingDisk(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "first.bin", key: testFileKey, data: []byte("contents")},
	})
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")

	// The good link comes first, so only parsing every link up front keeps the
	// destination directory from being created for a run that cannot succeed.
	links := []string{shareLink(testFolderKey), "https://example.com/f/share#key"}
	if err := client.Fetch(t.Context(), &bytes.Buffer{}, dir, links); !errors.Is(err, ErrNotALink) {
		t.Fatalf("Fetch error = %v, want ErrNotALink", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Fetch created the destination directory for a link it rejected")
	}
}

func TestFetchKeepsFilesInsideTheDestination(t *testing.T) {
	t.Parallel()
	// The name comes from metadata written by whoever made the share, so it is
	// not trustworthy input.
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "../escaped.bin", key: testFileKey, data: []byte("contents")},
	})
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")

	if err := client.Fetch(t.Context(), &bytes.Buffer{}, dir, []string{shareLink(testFolderKey)}); err == nil {
		t.Fatal("Fetch returned nil error")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.bin")); !os.IsNotExist(err) {
		t.Error("Fetch wrote the file under another name")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped.bin")); !os.IsNotExist(err) {
		t.Error("Fetch wrote outside the destination directory")
	}
}

func TestUsableName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file string
		want bool
	}{
		{name: "plain name", file: "report.txt", want: true},
		{name: "name with spaces", file: "annual report.txt", want: true},
		{name: "name with a directory in it", file: "a/report.txt", want: false},
		{name: "name climbing out", file: "../escaped.bin", want: false},
		{name: "absolute path", file: "/etc/passwd", want: false},
		{name: "trailing separator", file: "report/", want: false},
		{name: "current directory", file: ".", want: false},
		{name: "parent directory", file: "..", want: false},
		{name: "empty", file: "", want: false},
		{name: "colon, a stream on Windows", file: "notes 12:30.txt", want: runtime.GOOS != "windows"},
		{name: "trailing dot, dropped on Windows", file: "report.", want: runtime.GOOS != "windows"},
		{name: "device name on Windows", file: "CON", want: runtime.GOOS != "windows"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := usableName(tc.file); got != tc.want {
				t.Errorf("usableName(%q) = %v, want %v", tc.file, got, tc.want)
			}
		})
	}
}

func TestFetchRejectsANameUsedTwice(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "dup.txt", key: testFileKey, data: []byte("first")},
		{name: "dup.txt", key: testFileKey, data: []byte("second")},
	})
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")

	err := client.Fetch(t.Context(), &bytes.Buffer{}, dir, []string{shareLink(testFolderKey)})
	if !errors.Is(err, errNameTaken) {
		t.Fatalf("Fetch error = %v, want errNameTaken", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "dup.txt")); err != nil || string(got) != "first" {
		t.Errorf("dup.txt = %q (%v), want the first file kept", got, err)
	}
}

func TestFetchRejectsUnusableName(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "..", key: testFileKey, data: []byte("contents")},
	})
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")

	err := client.Fetch(t.Context(), &bytes.Buffer{}, dir, []string{shareLink(testFolderKey)})
	if err == nil {
		t.Fatal("Fetch returned nil error")
	}
	if !strings.Contains(err.Error(), "not usable") {
		t.Errorf("error = %v, want it to name the unusable file name", err)
	}
}

func TestFetchCarriesOnPastAFailedFile(t *testing.T) {
	t.Parallel()
	// The unusable name comes first, so only carrying on past it gets the
	// second file written.
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "..", key: testFileKey, data: []byte("unusable")},
		{name: "good.bin", key: testFileKey, data: []byte("kept")},
	})
	client := newTestClient(server)
	dir := filepath.Join(t.TempDir(), "dest")

	err := client.Fetch(t.Context(), &bytes.Buffer{}, dir, []string{shareLink(testFolderKey)})
	if err == nil {
		t.Fatal("Fetch returned nil error")
	}
	if !strings.Contains(err.Error(), "not usable") {
		t.Errorf("error = %v, want it to name the unusable file", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "good.bin"))
	if err != nil {
		t.Fatalf("the file behind the failure was not written: %v", err)
	}
	if string(got) != "kept" {
		t.Errorf("good.bin = %q, want %q", got, "kept")
	}
}

func TestFetchReportsEveryFailure(t *testing.T) {
	t.Parallel()
	server := newShareServer(t, testFolderKey, []testFile{
		{name: "..", key: testFileKey, data: []byte("one")},
		{name: ".", key: testFileKey, data: []byte("two")},
	})
	client := newTestClient(server)

	err := client.Fetch(t.Context(), &bytes.Buffer{}, filepath.Join(t.TempDir(), "dest"),
		[]string{shareLink(testFolderKey)})
	if err == nil {
		t.Fatal("Fetch returned nil error")
	}
	if got := strings.Count(err.Error(), "not usable"); got != 2 {
		t.Errorf("error named %d failures, want 2: %v", got, err)
	}
}

func TestFetchReportsAnUnstatableDestination(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a path under a file as absent rather than as a fault")
	}
	// A file where the destination directory should be, so the stat fails with
	// something other than absence.
	dir := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
		t.Fatalf("set up the blocked destination: %v", err)
	}
	// The egest is unreachable, so a stat treated as absence would fail
	// differently and the test would not be proving anything.
	client := New(http.DefaultClient, "http://invalid.invalid", "http://invalid.invalid")

	_, err := client.fetchOne(t.Context(), io.Discard, dir, File{Name: "payload.bin", Version: dataVersion}, nil)
	if err == nil {
		t.Fatal("fetchOne returned nil error")
	}
	if !strings.Contains(err.Error(), "stat") {
		t.Errorf("error = %v, want it to report the stat failure", err)
	}
}
