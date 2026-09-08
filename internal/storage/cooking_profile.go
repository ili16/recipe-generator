package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// UpsertCookingProfile stores the background worker's latest computed profile for a user.
func UpsertCookingProfile(ctx context.Context, userID int, p model.CookingProfile) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = Pool.Exec(ctx, `
		INSERT INTO user_cooking_profiles (user_id, data, computed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET data = EXCLUDED.data, computed_at = NOW()`,
		userID, data)
	return err
}

// GetCookingProfile returns a user's stored cooking profile, or found=false if the
// worker hasn't computed one yet. Not called by any handler yet — kept trivial so a
// future read-only screen or personalization step can use it without new plumbing.
func GetCookingProfile(ctx context.Context, userID int) (p model.CookingProfile, found bool, err error) {
	var raw []byte
	err = Pool.QueryRow(ctx, `SELECT data FROM user_cooking_profiles WHERE user_id = $1`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, false, err
	}
	return p, true, nil
}

// RecomputeAllCookingProfiles rebuilds every user's CookingProfile from scratch and
// upserts it. Called by the background worker (internal/worker) on a tick.
//
// ponytail: full O(n) recompute over every user with a saved recipe on every tick, not
// incremental — switch to "recipes updated since last computed_at" if the user/recipe
// count ever makes this measurably slow.
func RecomputeAllCookingProfiles(ctx context.Context) (int, error) {
	countRows, err := Pool.Query(ctx, `
		SELECT user_id, COUNT(*), MIN(created_at)
		FROM recipes WHERE deleted_at IS NULL
		GROUP BY user_id`)
	if err != nil {
		return 0, err
	}
	profiles := make(map[int]*model.CookingProfile)
	firstSaved := make(map[int]time.Time)
	for countRows.Next() {
		var userID int
		var count int
		var minCreated time.Time
		if err := countRows.Scan(&userID, &count, &minCreated); err != nil {
			countRows.Close()
			return 0, err
		}
		profiles[userID] = &model.CookingProfile{RecipeCount: count}
		firstSaved[userID] = minCreated
	}
	countRows.Close()
	if err := countRows.Err(); err != nil {
		return 0, err
	}
	for userID, p := range profiles {
		weeks := time.Since(firstSaved[userID]).Hours() / (24 * 7)
		if weeks < 1 {
			weeks = 1
		}
		p.AvgRecipesPerWeek = float64(p.RecipeCount) / weeks
	}

	tagCounts := make(map[int]map[string]int)
	tagRows, err := Pool.Query(ctx, `
		SELECT r.user_id, t.slug, COUNT(*)
		FROM recipe_tags rt
		JOIN tags t ON t.id = rt.tag_id
		JOIN recipes r ON r.id = rt.recipe_id
		WHERE r.deleted_at IS NULL
		GROUP BY r.user_id, t.slug`)
	if err != nil {
		return 0, err
	}
	for tagRows.Next() {
		var userID int
		var slug string
		var n int
		if err := tagRows.Scan(&userID, &slug, &n); err != nil {
			tagRows.Close()
			return 0, err
		}
		if tagCounts[userID] == nil {
			tagCounts[userID] = make(map[string]int)
		}
		tagCounts[userID][slug] = n
	}
	tagRows.Close()
	if err := tagRows.Err(); err != nil {
		return 0, err
	}
	for userID, counts := range tagCounts {
		p, ok := profiles[userID]
		if !ok {
			continue
		}
		type slugCount struct {
			slug string
			n    int
		}
		ranked := make([]slugCount, 0, len(counts))
		for slug, n := range counts {
			ranked = append(ranked, slugCount{slug, n})
		}
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].n > ranked[j].n })
		for i := 0; i < len(ranked) && i < 5; i++ {
			p.TopTags = append(p.TopTags, ranked[i].slug)
		}
	}

	diffCounts := make(map[int]map[string]int)
	diffRows, err := Pool.Query(ctx, `
		SELECT user_id, difficulty, COUNT(*)
		FROM recipes WHERE deleted_at IS NULL AND difficulty IS NOT NULL
		GROUP BY user_id, difficulty`)
	if err != nil {
		return 0, err
	}
	for diffRows.Next() {
		var userID int
		var difficulty string
		var n int
		if err := diffRows.Scan(&userID, &difficulty, &n); err != nil {
			diffRows.Close()
			return 0, err
		}
		if diffCounts[userID] == nil {
			diffCounts[userID] = make(map[string]int)
		}
		diffCounts[userID][difficulty] = n
	}
	diffRows.Close()
	if err := diffRows.Err(); err != nil {
		return 0, err
	}
	for userID, counts := range diffCounts {
		p, ok := profiles[userID]
		if !ok {
			continue
		}
		var best string
		bestN := 0
		for difficulty, n := range counts {
			if n > bestN {
				best, bestN = difficulty, n
			}
		}
		if best != "" {
			p.PreferredDifficulty = &best
		}
	}

	for userID, p := range profiles {
		if err := UpsertCookingProfile(ctx, userID, *p); err != nil {
			return 0, err
		}
	}
	return len(profiles), nil
}
