CREATE TABLE IF NOT EXISTS streamer_uploaded_videos (
    streamer_id TEXT NOT NULL,
    video_id TEXT NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (streamer_id, video_id)
);

CREATE INDEX IF NOT EXISTS idx_streamer_uploaded_videos_streamer_created_at
    ON streamer_uploaded_videos (streamer_id, created_at DESC, video_id DESC);
