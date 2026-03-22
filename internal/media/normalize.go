package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrChunkNormalizationFailed = errors.New("chunk normalization failed")

type ChunkNormalizer interface {
	Normalize(ctx context.Context, chunk ChunkRef) (ChunkRef, error)
}

type FFmpegChunkNormalizer struct {
	binaryPath string
	runner     StreamlinkCommandRunner
}

func NewFFmpegChunkNormalizer(binaryPath string, runner StreamlinkCommandRunner) *FFmpegChunkNormalizer {
	trimmed := strings.TrimSpace(binaryPath)
	if trimmed == "" {
		trimmed = "ffmpeg"
	}
	if runner == nil {
		runner = execStreamlinkRunner{}
	}
	return &FFmpegChunkNormalizer{binaryPath: trimmed, runner: runner}
}

func (n *FFmpegChunkNormalizer) Normalize(ctx context.Context, chunk ChunkRef) (ChunkRef, error) {
	path := strings.TrimSpace(chunk.Reference)
	if path == "" {
		return chunk, nil
	}
	if strings.EqualFold(filepath.Ext(path), ".mp4") {
		return chunk, nil
	}
	if !strings.EqualFold(filepath.Ext(path), ".ts") {
		return chunk, nil
	}

	outputPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".mp4"
	args := []string{
		"-y",
		"-i", path,
		"-c", "copy",
		"-movflags", "+faststart",
		outputPath,
	}

	var stderr strings.Builder
	if err := n.runner.Run(ctx, io.Discard, &stderr, n.binaryPath, args...); err != nil {
		return ChunkRef{}, fmt.Errorf("%w: remux %s -> %s: %v (stderr=%s)", ErrChunkNormalizationFailed, path, outputPath, err, strings.TrimSpace(stderr.String()))
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		return ChunkRef{}, fmt.Errorf("%w: stat normalized chunk %s: %v", ErrChunkNormalizationFailed, outputPath, err)
	}
	if stat.Size() == 0 {
		return ChunkRef{}, fmt.Errorf("%w: normalized chunk %s is empty", ErrChunkNormalizationFailed, outputPath)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ChunkRef{}, fmt.Errorf("%w: remove source chunk %s: %v", ErrChunkNormalizationFailed, path, err)
	}

	chunk.Reference = outputPath
	return chunk, nil
}
