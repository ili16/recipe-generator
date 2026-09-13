-- +goose Up
-- +goose StatementBegin
-- A written bug report is ambiguous twice: once about what the user saw, and once about
-- what they meant by it. A screenshot settles both, and the reporter already has one --
-- on web it is one Ctrl-V.
--
-- Bytes in the row rather than object storage: an alpha is a dozen friends and a handful
-- of PNGs, and standing up a bucket, credentials and a signed-URL path before the first
-- report arrives is work that buys nothing yet. The server caps each image, so the table
-- cannot run away.
--
-- ponytail: bytea, move to object storage if this grows past a few hundred reports --
-- BACKLOG 13.3 (post-cook photos) needs the same thing and can carry both.
ALTER TABLE feedback ADD COLUMN screenshot BYTEA;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE feedback DROP COLUMN screenshot;
-- +goose StatementEnd
