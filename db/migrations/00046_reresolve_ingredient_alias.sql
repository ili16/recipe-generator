-- +goose Up
-- +goose StatementBegin
-- ingredient_alias is a cache of one model answer, and that answer's prompt changed, so
-- the cache is stale by definition (BACKLOG 17.2, 17.4). Two things were wrong with what
-- it held:
--
--   * class was a shelf, not a substitution. "spice" grouped a vanilla bean with black
--     pepper, so the recipe card counted a swap it should never have offered. The prompt
--     now says so outright and service.usableClass drops the answer anyway.
--   * canonical kept the grade. "granulated sugar" did not resolve to "sugar", so a
--     kitchen holding sugar read as a near-miss needing a swap instead of a plain hit.
--
-- A row is never overwritten once written (the ON CONFLICT DO NOTHING contract), so the
-- only way to re-ask is to delete. Nothing is lost: every name still in use comes back on
-- storage.MissingAliasNames, and an unresolved name resolves to itself in the meantime --
-- exactly the behaviour every join had before the table existed.
--
-- Hand-seeded rows (source <> 'llm') are kept: a human answer is not what went stale.
--
-- Run cmd/backfill-aliases after this migration to refill it in one or two cheap calls.
DELETE FROM ingredient_alias WHERE source = 'llm';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Nothing to undo: the table refills itself from the backfill, and the rows this deleted
-- were the wrong ones.
SELECT 1;
-- +goose StatementEnd
