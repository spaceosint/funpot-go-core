-- 0004_streamers_and_media.up.sql
-- Streamers are independent from app users.

CREATE TABLE public.streamers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    twitch_user_id TEXT,
    twitch_username TEXT NOT NULL CHECK (twitch_username <> ''),
    display_name TEXT NOT NULL DEFAULT '',

    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'inactive', 'disabled')),

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_streamers_twitch_username_lower
    ON public.streamers (lower(twitch_username));

CREATE UNIQUE INDEX uq_streamers_twitch_user_id
    ON public.streamers (twitch_user_id)
    WHERE twitch_user_id IS NOT NULL AND twitch_user_id <> '';

CREATE INDEX idx_streamers_status
    ON public.streamers (status);

CREATE TRIGGER trg_streamers_set_updated_at
BEFORE UPDATE ON public.streamers
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.streamer_uploaded_videos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    streamer_id UUID NOT NULL REFERENCES public.streamers(id) ON DELETE CASCADE,

    provider TEXT NOT NULL DEFAULT 'manual'
        CHECK (provider IN ('manual', 'twitch', 'youtube', 'bunny', 'other')),

    video_id TEXT NOT NULL CHECK (video_id <> ''),
    title TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL CHECK (url <> ''),

    status TEXT NOT NULL DEFAULT 'uploaded'
        CHECK (status IN ('uploaded', 'processing', 'ready', 'failed', 'deleted')),

    duration_s INT CHECK (duration_s IS NULL OR duration_s >= 0),
    size_bytes BIGINT CHECK (size_bytes IS NULL OR size_bytes >= 0),
    mime_type TEXT NOT NULL DEFAULT '',

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_streamer_uploaded_videos_provider_video
    ON public.streamer_uploaded_videos (provider, video_id);

CREATE INDEX idx_streamer_uploaded_videos_streamer_created_at
    ON public.streamer_uploaded_videos (streamer_id, created_at DESC);

CREATE INDEX idx_streamer_uploaded_videos_status
    ON public.streamer_uploaded_videos (status);

CREATE TRIGGER trg_streamer_uploaded_videos_set_updated_at
BEFORE UPDATE ON public.streamer_uploaded_videos
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();


CREATE TABLE public.media_clips (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    streamer_id UUID NOT NULL REFERENCES public.streamers(id) ON DELETE CASCADE,
    uploaded_video_id UUID REFERENCES public.streamer_uploaded_videos(id) ON DELETE SET NULL,

    url TEXT NOT NULL CHECK (url <> ''),
    duration_s INT CHECK (duration_s IS NULL OR duration_s >= 0),

    source_type TEXT NOT NULL DEFAULT '',
    source_id UUID,

    status TEXT NOT NULL DEFAULT 'ready'
        CHECK (status IN ('ready', 'processing', 'failed', 'deleted')),

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_media_clips_streamer_created_at
    ON public.media_clips (streamer_id, created_at DESC);

CREATE INDEX idx_media_clips_uploaded_video
    ON public.media_clips (uploaded_video_id)
    WHERE uploaded_video_id IS NOT NULL;

CREATE INDEX idx_media_clips_source
    ON public.media_clips (source_type, source_id)
    WHERE source_type <> '' AND source_id IS NOT NULL;

CREATE TRIGGER trg_media_clips_set_updated_at
BEFORE UPDATE ON public.media_clips
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();
