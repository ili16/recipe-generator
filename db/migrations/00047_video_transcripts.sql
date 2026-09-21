-- +goose Up
-- +goose StatementBegin
-- BACKLOG 11.9's second half. Reading a reel out loud costs a 27 MB download, an ffmpeg
-- run and ~$0.013 of Whisper, and takes ~20s while the user watches a chat turn sit there.
-- All of that is spent again on the second paste of the same link today, which is the one
-- thing the entry said it would not do: "the second paste of the same URL costs nothing".
--
-- Global, not per-kitchen, for ingredient_grams' reason: what is spoken in a public reel
-- is not a fact about a household, and two people pasting the same link should pay once
-- between them. So no user_id, no household_id, no inScope.
--
-- Keyed on the Instagram shortcode rather than the URL as pasted: the same reel arrives as
-- /reel/<code>/, /p/<code>/ and /<author>/reel/<code>/?igsh=<tracking>, and a URL key would
-- cache four copies of one soundtrack. The mp4 URL itself is never a key -- it is signed
-- and expires.
--
-- An empty transcript is a real answer, cached like any other: a reel with no speech in it
-- must not re-download on every paste to rediscover its own silence. That is why the read
-- reports found separately from the text.
CREATE TABLE video_transcripts
(
    source_key TEXT PRIMARY KEY,
    transcript TEXT        NOT NULL,
    seconds    NUMERIC     NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE video_transcripts;
-- +goose StatementEnd
