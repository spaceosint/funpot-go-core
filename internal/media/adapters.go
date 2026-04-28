package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

var (
	ErrStreamlinkNoData         = errors.New("streamlink capture produced no data")
	ErrStreamlinkChannelResolve = errors.New("failed to resolve streamlink channel")
	ErrStreamlinkAdBreak        = errors.New("streamlink capture paused by ad break")
	ErrStreamlinkStreamEnded    = errors.New("streamlink capture ended because stream is unavailable")
)

var streamlinkSafeTokenPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

var streamlinkAdBreakMarkers = []string{
	"waiting for pre-roll ads to finish",
	"waiting for mid-roll ads to finish",
	"detected advertisement break",
	"filtering out segments and pausing stream output",
	"will skip ad segments",
}

var streamlinkEndedMarkers = []string{
	"no playable streams found on this url",
	"this stream is unavailable",
	"could not open stream",
}

const defaultPreferredStreamQuality = "1080p60,1080p,720p60,720p,936p60,936p,648p60,648p,480p,best"
const defaultStreamlinkCaptureTimeout = 30 * time.Second
const streamlinkCaptureShutdownGracePeriod = 5 * time.Second
const continuousSegmentStabilityWindow = 1500 * time.Millisecond

type StreamlinkChannelResolver interface {
	ResolveStreamlinkChannel(ctx context.Context, streamerID string) (string, error)
}

type StreamlinkCommandRunner interface {
	Run(ctx context.Context, stdout io.Writer, stderr io.Writer, name string, args ...string) error
}

type execStreamlinkRunner struct{}

func (r execStreamlinkRunner) Run(ctx context.Context, stdout io.Writer, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type StreamlinkCaptureConfig struct {
	BinaryPath     string
	FFmpegBinary   string
	Quality        string
	CaptureTimeout time.Duration
	OutputDir      string
	URLTemplate    string
}

// StreamlinkCaptureAdapter captures live stream bytes via streamlink and stores each
// polling cycle into a local chunk file reference.
type StreamlinkCaptureAdapter struct {
	logger     *zap.Logger
	cfg        StreamlinkCaptureConfig
	resolver   StreamlinkChannelResolver
	runner     StreamlinkCommandRunner
	normalizer ChunkNormalizer
	nowFn      func() time.Time
	continuous bool
	mu         sync.Mutex
	sessions   map[string]*continuousCaptureSession
}

type continuousCaptureSession struct {
	streamerID    string
	channel       string
	segmentsDir   string
	nextIndex     int
	started       bool
	lastErr       error
	streamlinkCmd *exec.Cmd
	ffmpegCmd     *exec.Cmd
}

func NewStreamlinkCaptureAdapter(cfg StreamlinkCaptureConfig, resolver StreamlinkChannelResolver, runner StreamlinkCommandRunner) *StreamlinkCaptureAdapter {
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		cfg.BinaryPath = "streamlink"
	}
	if strings.TrimSpace(cfg.FFmpegBinary) == "" {
		cfg.FFmpegBinary = "ffmpeg"
	}
	cfg.Quality = normalizeStreamlinkQuality(cfg.Quality)
	if cfg.CaptureTimeout <= 0 {
		cfg.CaptureTimeout = defaultStreamlinkCaptureTimeout
	}
	if strings.TrimSpace(cfg.OutputDir) == "" {
		cfg.OutputDir = "tmp/stream_chunks"
	}
	if strings.TrimSpace(cfg.URLTemplate) == "" {
		cfg.URLTemplate = "https://twitch.tv/%s"
	}
	if runner == nil {
		runner = execStreamlinkRunner{}
	}
	return &StreamlinkCaptureAdapter{
		logger:     zap.NewNop(),
		cfg:        cfg,
		resolver:   resolver,
		runner:     runner,
		normalizer: NewFFmpegChunkNormalizer(cfg.FFmpegBinary, runner),
		nowFn:      time.Now,
		continuous: false,
		sessions:   make(map[string]*continuousCaptureSession),
	}
}

func (a *StreamlinkCaptureAdapter) SetLogger(logger *zap.Logger) {
	if a == nil {
		return
	}
	if logger == nil {
		a.logger = zap.NewNop()
		return
	}
	a.logger = logger
}

func (a *StreamlinkCaptureAdapter) Capture(ctx context.Context, streamerID string) (ChunkRef, error) {
	return a.CaptureWithDuration(ctx, streamerID, a.cfg.CaptureTimeout)
}

func (a *StreamlinkCaptureAdapter) CaptureWithDuration(ctx context.Context, streamerID string, duration time.Duration) (ChunkRef, error) {
	if duration <= 0 {
		duration = a.cfg.CaptureTimeout
	}
	if a.continuous {
		return a.captureContinuous(ctx, streamerID, duration)
	}
	return a.captureSingle(ctx, streamerID, duration)
}

func (a *StreamlinkCaptureAdapter) captureSingle(ctx context.Context, streamerID string, captureTimeout time.Duration) (ChunkRef, error) {
	logger := a.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	id := strings.TrimSpace(streamerID)
	if id == "" {
		logger.Warn("stream capture rejected empty streamer id")
		return ChunkRef{}, ErrStreamerIDRequired
	}
	logger.Info("starting stream capture", zap.String("streamerID", id))

	channel := id
	if a.resolver != nil {
		resolved, err := a.resolver.ResolveStreamlinkChannel(ctx, id)
		if err != nil {
			logger.Error("failed to resolve streamlink channel", zap.String("streamerID", id), zap.Error(err))
			return ChunkRef{}, fmt.Errorf("%w: %v", ErrStreamlinkChannelResolve, err)
		}
		channel = strings.TrimSpace(resolved)
	}
	if channel == "" {
		logger.Warn("stream capture resolved empty channel", zap.String("streamerID", id))
		return ChunkRef{}, fmt.Errorf("%w: empty channel", ErrStreamlinkChannelResolve)
	}
	logger.Info("stream capture channel resolved", zap.String("streamerID", id), zap.String("channel", channel))

	chunkDir := filepath.Join(a.cfg.OutputDir, sanitizeToken(id))
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return ChunkRef{}, err
	}

	stamp := a.nowFn().UTC().Format("20060102T150405.000000000")
	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%s.ts", sanitizeToken(stamp)))
	file, err := os.Create(chunkPath)
	if err != nil {
		return ChunkRef{}, err
	}
	defer file.Close() //nolint:errcheck

	captureCtx, cancel := context.WithTimeout(ctx, captureTimeout+streamlinkCaptureShutdownGracePeriod)
	defer cancel()

	streamURL := fmt.Sprintf(a.cfg.URLTemplate, channel)
	args := []string{"--stdout", "--ffmpeg-fout", "mp4", "--stream-segmented-duration", formatStreamlinkDurationArg(captureTimeout), streamURL, a.cfg.Quality}

	var stderr strings.Builder
	logger.Info("executing streamlink capture", zap.String("streamerID", id), zap.String("binaryPath", a.cfg.BinaryPath), zap.String("streamURL", streamURL), zap.String("quality", a.cfg.Quality), zap.String("chunkPath", chunkPath))
	runErr := a.runner.Run(captureCtx, file, &stderr, a.cfg.BinaryPath, args...)
	if runErr != nil && isStreamlinkUnknownOption(stderr.String(), "--stream-segmented-duration") {
		logger.Info("streamlink does not support --stream-segmented-duration; retrying with --hls-duration", zap.String("streamerID", id), zap.String("binaryPath", a.cfg.BinaryPath))
		if err := file.Truncate(0); err != nil {
			return ChunkRef{}, err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return ChunkRef{}, err
		}
		stderr.Reset()
		args = []string{"--stdout", "--ffmpeg-fout", "mp4", "--hls-duration", formatStreamlinkDurationArg(captureTimeout), streamURL, a.cfg.Quality}
		runErr = a.runner.Run(captureCtx, file, &stderr, a.cfg.BinaryPath, args...)
	}
	if runErr != nil && isStreamlinkUnknownOption(stderr.String(), "--ffmpeg-fout") {
		logger.Info("streamlink does not support --ffmpeg-fout; retrying with transport stream output", zap.String("streamerID", id), zap.String("binaryPath", a.cfg.BinaryPath))
		if err := file.Truncate(0); err != nil {
			return ChunkRef{}, err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return ChunkRef{}, err
		}
		stderr.Reset()
		args = []string{"--stdout", "--stream-segmented-duration", formatStreamlinkDurationArg(captureTimeout), streamURL, a.cfg.Quality}
		runErr = a.runner.Run(captureCtx, file, &stderr, a.cfg.BinaryPath, args...)
		if runErr != nil && isStreamlinkUnknownOption(stderr.String(), "--stream-segmented-duration") {
			logger.Info("streamlink does not support --stream-segmented-duration; retrying with --hls-duration", zap.String("streamerID", id), zap.String("binaryPath", a.cfg.BinaryPath))
			if err := file.Truncate(0); err != nil {
				return ChunkRef{}, err
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return ChunkRef{}, err
			}
			stderr.Reset()
			args = []string{"--stdout", "--hls-duration", formatStreamlinkDurationArg(captureTimeout), streamURL, a.cfg.Quality}
			runErr = a.runner.Run(captureCtx, file, &stderr, a.cfg.BinaryPath, args...)
		}
	}

	stat, err := file.Stat()
	if err != nil {
		return ChunkRef{}, err
	}
	if stat.Size() <= 0 {
		trimmedStderr := strings.TrimSpace(stderr.String())
		_ = os.Remove(chunkPath)
		if isStreamlinkAdBreak(trimmedStderr) {
			logger.Info("stream capture paused by ad break", zap.String("streamerID", id), zap.String("chunkPath", chunkPath), zap.String("stderr", trimmedStderr), zap.Error(runErr))
			if runErr != nil {
				return ChunkRef{}, fmt.Errorf("%w: %v (stderr=%s)", ErrStreamlinkAdBreak, runErr, trimmedStderr)
			}
			return ChunkRef{}, fmt.Errorf("%w (stderr=%s)", ErrStreamlinkAdBreak, trimmedStderr)
		}
		if isStreamlinkEnded(trimmedStderr) {
			logger.Info("stream capture skipped because stream is unavailable", zap.String("streamerID", id), zap.String("chunkPath", chunkPath), zap.String("stderr", trimmedStderr), zap.Error(runErr))
			if runErr != nil {
				return ChunkRef{}, fmt.Errorf("%w: %v (stderr=%s)", ErrStreamlinkStreamEnded, runErr, trimmedStderr)
			}
			return ChunkRef{}, fmt.Errorf("%w (stderr=%s)", ErrStreamlinkStreamEnded, trimmedStderr)
		}
		logger.Warn("stream capture produced empty chunk", zap.String("streamerID", id), zap.String("chunkPath", chunkPath), zap.String("stderr", trimmedStderr), zap.Error(runErr))
		if runErr != nil {
			return ChunkRef{}, fmt.Errorf("%w: %v (stderr=%s)", ErrStreamlinkNoData, runErr, trimmedStderr)
		}
		return ChunkRef{}, fmt.Errorf("%w (stderr=%s)", ErrStreamlinkNoData, trimmedStderr)
	}

	if runErr != nil && !errors.Is(captureCtx.Err(), context.DeadlineExceeded) && !errors.Is(runErr, context.DeadlineExceeded) {
		logger.Error("streamlink capture command failed", zap.String("streamerID", id), zap.String("stderr", strings.TrimSpace(stderr.String())), zap.Error(runErr))
		return ChunkRef{}, fmt.Errorf("streamlink command failed: %w (stderr=%s)", runErr, strings.TrimSpace(stderr.String()))
	}

	logger.Info("stream capture completed", zap.String("streamerID", id), zap.String("chunkPath", chunkPath), zap.Int64("bytes", stat.Size()))
	chunk := ChunkRef{Reference: chunkPath, CapturedAt: a.nowFn().UTC()}
	if a.normalizer == nil {
		return chunk, nil
	}
	normalized, err := a.normalizer.Normalize(ctx, chunk)
	if err != nil {
		logger.Error("stream chunk normalization failed", zap.String("streamerID", id), zap.String("chunkPath", chunkPath), zap.Error(err))
		return ChunkRef{}, err
	}
	if normalized.Reference != chunk.Reference {
		logger.Info("stream chunk normalized", zap.String("streamerID", id), zap.String("sourceChunkPath", chunk.Reference), zap.String("normalizedChunkPath", normalized.Reference))
	}
	return normalized, nil
}

func (a *StreamlinkCaptureAdapter) captureContinuous(ctx context.Context, streamerID string, captureTimeout time.Duration) (ChunkRef, error) {
	logger := a.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	id := strings.TrimSpace(streamerID)
	if id == "" {
		return ChunkRef{}, ErrStreamerIDRequired
	}

	channel := id
	if a.resolver != nil {
		resolved, err := a.resolver.ResolveStreamlinkChannel(ctx, id)
		if err != nil {
			return ChunkRef{}, fmt.Errorf("%w: %v", ErrStreamlinkChannelResolve, err)
		}
		channel = strings.TrimSpace(resolved)
	}
	if channel == "" {
		return ChunkRef{}, fmt.Errorf("%w: empty channel", ErrStreamlinkChannelResolve)
	}

	session, err := a.ensureContinuousSession(id, channel)
	if err != nil {
		return ChunkRef{}, err
	}
	if session.lastErr != nil {
		return ChunkRef{}, session.lastErr
	}

	targetIndex := session.nextIndex
	deadline := time.Now().Add(captureTimeout + streamlinkCaptureShutdownGracePeriod)
	lastObservedSize := int64(-1)
	var lastSizeChangedAt time.Time
	for {
		select {
		case <-ctx.Done():
			return ChunkRef{}, ctx.Err()
		default:
		}
		segmentPath := filepath.Join(session.segmentsDir, fmt.Sprintf("%09d.mp4", targetIndex))
		info, statErr := os.Stat(segmentPath)
		nextSegmentPath := filepath.Join(session.segmentsDir, fmt.Sprintf("%09d.mp4", targetIndex+1))
		nextInfo, nextErr := os.Stat(nextSegmentPath)
		segmentFinalized := nextErr == nil && nextInfo.Size() > 0
		finalizedByStability := false
		if statErr == nil && info.Size() > 0 {
			if info.Size() != lastObservedSize {
				lastObservedSize = info.Size()
				lastSizeChangedAt = time.Now()
			}
			stableByObservedSize := !lastSizeChangedAt.IsZero() && time.Since(lastSizeChangedAt) >= continuousSegmentStabilityWindow
			stableByModTime := time.Since(info.ModTime()) >= continuousSegmentStabilityWindow
			nearDeadline := time.Until(deadline) <= continuousSegmentStabilityWindow
			if !segmentFinalized && stableByObservedSize && stableByModTime && nearDeadline {
				segmentFinalized = true
				finalizedByStability = true
			}
		}
		if statErr == nil && info.Size() > 0 && segmentFinalized {
			chunkPath := filepath.Join(filepath.Dir(session.segmentsDir), fmt.Sprintf("%s.mp4", sanitizeToken(fmt.Sprintf("%09d", targetIndex))))
			if finalizedByStability {
				if err := copyFile(segmentPath, chunkPath); err != nil {
					return ChunkRef{}, err
				}
			} else {
				if err := os.Rename(segmentPath, chunkPath); err != nil {
					return ChunkRef{}, err
				}
			}
			session.nextIndex++
			return ChunkRef{Reference: chunkPath, CapturedAt: a.nowFn().UTC()}, nil
		}
		if statErr != nil {
			lastObservedSize = -1
			lastSizeChangedAt = time.Time{}
		}
		if time.Now().After(deadline) {
			return ChunkRef{}, fmt.Errorf("%w: no continuous segment available before deadline", ErrStreamlinkNoData)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (a *StreamlinkCaptureAdapter) ensureContinuousSession(streamerID, channel string) (*continuousCaptureSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	session, ok := a.sessions[streamerID]
	if !ok {
		streamerDir := filepath.Join(a.cfg.OutputDir, sanitizeToken(streamerID))
		segmentsDir := filepath.Join(streamerDir, "live_segments")
		session = &continuousCaptureSession{
			streamerID:  streamerID,
			channel:     channel,
			segmentsDir: segmentsDir,
			nextIndex:   1,
		}
		a.sessions[streamerID] = session
	}
	if session.started {
		return session, nil
	}

	if err := os.MkdirAll(session.segmentsDir, 0o755); err != nil {
		return nil, err
	}
	for _, entry := range []string{"*.ts", "*.mp4"} {
		matches, _ := filepath.Glob(filepath.Join(session.segmentsDir, entry))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	session.started = true
	session.lastErr = nil
	go a.runContinuousSession(session)
	return session, nil
}

func (a *StreamlinkCaptureAdapter) runContinuousSession(session *continuousCaptureSession) {
	streamURL := fmt.Sprintf(a.cfg.URLTemplate, session.channel)

	streamlinkArgs := []string{"--stdout", "--ffmpeg-fout", "mp4", streamURL, a.cfg.Quality}
	ffmpegOutputPattern := filepath.Join(session.segmentsDir, "%09d.mp4")
	ffmpegArgs := []string{
		"-y",
		"-i", "pipe:0",
		"-c", "copy",
		"-f", "segment",
		"-segment_format", "mp4",
		"-segment_time", formatStreamlinkDurationArg(a.cfg.CaptureTimeout),
		"-segment_start_number", "1",
		"-reset_timestamps", "1",
		"-movflags", "+faststart",
		ffmpegOutputPattern,
	}

	streamlinkCmd := exec.Command(a.cfg.BinaryPath, streamlinkArgs...)
	ffmpegCmd := exec.Command(a.cfg.FFmpegBinary, ffmpegArgs...)

	stdout, err := streamlinkCmd.StdoutPipe()
	if err != nil {
		a.setSessionError(session.streamerID, err)
		return
	}
	ffmpegCmd.Stdin = stdout
	var streamlinkErrBuf, ffmpegErrBuf strings.Builder
	streamlinkCmd.Stderr = &streamlinkErrBuf
	ffmpegCmd.Stderr = &ffmpegErrBuf

	if err := ffmpegCmd.Start(); err != nil {
		a.setSessionError(session.streamerID, err)
		return
	}
	if err := streamlinkCmd.Start(); err != nil {
		_ = ffmpegCmd.Process.Kill()
		a.setSessionError(session.streamerID, err)
		return
	}

	_ = streamlinkCmd.Wait()
	_ = ffmpegCmd.Wait()
	sessionErr := strings.TrimSpace(streamlinkErrBuf.String() + " " + ffmpegErrBuf.String())
	if sessionErr == "" {
		sessionErr = "continuous capture session exited"
	}
	a.setSessionError(session.streamerID, fmt.Errorf("continuous capture stopped: %s", sessionErr))
}

func (a *StreamlinkCaptureAdapter) setSessionError(streamerID string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[streamerID]
	if !ok {
		return
	}
	session.lastErr = err
	session.started = false
}

func formatStreamlinkDurationArg(value time.Duration) string {
	seconds := int(value.Round(time.Second) / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func isStreamlinkUnknownOption(stderr, option string) bool {
	normalized := strings.ToLower(strings.TrimSpace(stderr))
	if normalized == "" {
		return false
	}
	needle := strings.ToLower(strings.TrimSpace(option))
	if needle == "" || !strings.Contains(normalized, needle) {
		return false
	}
	return strings.Contains(normalized, "unrecognized arguments") ||
		strings.Contains(normalized, "unknown option") ||
		strings.Contains(normalized, "no such option")
}

func sanitizeToken(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	replaced := streamlinkSafeTokenPattern.ReplaceAllString(trimmed, "_")
	if replaced == "" {
		return "unknown"
	}
	return replaced
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func normalizeStreamlinkQuality(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "best") {
		return defaultPreferredStreamQuality
	}
	return trimmed
}

// PromptedStageClassifier is a deterministic baseline classifier that accepts
// stage prompt context and returns a generic placeholder until Gemini integration is wired.
type PromptedStageClassifier struct{}

func (c PromptedStageClassifier) Classify(_ context.Context, input StageRequest) (StageClassification, error) {
	if strings.TrimSpace(input.Prompt.Template) == "" {
		return StageClassification{Label: "uncertain", Confidence: 0.1}, nil
	}
	return StageClassification{Label: "ok", Confidence: 0.75}, nil
}

func isStreamlinkAdBreak(stderr string) bool {
	normalized := strings.ToLower(strings.TrimSpace(stderr))
	if normalized == "" {
		return false
	}
	for _, marker := range streamlinkAdBreakMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func isStreamlinkEnded(stderr string) bool {
	normalized := strings.ToLower(strings.TrimSpace(stderr))
	if normalized == "" {
		return false
	}
	for _, marker := range streamlinkEndedMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
