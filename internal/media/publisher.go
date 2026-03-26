package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultBunnyBaseURL          = "https://video.bunnycdn.com"
	defaultChunkPublishBatchSize = 5
)

type BunnyChunkPublisherConfig struct {
	OutputDir      string
	FFmpegBinary   string
	Runner         StreamlinkCommandRunner
	AggregateCount int
	BaseURL        string
	LibraryID      string
	APIKey         string
	HTTPTimeout    time.Duration
}

type BunnyChunkPublisher struct {
	cfg    BunnyChunkPublisherConfig
	client *http.Client
}

func NewBunnyChunkPublisher(cfg BunnyChunkPublisherConfig) *BunnyChunkPublisher {
	if strings.TrimSpace(cfg.OutputDir) == "" {
		cfg.OutputDir = "tmp/stream_chunks"
	}
	if strings.TrimSpace(cfg.FFmpegBinary) == "" {
		cfg.FFmpegBinary = "ffmpeg"
	}
	if cfg.Runner == nil {
		cfg.Runner = execStreamlinkRunner{}
	}
	if cfg.AggregateCount <= 0 {
		cfg.AggregateCount = defaultChunkPublishBatchSize
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBunnyBaseURL
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 2 * time.Minute
	}
	return &BunnyChunkPublisher{cfg: cfg, client: &http.Client{Timeout: cfg.HTTPTimeout}}
}

func (p *BunnyChunkPublisher) Publish(ctx context.Context, streamerID string, chunk ChunkRef) error {
	if p == nil {
		return nil
	}
	if strings.TrimSpace(p.cfg.LibraryID) == "" || strings.TrimSpace(p.cfg.APIKey) == "" {
		return nil
	}
	chunkPath := strings.TrimSpace(chunk.Reference)
	if chunkPath == "" {
		return fmt.Errorf("publish chunk: empty chunk reference")
	}

	segmentsDir := filepath.Join(p.cfg.OutputDir, sanitizeToken(streamerID), "segments")
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		return err
	}
	segmentPath := filepath.Join(segmentsDir, filepath.Base(chunkPath))
	if err := os.Rename(chunkPath, segmentPath); err != nil {
		return err
	}

	segments, err := p.listSegments(segmentsDir)
	if err != nil {
		return err
	}
	if len(segments) < p.cfg.AggregateCount {
		return nil
	}

	selected := segments[:p.cfg.AggregateCount]
	windowPath, err := p.concatSegments(ctx, streamerID, segmentsDir, selected)
	if err != nil {
		return err
	}
	for _, segment := range selected {
		_ = os.Remove(segment)
	}
	defer os.Remove(windowPath) //nolint:errcheck

	videoID, err := p.createVideo(ctx, streamerID, chunk.CapturedAt)
	if err != nil {
		return err
	}
	if err := p.uploadVideo(ctx, videoID, windowPath); err != nil {
		return err
	}
	return nil
}

func (p *BunnyChunkPublisher) listSegments(segmentsDir string) ([]string, error) {
	entries, err := os.ReadDir(segmentsDir)
	if err != nil {
		return nil, err
	}
	segments := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".mp4") {
			continue
		}
		segments = append(segments, filepath.Join(segmentsDir, entry.Name()))
	}
	sort.Strings(segments)
	return segments, nil
}

func (p *BunnyChunkPublisher) concatSegments(ctx context.Context, streamerID, segmentsDir string, selected []string) (string, error) {
	listPath := filepath.Join(segmentsDir, fmt.Sprintf("concat_%s.txt", sanitizeToken(time.Now().UTC().Format(time.RFC3339Nano))))
	var body strings.Builder
	for _, segment := range selected {
		body.WriteString("file '")
		body.WriteString(strings.ReplaceAll(segment, "'", "'\\''"))
		body.WriteString("'\n")
	}
	if err := os.WriteFile(listPath, []byte(body.String()), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(listPath) //nolint:errcheck

	outputPath := filepath.Join(p.cfg.OutputDir, sanitizeToken(streamerID), fmt.Sprintf("window_%s.mp4", sanitizeToken(time.Now().UTC().Format("20060102T150405.000000000"))))
	var stderr strings.Builder
	args := []string{"-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", outputPath}
	if err := p.cfg.Runner.Run(ctx, io.Discard, &stderr, p.cfg.FFmpegBinary, args...); err != nil {
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("concat chunks failed: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}
	return outputPath, nil
}

type bunnyCreateVideoRequest struct {
	Title string `json:"title"`
}

type bunnyCreateVideoResponse struct {
	GUID string `json:"guid"`
}

func (p *BunnyChunkPublisher) createVideo(ctx context.Context, streamerID string, capturedAt time.Time) (string, error) {
	title := fmt.Sprintf("%s-%s", sanitizeToken(streamerID), sanitizeToken(capturedAt.UTC().Format(time.RFC3339Nano)))
	payload, err := json.Marshal(bunnyCreateVideoRequest{Title: title})
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/library/" + p.cfg.LibraryID + "/videos"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("AccessKey", p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("create bunny video failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var decoded bunnyCreateVideoResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if strings.TrimSpace(decoded.GUID) == "" {
		return "", fmt.Errorf("create bunny video: empty guid")
	}
	return decoded.GUID, nil
}

func (p *BunnyChunkPublisher) uploadVideo(ctx context.Context, videoID, videoPath string) error {
	file, err := os.Open(filepath.Clean(videoPath))
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck

	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/library/" + p.cfg.LibraryID + "/videos/" + videoID
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, file)
	if err != nil {
		return err
	}
	req.Header.Set("AccessKey", p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("upload bunny video failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
