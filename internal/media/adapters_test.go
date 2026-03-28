package media

import (
	"context"
	"errors"
	"fmt"
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
	names        []string
	argsHistory  [][]string
	runFn        func(name string, args ...string) error
	runWithIOFn  func(stdout io.Writer, stderr io.Writer, name string, args ...string) error
}

func (f *fakeCommandRunner) Run(_ context.Context, stdout io.Writer, stderr io.Writer, name string, args ...string) error {
	f.lastName = name
	f.lastArgs = append([]string(nil), args...)
	f.names = append(f.names, name)
	f.argsHistory = append(f.argsHistory, append([]string(nil), args...))
	if f.runWithIOFn != nil {
		return f.runWithIOFn(stdout, stderr, name, args...)
	}
	if f.runFn != nil {
		return f.runFn(name, args...)
	}
	if strings.Contains(name, "ffprobe") {
		_, _ = io.WriteString(stdout, `{"streams":[{"codec_name":"h264","width":1920,"height":1080}]}`)
		return nil
	}
	if strings.Contains(name, "ffmpeg") && len(args) > 0 {
		outputPath := args[len(args)-1]
		inputPath := ""
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-i" && i+1 < len(args) {
				inputPath = args[i+1]
				break
			}
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return err
		}
		return os.WriteFile(outputPath, data, 0o644)
	}
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
	if len(runner.names) < 2 || runner.names[0] != "streamlink-bin" {
		t.Fatalf("runner binaries = %#v", runner.names)
	}
	if got := runner.argsHistory[0][len(runner.argsHistory[0])-1]; got != defaultPreferredStreamQuality {
		t.Fatalf("runner quality = %q, want %q", got, defaultPreferredStreamQuality)
	}
	joined := strings.Join(runner.argsHistory[0], " ")
	if !strings.Contains(joined, "https://twitch.tv/shroud") {
		t.Fatalf("expected resolved channel in args, got %q", joined)
	}
	if !strings.Contains(joined, fmt.Sprintf("--stream-segmented-duration %d", int(minimumStreamlinkCaptureTimeout/time.Second))) {
		t.Fatalf("expected --stream-segmented-duration argument, got %q", joined)
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
	if filepath.Ext(chunk.Reference) != ".mp4" {
		t.Fatalf("expected normalized mp4 chunk, got %q", chunk.Reference)
	}
}

func TestStreamlinkCaptureAdapterFallsBackToHLSDurationWhenStreamSegmentedUnsupported(t *testing.T) {
	outDir := t.TempDir()
	attempt := 0
	runner := &fakeCommandRunner{
		runWithIOFn: func(stdout io.Writer, stderr io.Writer, name string, args ...string) error {
			if strings.Contains(name, "ffprobe") {
				_, _ = io.WriteString(stdout, `{"streams":[{"codec_name":"h264","width":1920,"height":1080}]}`)
				return nil
			}
			if strings.Contains(name, "ffmpeg") && len(args) > 0 {
				outputPath := args[len(args)-1]
				inputPath := ""
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "-i" && i+1 < len(args) {
						inputPath = args[i+1]
						break
					}
				}
				data, err := os.ReadFile(inputPath)
				if err != nil {
					return err
				}
				return os.WriteFile(outputPath, data, 0o644)
			}
			attempt++
			if attempt == 1 {
				_, _ = io.WriteString(stderr, "error: unrecognized arguments: --stream-segmented-duration")
				return errors.New("exit status 2")
			}
			_, _ = stdout.Write([]byte("chunk-bytes"))
			return nil
		},
	}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{OutputDir: outDir}, nil, runner)

	chunk, err := adapter.Capture(context.Background(), "str_fallback")
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if chunk.Reference == "" {
		t.Fatal("expected chunk reference")
	}
	if len(runner.argsHistory) < 3 {
		t.Fatalf("argsHistory length = %d, want at least 3 (streamlink retry + ffprobe + ffmpeg)", len(runner.argsHistory))
	}
	first := strings.Join(runner.argsHistory[0], " ")
	second := strings.Join(runner.argsHistory[1], " ")
	if !strings.Contains(first, fmt.Sprintf("--stream-segmented-duration %d", int(minimumStreamlinkCaptureTimeout/time.Second))) {
		t.Fatalf("first streamlink invocation = %q, want --stream-segmented-duration", first)
	}
	if !strings.Contains(second, fmt.Sprintf("--hls-duration %d", int(minimumStreamlinkCaptureTimeout/time.Second))) {
		t.Fatalf("second streamlink invocation = %q, want --hls-duration", second)
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
		CaptureTimeout: 5 * time.Second,
		OutputDir:      t.TempDir(),
	}, nil, &fakeCommandRunner{})

	if adapter.cfg.CaptureTimeout != minimumStreamlinkCaptureTimeout {
		t.Fatalf("CaptureTimeout = %s, want %s", adapter.cfg.CaptureTimeout, minimumStreamlinkCaptureTimeout)
	}
}

func TestNewStreamlinkCaptureAdapterEnablesContinuousModeForDefaultRunner(t *testing.T) {
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		OutputDir: t.TempDir(),
	}, nil, nil)
	if !adapter.continuous {
		t.Fatalf("expected continuous mode for default runner")
	}
}

func TestNewStreamlinkCaptureAdapterDisablesContinuousModeForCustomRunner(t *testing.T) {
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		OutputDir: t.TempDir(),
	}, nil, &fakeCommandRunner{})
	if adapter.continuous {
		t.Fatalf("expected non-continuous mode for custom runner")
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

func TestFFmpegChunkNormalizerRemuxesTSChunksToMP4(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "chunk.ts")
	if err := os.WriteFile(inputPath, []byte("transport-stream"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runner := &fakeCommandRunner{
		runWithIOFn: func(stdout io.Writer, _ io.Writer, name string, args ...string) error {
			if name == "ffprobe-bin" {
				_, _ = io.WriteString(stdout, `{"streams":[{"codec_name":"h264","width":1920,"height":1080}]}`)
				return nil
			}
			if name != "ffmpeg-bin" {
				t.Fatalf("runner binary = %q, want ffmpeg-bin", name)
			}
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "-bsf:v") || !strings.Contains(joined, "h264_metadata=crop_left=420:crop_right=420:crop_top=0:crop_bottom=0") {
				t.Fatalf("expected crop metadata bitstream filter args, got %q", joined)
			}
			if got := args[len(args)-1]; got != filepath.Join(dir, "chunk.mp4") {
				t.Fatalf("output path = %q", got)
			}
			if err := os.WriteFile(filepath.Join(dir, "chunk.mp4"), []byte("mp4-bytes"), 0o644); err != nil {
				t.Fatalf("WriteFile(normalized) error = %v", err)
			}
			return nil
		},
	}

	normalizer := NewFFmpegChunkNormalizer("ffmpeg-bin", runner)
	normalized, err := normalizer.Normalize(context.Background(), ChunkRef{Reference: inputPath})
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if normalized.Reference != filepath.Join(dir, "chunk.mp4") {
		t.Fatalf("normalized reference = %q", normalized.Reference)
	}
	if _, err := os.Stat(inputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected source ts file removal, err=%v", err)
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
