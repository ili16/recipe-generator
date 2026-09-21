-- +goose Up
-- +goose StatementBegin
-- BACKLOG 18.0. "instant-hefe" resolved to "instant yeast" and "trockenhefe" to "active yeast"
-- with no class, so a pantry of one never covered a recipe wanting the other. Trockenhefe is
-- dry yeast, the same food as instant yeast. Pinned manual so no re-resolve or audit moves it.
INSERT INTO ingredient_alias (name, canonical, class, source)
VALUES ('trockenhefe', 'instant yeast', 'yeast', 'manual'),
       ('instant-hefe', 'instant yeast', 'yeast', 'manual')
ON CONFLICT (name) DO UPDATE SET canonical = EXCLUDED.canonical, class = EXCLUDED.class, source = 'manual';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
