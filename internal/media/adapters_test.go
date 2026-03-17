package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeChannelResolver struct {
	channel string
	err     error
}

func (f fakeChannelResolver) ResolveStreamlinkChannel(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.channel, nil
}

type fakeCommandRunner struct {
	err       error
	writeData []byte
	lastName  string
	lastArgs  []string
}

func (f *fakeCommandRunner) Run(_ context.Context, stdout io.Writer, _ io.Writer, name string, args ...string) error {
	f.lastName = name
	f.lastArgs = append([]string(nil), args...)
	if len(f.writeData) > 0 {
		_, _ = stdout.Write(f.writeData)
	}
	return f.err
}

func TestStreamlinkCaptureAdapterCaptureSuccess(t *testing.T) {
	outDir := t.TempDir()
	runner := &fakeCommandRunner{writeData: []byte("chunk-bytes")}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		BinaryPath:     "streamlink-bin",
		Quality:        "best",
		CaptureTimeout: 2 * time.Second,
		OutputDir:      outDir,
		URLTemplate:    "https://twitch.tv/%s",
	}, fakeChannelResolver{channel: "shroud"}, runner)

	chunk, err := adapter.Capture(context.Background(), "str_1")
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if chunk.Reference == "" {
		t.Fatal("expected chunk reference")
	}
	if runner.lastName != "streamlink-bin" {
		t.Fatalf("runner binary = %q", runner.lastName)
	}
	joined := strings.Join(runner.lastArgs, " ")
	if !strings.Contains(joined, "https://twitch.tv/shroud") {
		t.Fatalf("expected resolved channel in args, got %q", joined)
	}

	data, err := os.ReadFile(chunk.Reference)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "chunk-bytes" {
		t.Fatalf("unexpected chunk content: %q", string(data))
	}
	if !strings.HasPrefix(chunk.Reference, filepath.Join(outDir, "str_1")) {
		t.Fatalf("unexpected chunk path: %q", chunk.Reference)
	}
}

func TestStreamlinkCaptureAdapterAcceptsTimeoutWhenChunkCaptured(t *testing.T) {
	runner := &fakeCommandRunner{writeData: []byte("partial"), err: context.DeadlineExceeded}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{OutputDir: t.TempDir()}, nil, runner)

	if _, err := adapter.Capture(context.Background(), "str_2"); err != nil {
		t.Fatalf("expected timeout with captured bytes to be accepted, got %v", err)
	}
}

func TestStreamlinkCaptureAdapterFailsWithoutBytes(t *testing.T) {
	runner := &fakeCommandRunner{err: errors.New("streamlink failed")}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{OutputDir: t.TempDir()}, nil, runner)

	_, err := adapter.Capture(context.Background(), "str_3")
	if !errors.Is(err, ErrStreamlinkNoData) {
		t.Fatalf("expected ErrStreamlinkNoData, got %v", err)
	}
}
