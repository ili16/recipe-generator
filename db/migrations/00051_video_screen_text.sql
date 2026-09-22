-- +goose Up
-- +goose StatementBegin
-- BACKLOG 11.10. 11.9 reads a reel's soundtrack, which gives a flawless method and vague
-- amounts: the voiceover says "a good glug of oil" because the 2 tbsp is burned into the
-- frame. This is that other half — frames sampled out of the same mp4, read by the same
-- vision path that already reads a photo of a recipe.
--
-- A column, not a table, because it is derived from the same download under the same
-- shortcode: what is cached is the *reading* of one post, and the audio and the screen are
-- two halves of one reading. A second table would key on the same thing and expire apart.
--
-- '' is a real cached answer here as much as it is for the transcript — a reel whose frames
-- carry no recipe text must not re-download to rediscover that. Rows written before this
-- migration therefore read as "no screen text": they were cached by an earlier version that
-- never sampled a frame. That is a stale reading, not a wrong one, and it costs at most the
-- amounts of a handful of already-pasted reels; re-reading them all would cost a download
-- and a vision call each, for links nobody has asked about again.
ALTER TABLE video_transcripts
    ADD COLUMN screen_text TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE video_transcripts
    DROP COLUMN screen_text;
-- +goose StatementEnd
