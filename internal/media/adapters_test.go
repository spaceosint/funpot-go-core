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
	err          error
	writeData    []byte
	stderrOutput string
	lastName     string
	lastArgs     []string
}

func (f *fakeCommandRunner) Run(_ context.Context, stdout io.Writer, stderr io.Writer, name string, args ...string) error {
	f.lastName = name
	f.lastArgs = append([]string(nil), args...)
	if len(f.writeData) > 0 {
		_, _ = stdout.Write(f.writeData)
	}
	if f.stderrOutput != "" {
		_, _ = io.WriteString(stderr, f.stderrOutput)
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
	if got := runner.lastArgs[len(runner.lastArgs)-1]; got != defaultPreferredStreamQuality {
		t.Fatalf("runner quality = %q, want %q", got, defaultPreferredStreamQuality)
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

func TestNewStreamlinkCaptureAdapterEnforcesMinimumCaptureTimeout(t *testing.T) {
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		CaptureTimeout: 12 * time.Second,
		OutputDir:      t.TempDir(),
	}, nil, &fakeCommandRunner{})

	if adapter.cfg.CaptureTimeout != minimumStreamlinkCaptureTimeout {
		t.Fatalf("CaptureTimeout = %s, want %s", adapter.cfg.CaptureTimeout, minimumStreamlinkCaptureTimeout)
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

func TestStreamlinkCaptureAdapterReturnsAdBreakErrorWhenAdsPauseOutput(t *testing.T) {
	outDir := t.TempDir()
	runner := &fakeCommandRunner{
		err: errors.New("signal: killed"),
		stderrOutput: strings.Join([]string{
			"[plugins.twitch][info] Will skip ad segments",
			"[plugins.twitch][info] Waiting for pre-roll ads to finish, be patient",
			"[plugins.twitch][info] Detected advertisement break of 15 seconds",
			"[stream.hls][info] Filtering out segments and pausing stream output",
		}, "\n"),
	}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{OutputDir: outDir}, nil, runner)

	_, err := adapter.Capture(context.Background(), "str_ads")
	if !errors.Is(err, ErrStreamlinkAdBreak) {
		t.Fatalf("expected ErrStreamlinkAdBreak, got %v", err)
	}
	files, readErr := os.ReadDir(filepath.Join(outDir, "str_ads"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("ReadDir() error = %v", readErr)
	}
	if len(files) != 0 {
		t.Fatalf("expected ad-break empty chunk to be removed, found %d files", len(files))
	}
}

func TestStreamlinkCaptureAdapterReturnsStreamEndedErrorWhenNoPlayableStreamsRemain(t *testing.T) {
	outDir := t.TempDir()
	runner := &fakeCommandRunner{
		err: errors.New("exit status 1"),
		stderrOutput: strings.Join([]string{
			"[cli][info] Found matching plugin twitch for URL https://twitch.tv/donacs",
			"error: No playable streams found on this URL: https://twitch.tv/donacs",
		}, "\n"),
	}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{OutputDir: outDir}, nil, runner)

	_, err := adapter.Capture(context.Background(), "str_offline")
	if !errors.Is(err, ErrStreamlinkStreamEnded) {
		t.Fatalf("expected ErrStreamlinkStreamEnded, got %v", err)
	}
	files, readErr := os.ReadDir(filepath.Join(outDir, "str_offline"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("ReadDir() error = %v", readErr)
	}
	if len(files) != 0 {
		t.Fatalf("expected offline empty chunk to be removed, found %d files", len(files))
	}
}

func TestNormalizeStreamlinkQualityPrefers1080p(t *testing.T) {
	for _, input := range []string{"", "best", " BEST "} {
		if got := normalizeStreamlinkQuality(input); got != defaultPreferredStreamQuality {
			t.Fatalf("normalizeStreamlinkQuality(%q) = %q, want %q", input, got, defaultPreferredStreamQuality)
		}
	}
	if got := normalizeStreamlinkQuality("480p"); got != "480p" {
		t.Fatalf("normalizeStreamlinkQuality(custom) = %q, want 480p", got)
	}
}
