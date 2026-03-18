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
	"strings"
	"time"

	"go.uber.org/zap"
)

var (
	ErrStreamlinkNoData         = errors.New("streamlink capture produced no data")
	ErrStreamlinkChannelResolve = errors.New("failed to resolve streamlink channel")
	ErrStreamlinkAdBreak        = errors.New("streamlink capture paused by ad break")
)

var streamlinkSafeTokenPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

var streamlinkAdBreakMarkers = []string{
	"waiting for pre-roll ads to finish",
	"detected advertisement break",
	"filtering out segments and pausing stream output",
	"will skip ad segments",
}

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
	Quality        string
	CaptureTimeout time.Duration
	OutputDir      string
	URLTemplate    string
}

// StreamlinkCaptureAdapter captures live stream bytes via streamlink and stores each
// polling cycle into a local chunk file reference.
type StreamlinkCaptureAdapter struct {
	logger   *zap.Logger
	cfg      StreamlinkCaptureConfig
	resolver StreamlinkChannelResolver
	runner   StreamlinkCommandRunner
	nowFn    func() time.Time
}

func NewStreamlinkCaptureAdapter(cfg StreamlinkCaptureConfig, resolver StreamlinkChannelResolver, runner StreamlinkCommandRunner) *StreamlinkCaptureAdapter {
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		cfg.BinaryPath = "streamlink"
	}
	if strings.TrimSpace(cfg.Quality) == "" {
		cfg.Quality = "best"
	}
	if cfg.CaptureTimeout <= 0 {
		cfg.CaptureTimeout = 12 * time.Second
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
	return &StreamlinkCaptureAdapter{logger: zap.NewNop(), cfg: cfg, resolver: resolver, runner: runner, nowFn: time.Now}
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

	captureCtx, cancel := context.WithTimeout(ctx, a.cfg.CaptureTimeout)
	defer cancel()

	streamURL := fmt.Sprintf(a.cfg.URLTemplate, channel)
	args := []string{"--stdout", streamURL, a.cfg.Quality}

	var stderr strings.Builder
	logger.Info("executing streamlink capture", zap.String("streamerID", id), zap.String("binaryPath", a.cfg.BinaryPath), zap.String("streamURL", streamURL), zap.String("quality", a.cfg.Quality), zap.String("chunkPath", chunkPath))
	runErr := a.runner.Run(captureCtx, file, &stderr, a.cfg.BinaryPath, args...)

	stat, err := file.Stat()
	if err != nil {
		return ChunkRef{}, err
	}
	if stat.Size() <= 0 {
		trimmedStderr := strings.TrimSpace(stderr.String())
		if isStreamlinkAdBreak(trimmedStderr) {
			logger.Info("stream capture paused by ad break", zap.String("streamerID", id), zap.String("chunkPath", chunkPath), zap.String("stderr", trimmedStderr), zap.Error(runErr))
			if runErr != nil {
				return ChunkRef{}, fmt.Errorf("%w: %v (stderr=%s)", ErrStreamlinkAdBreak, runErr, trimmedStderr)
			}
			return ChunkRef{}, fmt.Errorf("%w (stderr=%s)", ErrStreamlinkAdBreak, trimmedStderr)
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
	return ChunkRef{Reference: chunkPath, CapturedAt: a.nowFn().UTC()}, nil
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
