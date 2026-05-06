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

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory to %q: %v", originalDir, err)
		}
	})
}

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
	concatInputs []string
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
		isConcat := false
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-f" && i+1 < len(args) && args[i+1] == "concat" {
				isConcat = true
			}
			if args[i] == "-i" && i+1 < len(args) {
				inputPath = args[i+1]
				break
			}
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return err
		}
		if isConcat {
			f.concatInputs = append(f.concatInputs, string(data))
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
	if !strings.Contains(joined, "--stream-segmented-duration 2") {
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
	if !strings.Contains(first, "--stream-segmented-duration 30") {
		t.Fatalf("first streamlink invocation = %q, want --stream-segmented-duration", first)
	}
	if !strings.Contains(second, "--hls-duration 30") {
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

func TestNewStreamlinkCaptureAdapterKeepsConfiguredCaptureTimeout(t *testing.T) {
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		CaptureTimeout: 5 * time.Second,
		OutputDir:      t.TempDir(),
	}, nil, &fakeCommandRunner{})

	if adapter.cfg.CaptureTimeout != 5*time.Second {
		t.Fatalf("CaptureTimeout = %s, want 5s", adapter.cfg.CaptureTimeout)
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

func TestCleanupContinuousSessionFilesRemovesStaleLocalArtifacts(t *testing.T) {
	streamerDir := t.TempDir()
	segmentsDir := filepath.Join(streamerDir, "live_segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	staleFiles := []string{
		filepath.Join(streamerDir, "000000001-000000003.mp4"),
		filepath.Join(streamerDir, "old.ts"),
		filepath.Join(streamerDir, "concat_old.txt"),
		filepath.Join(segmentsDir, "000000001.mp4"),
		filepath.Join(segmentsDir, "old.ts"),
		filepath.Join(segmentsDir, "concat_old.txt"),
	}
	for _, path := range staleFiles {
		if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	keepPath := filepath.Join(streamerDir, "keep.json")
	if err := os.WriteFile(keepPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile(keep) error = %v", err)
	}

	cleanupContinuousSessionFiles(streamerDir, segmentsDir)

	for _, path := range staleFiles {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected stale artifact %s to be removed, err=%v", path, err)
		}
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("expected unrelated file to remain, err=%v", err)
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

func TestStreamlinkCaptureAdapterContinuousAcceptsStableSegmentWithoutNextChunk(t *testing.T) {
	outDir := t.TempDir()
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		OutputDir: outDir,
	}, nil, nil)
	adapter.cfg.CaptureTimeout = 2 * time.Second

	segmentsDir := filepath.Join(outDir, "str_live", "live_segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	segmentPath := filepath.Join(segmentsDir, "000000001.mp4")
	if err := os.WriteFile(segmentPath, []byte("segment-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	past := time.Now().Add(-3 * time.Second)
	if err := os.Chtimes(segmentPath, past, past); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	adapter.sessions["str_live"] = &continuousCaptureSession{
		streamerID:  "str_live",
		channel:     "live_channel",
		segmentsDir: segmentsDir,
		nextIndex:   1,
		started:     true,
	}

	chunk, err := adapter.captureContinuous(context.Background(), "str_live", time.Second)
	if err != nil {
		t.Fatalf("captureContinuous() error = %v", err)
	}
	if chunk.Reference == "" {
		t.Fatal("expected chunk reference")
	}
	if filepath.Base(chunk.Reference) != "000000001.mp4" {
		t.Fatalf("chunk path = %q, want renamed stable segment", chunk.Reference)
	}
	if _, err := os.Stat(segmentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected consumed source segment to be removed, err=%v", err)
	}
}

func TestStreamlinkCaptureAdapterContinuousUsesRequestedDurationWithoutSkippingSegments(t *testing.T) {
	outDir := t.TempDir()
	runner := &fakeCommandRunner{}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		OutputDir:    outDir,
		FFmpegBinary: "ffmpeg-bin",
	}, nil, runner)

	segmentsDir := filepath.Join(outDir, "str_live", "live_segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for i := 1; i <= 6; i++ {
		segmentPath := filepath.Join(segmentsDir, fmt.Sprintf("%09d.mp4", i))
		if err := os.WriteFile(segmentPath, []byte(fmt.Sprintf("segment-%d", i)), 0o644); err != nil {
			t.Fatalf("WriteFile(%d) error = %v", i, err)
		}
	}

	adapter.sessions["str_live"] = &continuousCaptureSession{
		streamerID:  "str_live",
		channel:     "live_channel",
		segmentsDir: segmentsDir,
		nextIndex:   1,
		started:     true,
	}

	first, err := adapter.captureContinuous(context.Background(), "str_live", 3*time.Second)
	if err != nil {
		t.Fatalf("first captureContinuous() error = %v", err)
	}
	if filepath.Base(first.Reference) != "000000001-000000003.mp4" {
		t.Fatalf("first chunk path = %q", first.Reference)
	}
	if adapter.sessions["str_live"].nextIndex != 4 {
		t.Fatalf("nextIndex after first capture = %d, want 4", adapter.sessions["str_live"].nextIndex)
	}

	second, err := adapter.captureContinuous(context.Background(), "str_live", 2*time.Second)
	if err != nil {
		t.Fatalf("second captureContinuous() error = %v", err)
	}
	if filepath.Base(second.Reference) != "000000004-000000005.mp4" {
		t.Fatalf("second chunk path = %q", second.Reference)
	}
	if adapter.sessions["str_live"].nextIndex != 6 {
		t.Fatalf("nextIndex after second capture = %d, want 6", adapter.sessions["str_live"].nextIndex)
	}

	for i := 1; i <= 5; i++ {
		segmentPath := filepath.Join(segmentsDir, fmt.Sprintf("%09d.mp4", i))
		if _, err := os.Stat(segmentPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected consumed segment %d to be removed, err=%v", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(segmentsDir, "000000006.mp4")); err != nil {
		t.Fatalf("expected unused segment 6 to remain, err=%v", err)
	}

	if len(runner.argsHistory) != 2 {
		t.Fatalf("ffmpeg concat invocations = %d, want 2", len(runner.argsHistory))
	}
	for _, args := range runner.argsHistory {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-f concat") || !strings.Contains(joined, "-c copy") {
			t.Fatalf("expected concat demuxer with stream copy, got %q", joined)
		}
	}
}

func TestStreamlinkCaptureAdapterContinuousAssemblesStableRangeWithoutNextSegment(t *testing.T) {
	outDir := t.TempDir()
	runner := &fakeCommandRunner{}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		OutputDir:    outDir,
		FFmpegBinary: "ffmpeg-bin",
	}, nil, runner)

	segmentsDir := filepath.Join(outDir, "str_live", "live_segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	past := time.Now().Add(-2 * continuousSegmentStabilityWindow)
	for i := 1; i <= 3; i++ {
		segmentPath := filepath.Join(segmentsDir, fmt.Sprintf("%09d.mp4", i))
		if err := os.WriteFile(segmentPath, []byte(fmt.Sprintf("segment-%d", i)), 0o644); err != nil {
			t.Fatalf("WriteFile(%d) error = %v", i, err)
		}
		if err := os.Chtimes(segmentPath, past, past); err != nil {
			t.Fatalf("Chtimes(%d) error = %v", i, err)
		}
	}

	adapter.sessions["str_live"] = &continuousCaptureSession{
		streamerID:  "str_live",
		channel:     "live_channel",
		segmentsDir: segmentsDir,
		nextIndex:   1,
		started:     true,
	}

	chunk, err := adapter.captureContinuous(context.Background(), "str_live", 3*time.Second)
	if err != nil {
		t.Fatalf("captureContinuous() error = %v", err)
	}
	if filepath.Base(chunk.Reference) != "000000001-000000003.mp4" {
		t.Fatalf("chunk path = %q", chunk.Reference)
	}
	if adapter.sessions["str_live"].nextIndex != 4 {
		t.Fatalf("nextIndex after capture = %d, want 4", adapter.sessions["str_live"].nextIndex)
	}
}

func TestStreamlinkCaptureAdapterContinuousConcatListUsesAbsoluteSegmentPaths(t *testing.T) {
	chdirForTest(t, t.TempDir())
	outDir := filepath.Join("tmp", "stream_chunks")
	runner := &fakeCommandRunner{}
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		OutputDir:    outDir,
		FFmpegBinary: "ffmpeg-bin",
	}, nil, runner)

	segmentsDir := filepath.Join(outDir, "str_live", "live_segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for i := 1; i <= 3; i++ {
		segmentPath := filepath.Join(segmentsDir, fmt.Sprintf("%09d.mp4", i))
		if err := os.WriteFile(segmentPath, []byte(fmt.Sprintf("segment-%d", i)), 0o644); err != nil {
			t.Fatalf("WriteFile(%d) error = %v", i, err)
		}
	}

	adapter.sessions["str_live"] = &continuousCaptureSession{
		streamerID:  "str_live",
		channel:     "live_channel",
		segmentsDir: segmentsDir,
		nextIndex:   1,
		started:     true,
	}

	if _, err := adapter.captureContinuous(context.Background(), "str_live", 2*time.Second); err != nil {
		t.Fatalf("captureContinuous() error = %v", err)
	}
	if len(runner.concatInputs) != 1 {
		t.Fatalf("concat inputs = %d, want 1", len(runner.concatInputs))
	}
	for _, line := range strings.Split(strings.TrimSpace(runner.concatInputs[0]), "\n") {
		path := strings.TrimSuffix(strings.TrimPrefix(line, "file '"), "'")
		if !filepath.IsAbs(path) {
			t.Fatalf("concat segment path = %q, want absolute path", path)
		}
		if strings.Contains(path, filepath.Join("live_segments", outDir)) {
			t.Fatalf("concat segment path repeats the relative output directory: %q", path)
		}
	}
}

func TestStreamlinkCaptureAdapterContinuousStableSegmentWaitsNearDeadline(t *testing.T) {
	outDir := t.TempDir()
	adapter := NewStreamlinkCaptureAdapter(StreamlinkCaptureConfig{
		OutputDir: outDir,
	}, nil, nil)
	adapter.cfg.CaptureTimeout = 10 * time.Second

	segmentsDir := filepath.Join(outDir, "str_live", "live_segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	segmentPath := filepath.Join(segmentsDir, "000000001.mp4")
	if err := os.WriteFile(segmentPath, []byte("segment-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	past := time.Now().Add(-3 * time.Second)
	if err := os.Chtimes(segmentPath, past, past); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	adapter.sessions["str_live"] = &continuousCaptureSession{
		streamerID:  "str_live",
		channel:     "live_channel",
		segmentsDir: segmentsDir,
		nextIndex:   1,
		started:     true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := adapter.captureContinuous(ctx, "str_live", 30*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("captureContinuous() error = %v, want context deadline exceeded while far from capture deadline", err)
	}
}
