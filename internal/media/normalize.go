package media

import (
	"bytes"
	"context"
	"encoding/json"
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
	probePath  string
	runner     StreamlinkCommandRunner
}

func NewFFmpegChunkNormalizer(binaryPath string, runner StreamlinkCommandRunner) *FFmpegChunkNormalizer {
	trimmed := strings.TrimSpace(binaryPath)
	if trimmed == "" {
		trimmed = "ffmpeg"
	}
	probe := "ffprobe"
	if strings.Contains(trimmed, "ffmpeg") {
		probe = strings.Replace(trimmed, "ffmpeg", "ffprobe", 1)
	}
	if runner == nil {
		runner = execStreamlinkRunner{}
	}
	return &FFmpegChunkNormalizer{binaryPath: trimmed, probePath: probe, runner: runner}
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

	streamMeta, err := n.probeVideoStream(ctx, path)
	if err != nil {
		return ChunkRef{}, err
	}
	if streamMeta.CodecName != "h264" {
		return ChunkRef{}, fmt.Errorf("%w: crop without re-encoding supports only h264 input, got %q", ErrChunkNormalizationFailed, streamMeta.CodecName)
	}

	outputPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".mp4"
	left, right, top, bottom := centeredSquareCropOffsets(streamMeta.Width, streamMeta.Height)
	args := []string{
		"-y",
		"-i", path,
		"-map", "0:v:0",
		"-map", "0:a?",
		"-c", "copy",
		"-bsf:v", fmt.Sprintf("h264_metadata=crop_left=%d:crop_right=%d:crop_top=%d:crop_bottom=%d", left, right, top, bottom),
		"-movflags", "+faststart",
		outputPath,
	}

	var stderr strings.Builder
	if err := n.runner.Run(ctx, io.Discard, &stderr, n.binaryPath, args...); err != nil {
		return ChunkRef{}, fmt.Errorf("%w: normalize %s -> %s: %v (stderr=%s)", ErrChunkNormalizationFailed, path, outputPath, err, strings.TrimSpace(stderr.String()))
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

type ffprobeStreamMeta struct {
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type ffprobeOutput struct {
	Streams []ffprobeStreamMeta `json:"streams"`
}

func (n *FFmpegChunkNormalizer) probeVideoStream(ctx context.Context, path string) (ffprobeStreamMeta, error) {
	var stdout bytes.Buffer
	var stderr strings.Builder
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height",
		"-of", "json",
		path,
	}
	if err := n.runner.Run(ctx, &stdout, &stderr, n.probePath, args...); err != nil {
		return ffprobeStreamMeta{}, fmt.Errorf("%w: ffprobe %s: %v (stderr=%s)", ErrChunkNormalizationFailed, path, err, strings.TrimSpace(stderr.String()))
	}
	var parsed ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return ffprobeStreamMeta{}, fmt.Errorf("%w: parse ffprobe output: %v", ErrChunkNormalizationFailed, err)
	}
	if len(parsed.Streams) == 0 {
		return ffprobeStreamMeta{}, fmt.Errorf("%w: ffprobe returned no video streams", ErrChunkNormalizationFailed)
	}
	meta := parsed.Streams[0]
	if meta.Width <= 0 || meta.Height <= 0 {
		return ffprobeStreamMeta{}, fmt.Errorf("%w: invalid dimensions %dx%d", ErrChunkNormalizationFailed, meta.Width, meta.Height)
	}
	return meta, nil
}

func centeredSquareCropOffsets(width, height int) (left, right, top, bottom int) {
	if width <= 0 || height <= 0 || width == height {
		return 0, 0, 0, 0
	}
	if width > height {
		delta := width - height
		left = delta / 2
		right = delta - left
		return roundDownToEven(left), roundDownToEven(right), 0, 0
	}
	delta := height - width
	top = delta / 2
	bottom = delta - top
	return 0, 0, roundDownToEven(top), roundDownToEven(bottom)
}

func roundDownToEven(value int) int {
	if value <= 0 {
		return 0
	}
	return value - (value % 2)
}
