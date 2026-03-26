package media

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakePublishRunner struct {
	names []string
	args  [][]string
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

func TestBunnyChunkPublisherAggregatesAndUploadsWhenBatchReady(t *testing.T) {
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
	if uploadCalls != 1 {
		t.Fatalf("uploadCalls = %d, want 1 after batch ready", uploadCalls)
	}
}
