-- +goose Up
-- +goose StatementBegin
-- The alpha is going out to friends and there is no channel back: a bug arrives as a text
-- message, without the screen they were on, the app version, or what they were trying to do.
-- One table serves both directions -- the report button they press ('bug'/'idea') and the
-- occasional prompt we show them ('pulse') -- because both end up in the same morning read,
-- and splitting them would mean two queries to answer "what did people say yesterday".
CREATE TABLE feedback (
    id         SERIAL PRIMARY KEY,
    -- Nullable, and SET NULL rather than CASCADE: a friend who never signs in still hits
    -- bugs, and a report from someone who later deletes their account is still a bug worth
    -- keeping. Losing who said it is acceptable; losing what they said is not.
    user_id    INT REFERENCES users(id) ON DELETE SET NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('bug', 'idea', 'pulse')),
    message    TEXT NOT NULL DEFAULT '',
    -- +1/-1, the same shape as recipe_votes.vote (00011), not a second 1-5 scale: a one-tap
    -- thumb is the highest-response question there is, and stars would collide with the star
    -- row that rates a *recipe*.
    sentiment  SMALLINT CHECK (sentiment IN (-1, 1)),
    -- Where to write back when the reporter is anonymous. Optional, never required --
    -- demanding an address from an anonymous reporter is the biggest drop-off in a report form.
    contact    TEXT NOT NULL DEFAULT '',
    -- route, app version, platform, locale. One column instead of four, because nothing
    -- queries on them; they are context a human reads next to the message.
    context    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- A row with neither words nor a thumb is noise.
    CONSTRAINT feedback_not_empty CHECK (message <> '' OR sentiment IS NOT NULL)
);

-- The only read this table has is "everything since yesterday, newest first".
CREATE INDEX feedback_created_at_idx ON feedback (created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE feedback;
-- +goose StatementEnd
