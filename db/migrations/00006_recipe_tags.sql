-- +goose Up
-- +goose StatementBegin
-- Fixed tagging taxonomy replacing the single recipes.category enum. See specs/RECIPE_TAGS.md.
CREATE TABLE tags
(
    id         SERIAL PRIMARY KEY,
    slug       TEXT        NOT NULL UNIQUE,
    label      TEXT        NOT NULL,
    tag_group  TEXT        NOT NULL CHECK (tag_group IN (
        'meal_type', 'dietary', 'time_difficulty', 'cooking_method', 'primary_ingredient', 'cuisine_vibe'
        )),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE recipe_tags
(
    recipe_id INT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    tag_id    INT NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (recipe_id, tag_id)
);

CREATE INDEX idx_recipe_tags_tag ON recipe_tags (tag_id);

INSERT INTO tags (slug, label, tag_group)
VALUES ('breakfast-brunch', 'Breakfast & Brunch', 'meal_type'),
       ('appetizers-starters', 'Appetizers & Starters', 'meal_type'),
       ('main-courses', 'Main Courses', 'meal_type'),
       ('side-dishes', 'Side Dishes', 'meal_type'),
       ('desserts', 'Desserts', 'meal_type'),
       ('snacks', 'Snacks', 'meal_type'),
       ('beverages', 'Beverages', 'meal_type'),
       ('sauces-dressings', 'Sauces & Dressings', 'meal_type'),

       ('vegan', 'Vegan', 'dietary'),
       ('vegetarian', 'Vegetarian', 'dietary'),
       ('gluten-free', 'Gluten-Free', 'dietary'),
       ('dairy-free', 'Dairy-Free', 'dietary'),
       ('nut-free', 'Nut-Free', 'dietary'),
       ('keto', 'Keto', 'dietary'),
       ('low-carb', 'Low-Carb', 'dietary'),

       ('under-20-minutes', 'Under 20 Minutes', 'time_difficulty'),
       ('30-minute-meals', '30-Minute Meals', 'time_difficulty'),
       ('easy-prep', 'Easy Prep', 'time_difficulty'),
       ('beginner-friendly', 'Beginner Friendly', 'time_difficulty'),
       ('meal-prep', 'Meal Prep', 'time_difficulty'),

       ('one-pot-one-pan', 'One-Pot / One-Pan', 'cooking_method'),
       ('no-cook', 'No-Cook', 'cooking_method'),
       ('baking', 'Baking', 'cooking_method'),
       ('air-fryer', 'Air Fryer', 'cooking_method'),
       ('slow-cooker', 'Slow Cooker', 'cooking_method'),
       ('instant-pot', 'Instant Pot', 'cooking_method'),
       ('grilling', 'Grilling', 'cooking_method'),

       ('chicken-poultry', 'Chicken & Poultry', 'primary_ingredient'),
       ('beef-red-meat', 'Beef & Red Meat', 'primary_ingredient'),
       ('pork', 'Pork', 'primary_ingredient'),
       ('seafood', 'Seafood', 'primary_ingredient'),
       ('pasta-grains', 'Pasta & Grains', 'primary_ingredient'),
       ('vegetables', 'Vegetables', 'primary_ingredient'),

       ('comfort-food', 'Comfort Food', 'cuisine_vibe'),
       ('healthy', 'Healthy', 'cuisine_vibe'),
       ('budget-friendly', 'Budget-Friendly', 'cuisine_vibe'),
       ('kid-friendly', 'Kid-Friendly', 'cuisine_vibe'),
       ('spicy', 'Spicy', 'cuisine_vibe');

-- Best-effort backfill from the old category enum (see specs/RECIPE_TAGS.md mapping table).
-- 'other' recipes and any value with no clear equivalent are left untagged.
INSERT INTO recipe_tags (recipe_id, tag_id)
SELECT r.id, t.id
FROM recipes r
         JOIN tags t ON t.slug = CASE r.category
                                      WHEN 'main' THEN 'main-courses'
                                      WHEN 'starter' THEN 'appetizers-starters'
                                      WHEN 'dessert' THEN 'desserts'
                                      WHEN 'bread' THEN 'side-dishes'
                                      ELSE NULL
    END;

DROP INDEX idx_recipes_user_category;
ALTER TABLE recipes
    DROP COLUMN category;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Data note: this does not attempt to reverse-map recipe_tags back into a single category
-- value. Rolling back after tags have diverged from the old enum loses that divergence.
ALTER TABLE recipes
    ADD COLUMN category TEXT NOT NULL DEFAULT 'other';
CREATE INDEX idx_recipes_user_category ON recipes (user_id, category);

DROP TABLE recipe_tags;
DROP TABLE tags;
-- +goose StatementEnd
