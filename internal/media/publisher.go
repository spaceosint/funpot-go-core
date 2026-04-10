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
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBunnyBaseURL          = "https://video.bunnycdn.com"
	defaultBunnyPlaybackBaseURL  = "https://player.mediadelivery.net"
	defaultChunkPublishBatchSize = 5
)

type BunnyChunkPublisherConfig struct {
	OutputDir        string
	FFmpegBinary     string
	Runner           StreamlinkCommandRunner
	AggregateCount   int
	BaseURL          string
	LibraryID        string
	APIKey           string
	HTTPTimeout      time.Duration
	UsernameResolver func(ctx context.Context, streamerID string) (string, error)
	VideoStore       UploadedVideoStore
}

type BunnyChunkPublisher struct {
	cfg              BunnyChunkPublisherConfig
	client           *http.Client
	uploadedMu       sync.RWMutex
	uploadedByStream map[string][]UploadedVideo
}

type UploadedVideo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
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
	return &BunnyChunkPublisher{
		cfg:              cfg,
		client:           &http.Client{Timeout: cfg.HTTPTimeout},
		uploadedByStream: make(map[string][]UploadedVideo),
	}
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
	nextIndex, err := p.nextSegmentIndex(segmentsDir)
	if err != nil {
		return err
	}
	segmentPath := filepath.Join(segmentsDir, fmt.Sprintf("%09d.mp4", nextIndex))
	if err := os.Rename(chunkPath, segmentPath); err != nil {
		return err
	}

	return nil
}

func (p *BunnyChunkPublisher) Finalize(ctx context.Context, streamerID string, capturedAt time.Time) error {
	if p == nil {
		return nil
	}
	if strings.TrimSpace(p.cfg.LibraryID) == "" || strings.TrimSpace(p.cfg.APIKey) == "" {
		return nil
	}
	segmentsDir := filepath.Join(p.cfg.OutputDir, sanitizeToken(streamerID), "segments")
	segments, err := p.listSegments(segmentsDir)
	if err != nil {
		if errorsIsNotExist(err) {
			return nil
		}
		return err
	}
	if len(segments) == 0 {
		return nil
	}
	windowPath, err := p.concatSegments(ctx, streamerID, segmentsDir, segments)
	if err != nil {
		return err
	}
	defer os.Remove(windowPath) //nolint:errcheck

	videoID, title, err := p.createVideo(ctx, streamerID, capturedAt)
	if err != nil {
		return err
	}
	if err := p.uploadVideo(ctx, videoID, windowPath); err != nil {
		return err
	}
	p.appendUploadedVideo(ctx, streamerID, UploadedVideo{
		ID:        videoID,
		Title:     title,
		URL:       bunnyPlaybackURL(p.cfg.LibraryID, videoID),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	for _, segment := range segments {
		_ = os.Remove(segment)
	}
	_ = os.RemoveAll(segmentsDir)
	_ = os.Remove(filepath.Join(p.cfg.OutputDir, sanitizeToken(streamerID)))
	return nil
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func (p *BunnyChunkPublisher) listSegments(segmentsDir string) ([]string, error) {
	entries, err := os.ReadDir(segmentsDir)
	if err != nil {
		return nil, err
	}
	type segmentMeta struct {
		index    int
		path     string
		captured time.Time
	}
	segments := make([]segmentMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".mp4") {
			continue
		}
		path := filepath.Join(segmentsDir, entry.Name())
		index := parseSegmentIndex(entry.Name())
		captured := parseSegmentCapturedAt(entry.Name())
		if captured.IsZero() {
			info, statErr := entry.Info()
			if statErr != nil {
				return nil, statErr
			}
			captured = info.ModTime().UTC()
		}
		segments = append(segments, segmentMeta{index: index, path: path, captured: captured})
	}
	sort.SliceStable(segments, func(i, j int) bool {
		left := segments[i]
		right := segments[j]
		if left.index > 0 && right.index > 0 && left.index != right.index {
			return left.index < right.index
		}
		if left.captured.Equal(right.captured) {
			return left.path < right.path
		}
		return left.captured.Before(right.captured)
	})
	result := make([]string, 0, len(segments))
	for _, item := range segments {
		result = append(result, item.path)
	}
	return result, nil
}

func (p *BunnyChunkPublisher) nextSegmentIndex(segmentsDir string) (int, error) {
	entries, err := os.ReadDir(segmentsDir)
	if err != nil {
		return 0, err
	}
	maxIndex := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".mp4") {
			continue
		}
		index := parseSegmentIndex(entry.Name())
		if index > maxIndex {
			maxIndex = index
		}
	}
	return maxIndex + 1, nil
}

func (p *BunnyChunkPublisher) concatSegments(ctx context.Context, streamerID, segmentsDir string, selected []string) (string, error) {
	listPath := filepath.Join(segmentsDir, fmt.Sprintf("concat_%s.txt", sanitizeToken(time.Now().UTC().Format(time.RFC3339Nano))))
	var body strings.Builder
	for _, segment := range selected {
		absoluteSegmentPath, err := filepath.Abs(segment)
		if err != nil {
			return "", err
		}
		body.WriteString("file '")
		body.WriteString(strings.ReplaceAll(absoluteSegmentPath, "'", "'\\''"))
		body.WriteString("'\n")
	}
	if err := os.WriteFile(listPath, []byte(body.String()), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(listPath) //nolint:errcheck

	outputPath := filepath.Join(p.cfg.OutputDir, sanitizeToken(streamerID), fmt.Sprintf("window_%s.mp4", sanitizeToken(time.Now().UTC().Format("20060102T150405.000000000"))))
	var stderr strings.Builder
	args := []string{
		"-y",
		"-fflags", "+genpts",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		outputPath,
	}
	if err := p.cfg.Runner.Run(ctx, io.Discard, &stderr, p.cfg.FFmpegBinary, args...); err != nil {
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("concat chunks failed: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}
	return outputPath, nil
}

func parseSegmentCapturedAt(fileName string) time.Time {
	base := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	normalized := strings.ReplaceAll(base, "_", ".")
	parsed, err := time.Parse("20060102T150405.000000000", normalized)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func parseSegmentIndex(fileName string) int {
	base := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	value, err := strconv.Atoi(base)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

type bunnyCreateVideoRequest struct {
	Title string `json:"title"`
}

type bunnyCreateVideoResponse struct {
	GUID string `json:"guid"`
}

func (p *BunnyChunkPublisher) createVideo(ctx context.Context, streamerID string, capturedAt time.Time) (string, string, error) {
	title := p.buildRemoteVideoPath(ctx, streamerID, capturedAt)
	payload, err := json.Marshal(bunnyCreateVideoRequest{Title: title})
	if err != nil {
		return "", "", err
	}
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/library/" + p.cfg.LibraryID + "/videos"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("AccessKey", p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", fmt.Errorf("create bunny video failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var decoded bunnyCreateVideoResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(decoded.GUID) == "" {
		return "", "", fmt.Errorf("create bunny video: empty guid")
	}
	return decoded.GUID, title, nil
}

func (p *BunnyChunkPublisher) ListUploadedVideos(streamerID string) []UploadedVideo {
	if p == nil {
		return []UploadedVideo{}
	}
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return []UploadedVideo{}
	}
	if p.cfg.VideoStore != nil {
		items, err := p.cfg.VideoStore.ListByStreamer(context.Background(), key)
		if err == nil {
			return items
		}
	}
	p.uploadedMu.RLock()
	defer p.uploadedMu.RUnlock()
	items := p.uploadedByStream[key]
	out := make([]UploadedVideo, len(items))
	copy(out, items)
	return out
}

func (p *BunnyChunkPublisher) DeleteStreamerVideos(ctx context.Context, streamerID string) (int, error) {
	if p == nil {
		return 0, nil
	}
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return 0, nil
	}
	items := p.ListUploadedVideos(key)
	deleted := 0
	for _, item := range items {
		if err := p.deleteVideo(ctx, item.ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	if p.cfg.VideoStore != nil {
		if err := p.cfg.VideoStore.DeleteByStreamer(ctx, key); err != nil {
			return deleted, err
		}
		return deleted, nil
	}
	p.uploadedMu.Lock()
	delete(p.uploadedByStream, key)
	p.uploadedMu.Unlock()
	return deleted, nil
}

func (p *BunnyChunkPublisher) appendUploadedVideo(ctx context.Context, streamerID string, item UploadedVideo) {
	key := strings.TrimSpace(streamerID)
	if key == "" {
		return
	}
	if p.cfg.VideoStore != nil {
		if err := p.cfg.VideoStore.Save(ctx, key, item); err == nil {
			return
		}
	}
	p.uploadedMu.Lock()
	p.uploadedByStream[key] = append(p.uploadedByStream[key], item)
	p.uploadedMu.Unlock()
}

func (p *BunnyChunkPublisher) deleteVideo(ctx context.Context, videoID string) error {
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/library/" + p.cfg.LibraryID + "/videos/" + videoID
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("AccessKey", p.cfg.APIKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("delete bunny video failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
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

func (p *BunnyChunkPublisher) buildRemoteVideoPath(ctx context.Context, streamerID string, capturedAt time.Time) string {
	day := capturedAt.UTC().Format("2006-01-02")
	if day == "0001-01-01" {
		day = time.Now().UTC().Format("2006-01-02")
	}
	streamerFolder := sanitizeToken(streamerID)
	if p.cfg.UsernameResolver != nil {
		if username, err := p.cfg.UsernameResolver(ctx, streamerID); err == nil && strings.TrimSpace(username) != "" {
			streamerFolder = sanitizeToken(streamerID + "_" + username)
		}
	}
	return fmt.Sprintf("%s/%s/%s", streamerFolder, day, sanitizeToken(time.Now().UTC().Format(time.RFC3339Nano)))
}

func bunnyPlaybackURL(libraryID, videoID string) string {
	library := strings.TrimSpace(libraryID)
	video := strings.TrimSpace(videoID)
	if library == "" || video == "" {
		return ""
	}
	return strings.TrimRight(defaultBunnyPlaybackBaseURL, "/") + "/play/" + library + "/" + video
}
