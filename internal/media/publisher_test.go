package media

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakePublishRunner struct {
	names        []string
	args         [][]string
	concatInputs []string
}

func (f *fakePublishRunner) Run(_ context.Context, _ io.Writer, _ io.Writer, name string, args ...string) error {
	f.names = append(f.names, name)
	f.args = append(f.args, append([]string(nil), args...))
	if strings.Contains(name, "ffmpeg") {
		outputPath := args[len(args)-1]
		listPath := ""
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-i" && i+1 < len(args) {
				listPath = args[i+1]
				break
			}
		}
		listData, err := os.ReadFile(listPath)
		if err != nil {
			return err
		}
		f.concatInputs = append(f.concatInputs, string(listData))
		var merged strings.Builder
		for _, line := range strings.Split(string(listData), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "file '") || !strings.HasSuffix(line, "'") {
				continue
			}
			segmentPath := strings.TrimSuffix(strings.TrimPrefix(line, "file '"), "'")
			payload, readErr := os.ReadFile(segmentPath)
			if readErr != nil {
				return readErr
			}
			merged.Write(payload)
		}
		return os.WriteFile(outputPath, []byte(merged.String()), 0o644)
	}
	return nil
}

func TestBunnyChunkPublisherUploadsOnlyOnFinalize(t *testing.T) {
	uploadCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/videos"):
			_, _ = w.Write([]byte(`{"guid":"video-1"}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/videos/video-1"):
			uploadCalls++
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	runner := &fakePublishRunner{}
	dir := t.TempDir()
	publisher := NewBunnyChunkPublisher(BunnyChunkPublisherConfig{
		OutputDir:      dir,
		FFmpegBinary:   "ffmpeg",
		Runner:         runner,
		AggregateCount: 2,
		BaseURL:        api.URL,
		LibraryID:      "lib-1",
		APIKey:         "key",
		HTTPTimeout:    time.Second,
	})

	chunkA := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(chunkA, []byte("A"), 0o644); err != nil {
		t.Fatalf("write chunkA: %v", err)
	}
	if err := publisher.Publish(context.Background(), "str-1", ChunkRef{Reference: chunkA, CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("publish first chunk: %v", err)
	}
	if uploadCalls != 0 {
		t.Fatalf("uploadCalls = %d, want 0 before batch ready", uploadCalls)
	}

	chunkB := filepath.Join(dir, "b.mp4")
	if err := os.WriteFile(chunkB, []byte("B"), 0o644); err != nil {
		t.Fatalf("write chunkB: %v", err)
	}
	if err := publisher.Publish(context.Background(), "str-1", ChunkRef{Reference: chunkB, CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("publish second chunk: %v", err)
	}
	if uploadCalls != 0 {
		t.Fatalf("uploadCalls = %d, want 0 before finalize", uploadCalls)
	}
	if err := publisher.Finalize(context.Background(), "str-1", time.Now().UTC()); err != nil {
		t.Fatalf("finalize stream: %v", err)
	}
	if uploadCalls != 1 {
		t.Fatalf("uploadCalls = %d, want 1 after finalize", uploadCalls)
	}
	segmentsDir := filepath.Join(dir, "str-1", "segments")
	if _, err := os.Stat(segmentsDir); !os.IsNotExist(err) {
		t.Fatalf("segments dir should be removed after finalize, stat err=%v", err)
	}
}

func TestBunnyChunkPublisherConcatListUsesAbsolutePaths(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	testWD := t.TempDir()
	if err := os.Chdir(testWD); err != nil {
		t.Fatalf("chdir test wd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	uploadCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/videos"):
			_, _ = w.Write([]byte(`{"guid":"video-1"}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/videos/video-1"):
			uploadCalls++
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	runner := &fakePublishRunner{}
	publisher := NewBunnyChunkPublisher(BunnyChunkPublisherConfig{
		OutputDir:      "tmp/stream_chunks",
		FFmpegBinary:   "ffmpeg",
		Runner:         runner,
		AggregateCount: 2,
		BaseURL:        api.URL,
		LibraryID:      "lib-1",
		APIKey:         "key",
		HTTPTimeout:    time.Second,
	})

	if err := os.MkdirAll("tmp/input", 0o755); err != nil {
		t.Fatalf("mkdir input: %v", err)
	}
	chunkA := filepath.Join("tmp/input", "a.mp4")
	if err := os.WriteFile(chunkA, []byte("A"), 0o644); err != nil {
		t.Fatalf("write chunkA: %v", err)
	}
	if err := publisher.Publish(context.Background(), "str-1", ChunkRef{Reference: chunkA, CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("publish first chunk: %v", err)
	}

	chunkB := filepath.Join("tmp/input", "b.mp4")
	if err := os.WriteFile(chunkB, []byte("B"), 0o644); err != nil {
		t.Fatalf("write chunkB: %v", err)
	}
	if err := publisher.Publish(context.Background(), "str-1", ChunkRef{Reference: chunkB, CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("publish second chunk: %v", err)
	}
	if err := publisher.Finalize(context.Background(), "str-1", time.Now().UTC()); err != nil {
		t.Fatalf("finalize stream: %v", err)
	}
	if uploadCalls != 1 {
		t.Fatalf("uploadCalls = %d, want 1", uploadCalls)
	}
	if len(runner.concatInputs) == 0 {
		t.Fatalf("concatInputs is empty")
	}
	for _, line := range strings.Split(runner.concatInputs[len(runner.concatInputs)-1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "file '") {
			t.Fatalf("concat line = %q, want prefix file '", line)
		}
		pathValue := strings.TrimSuffix(strings.TrimPrefix(line, "file '"), "'")
		if !filepath.IsAbs(pathValue) {
			t.Fatalf("concat path = %q, want absolute path", pathValue)
		}
	}
}

func TestBunnyChunkPublisherListSegmentsSortsByCapturedTimestamp(t *testing.T) {
	dir := t.TempDir()
	segmentsDir := filepath.Join(dir, "segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("mkdir segments: %v", err)
	}

	files := []string{
		"20260326T120030_000000000.mp4",
		"20260326T120010_000000000.mp4",
		"20260326T120020_000000000.mp4",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(segmentsDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write segment %s: %v", name, err)
		}
	}

	publisher := NewBunnyChunkPublisher(BunnyChunkPublisherConfig{OutputDir: dir})
	got, err := publisher.listSegments(segmentsDir)
	if err != nil {
		t.Fatalf("listSegments() error = %v", err)
	}

	want := []string{
		filepath.Join(segmentsDir, "20260326T120010_000000000.mp4"),
		filepath.Join(segmentsDir, "20260326T120020_000000000.mp4"),
		filepath.Join(segmentsDir, "20260326T120030_000000000.mp4"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listSegments() = %#v, want %#v", got, want)
	}
}

func TestBunnyChunkPublisherListSegmentsSortsByIndex(t *testing.T) {
	dir := t.TempDir()
	segmentsDir := filepath.Join(dir, "segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("mkdir segments: %v", err)
	}
	files := []string{"000000003.mp4", "000000001.mp4", "000000002.mp4"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(segmentsDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write segment %s: %v", name, err)
		}
	}

	publisher := NewBunnyChunkPublisher(BunnyChunkPublisherConfig{OutputDir: dir})
	got, err := publisher.listSegments(segmentsDir)
	if err != nil {
		t.Fatalf("listSegments() error = %v", err)
	}

	want := []string{
		filepath.Join(segmentsDir, "000000001.mp4"),
		filepath.Join(segmentsDir, "000000002.mp4"),
		filepath.Join(segmentsDir, "000000003.mp4"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listSegments() = %#v, want %#v", got, want)
	}
}

func TestParseSegmentCapturedAt(t *testing.T) {
	got := parseSegmentCapturedAt("20260326T120010_123000000.mp4")
	want := time.Date(2026, 3, 26, 12, 0, 10, 123000000, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("parseSegmentCapturedAt() = %s, want %s", got, want)
	}
	if !parseSegmentCapturedAt("legacy.mp4").IsZero() {
		t.Fatalf("expected zero time for unparseable filename")
	}
}

func TestParseSegmentIndex(t *testing.T) {
	if got := parseSegmentIndex("000000007.mp4"); got != 7 {
		t.Fatalf("parseSegmentIndex() = %d, want 7", got)
	}
	if got := parseSegmentIndex("legacy.mp4"); got != 0 {
		t.Fatalf("parseSegmentIndex() = %d, want 0", got)
	}
}

func TestBunnyChunkPublisherCreateVideoTitleIncludesStreamerAndDayFolders(t *testing.T) {
	var title string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/videos"):
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if raw, ok := payload["title"].(string); ok {
				title = raw
			}
			_, _ = w.Write([]byte(`{"guid":"video-1"}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/videos/video-1"):
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	runner := &fakePublishRunner{}
	dir := t.TempDir()
	publisher := NewBunnyChunkPublisher(BunnyChunkPublisherConfig{
		OutputDir:    dir,
		FFmpegBinary: "ffmpeg",
		Runner:       runner,
		BaseURL:      api.URL,
		LibraryID:    "lib-1",
		APIKey:       "key",
		UsernameResolver: func(_ context.Context, _ string) (string, error) {
			return "best_streamer", nil
		},
		HTTPTimeout: time.Second,
	})

	chunkA := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(chunkA, []byte("A"), 0o644); err != nil {
		t.Fatalf("write chunkA: %v", err)
	}
	if err := publisher.Publish(context.Background(), "str-1", ChunkRef{Reference: chunkA, CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("publish chunk: %v", err)
	}
	at := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	if err := publisher.Finalize(context.Background(), "str-1", at); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if !strings.HasPrefix(title, "str-1_best_streamer/2026-04-10/") {
		t.Fatalf("title = %q, want folder prefix", title)
	}
}
