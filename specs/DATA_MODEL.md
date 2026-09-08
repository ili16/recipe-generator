# Recipe Data Model

Date: 2026-08-21 (tags updated 2026-09-06)
Status: implemented as goose migrations `00001`–`00006` (clean initial schema, no legacy DB)
System of record: PostgreSQL. Markdown/HTML are rendered views, never the source of truth.

## Design Rules

1. One recipe = one row in `recipes` + N ordered rows in `recipe_ingredients` / `recipe_steps`.
2. Anything free-form or experimental goes into a `attributes JSONB` column instead of a new table.
3. Provenance (`sources`) is immutable. AI never rewrites what the user handed us.
4. History is snapshot-based (`recipe_versions.data`), not delta-based — simpler to render and roll back.
5. Every user-owned table carries `user_id`; every query is filtered by the authenticated user.
6. Ownership is never accepted from a request body.

## Table Overview

| Table | Purpose | Owner column |
| --- | --- | --- |
| `users` | JIT-provisioned identities keyed by OIDC `sub` | — |
| `recipes` | canonical recipe header | `user_id` |
| `recipe_ingredients` | ordered ingredient list | via `recipe_id` |
| `recipe_steps` | ordered preparation steps | via `recipe_id` |
| `sources` | raw capture provenance (text/url/image/voice) | `user_id` |
| `recipe_versions` | full JSONB snapshot per accepted edit | via `recipe_id` |
| `recipe_notes` | cooking notes, rating, variant label | `user_id` |
| `recipe_timeline_events` | append-only activity log | `user_id` |
| `techniques`, `flavor_profiles` | reference vocabularies | — |
| `recipe_techniques`, `recipe_flavor_profiles` | validation tags with confidence | via `recipe_id` |
| `tags` | fixed multi-group tagging taxonomy, see `RECIPE_TAGS.md` | — |
| `recipe_tags` | recipe ↔ tag join table (replaces the old `recipes.category`) | via `recipe_id` |
| `creator_follows`, `feed_candidates` | blog/creator follow + discovered recipes | `user_id` |
| `meal_plans`, `meal_plan_items` | meal planning over saved recipes (items carry `start_time`, migration `00013`) | `user_id` |

## `users`

Identity is derived exclusively from the token subject claim.

```
id             SERIAL PK
oauth_id       TEXT UNIQUE NOT NULL   -- OIDC `sub`, the only stable identifier
name           TEXT NULL              -- optional claim
email          TEXT NULL              -- optional claim
oauth_provider TEXT NOT NULL DEFAULT ''
created_at     TIMESTAMPTZ
last_seen_at   TIMESTAMPTZ            -- refreshed on every authenticated request
```

JIT upsert executed by `storage.Users.EnsureUser`:

```sql
INSERT INTO users (oauth_id, name, email, oauth_provider, last_seen_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (oauth_id) DO UPDATE
SET name = COALESCE(NULLIF(EXCLUDED.name, ''), users.name),
    ...
RETURNING id;
```

## `recipes`

```
id                    SERIAL PK
public_id             UUID UNIQUE            -- external/shareable id
user_id               INT FK users
title                 TEXT NOT NULL
summary               TEXT
language              TEXT NOT NULL DEFAULT 'en'
servings              INT
prep_minutes          INT
cook_minutes          INT
total_minutes         INT
difficulty            TEXT
source_type           TEXT CHECK (text|url|image|voice|manual|import)
source_url            TEXT
content               TEXT                   -- rendered markdown draft
ai_model              TEXT
ai_prompt_version     TEXT
extraction_confidence NUMERIC(4,3)
current_version       INT NOT NULL DEFAULT 1
attributes            JSONB NOT NULL DEFAULT '{}'
created_at/updated_at TIMESTAMPTZ
deleted_at            TIMESTAMPTZ            -- soft delete
```

`content` is a convenience copy of the rendered markdown so the current mobile client keeps
working; `recipe_ingredients` and `recipe_steps` are authoritative once the structured write
path lands.

Migration `00014` adds `variant_of_recipe_id INT REFERENCES recipes(id) ON DELETE SET NULL`
(indexed, partial) — links a recipe back to the one it was generated as a twist on via
`POST /meal-plan/variants/{id}`, when the caller accepts the proposal. Not yet surfaced in
any listing/grouping UI. (This block otherwise predates several later migrations —
`manually_edited`, `recipe_votes`, `user_preferences`, `user_cooking_profiles`,
`generation_events` — see the migration files for the current full schema.)

## Ingredients and Steps

Both are ordered child tables with a unique `(recipe_id, sort_order)` index, so reordering
is a single transactional rewrite and rendering never needs a sort heuristic.

`recipe_ingredients` splits quantity into `quantity NUMERIC` (for on-device scaling) and
`quantity_text TEXT` (for un-parseable inputs like "a pinch"). `section` groups ingredients
into "Dough" / "Sauce" blocks.

`recipe_steps` carries `timer_seconds` and `temperature_c` so the mobile app can render
in-step timers without re-parsing prose.

## Tags

`recipes.category` (a single `main|starter|bread|dessert|other` value) was replaced in
migration `00006` by `tags` (fixed reference vocabulary, `slug`/`label`/`tag_group`) and
`recipe_tags` (recipe ↔ tag join table, composite PK). A recipe can carry any number of tags
across any number of groups. Full taxonomy, schema, and API contract: `RECIPE_TAGS.md`.

## Versioning

`recipe_versions.data` holds the complete canonical JSON document:

```json
{
  "title": "...", "servings": 4, "language": "de",
  "ingredients": [{"section": null, "item": "flour", "quantity": 500, "unit": "g"}],
  "steps": [{"sort_order": 1, "step_text": "...", "timer_seconds": 600}],
  "attributes": {}
}
```

Flow for an AI edit: propose diff → user accepts → write new `recipe_versions` row with
`change_kind='ai_edit'` → apply to live tables → bump `recipes.current_version` →
append `recipe_timeline_events` row.

## Rendering Contract

`GET /recipes/:id?format=json|markdown|html` reads the live structured tables and renders
on the fly. No rendered artifact is persisted.

## Remaining Work

The write path still only fills `recipes.content`. To finish the structured model:

1. Have the extraction pipeline emit the canonical JSON document above.
2. Persist it into `recipe_ingredients` / `recipe_steps` plus a `recipe_versions` row
   with `change_kind = 'extraction'` in one transaction.
3. Render `content` from the structured rows instead of storing LLM output directly.
4. Once the mobile client reads structured fields, drop `recipes.content`.

## Explicitly Out of Scope

- Object storage for media (source binaries are referenced, not stored).
- Job queues / async workers — extraction stays synchronous for MVP.
- Cross-user sharing tables beyond the read-only static cookbook render.
