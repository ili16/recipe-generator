package storage

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// A household is a closed group sharing one kitchen (BACKLOG 15.1): one pantry, one week,
// one grocery list, and a library everyone can cook from but only the owner can rewrite.
//
// Membership is a column on users, not a join table, because a user is in at most one
// household. Sharing is a nullable household_id on the shared tables; the row keeps the
// user_id it always had, which is why leaving and disbanding are a SET NULL rather than a
// reassignment. Nothing is ever moved, so nothing can be lost on the way back.

var (
	// ErrAlreadyInHousehold is a 409: join and create both refuse rather than silently
	// moving someone out of the household they are in.
	ErrAlreadyInHousehold = errors.New("already in a household")
	// ErrNotInHousehold is a 404 for leave/disband/rotate.
	ErrNotInHousehold = errors.New("not in a household")
)

// joinCodeAlphabet excludes the characters that get misheard and mistyped when a code is
// read aloud across a kitchen, which is the only way this one ever travels: O/0, I/1/L,
// U/V. What is left is unambiguous in both speech and handwriting.
const joinCodeAlphabet = "ABCDEFGHJKMNPQRSTWXYZ23456789"

// newJoinCode returns an 8-character code from crypto/rand -- ~39 bits over the alphabet
// above. Unguessable is the whole security model here, exactly as it is for a recipe's
// share token (share.go), so the randomness source is the same one.
func newJoinCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = joinCodeAlphabet[int(b)%len(joinCodeAlphabet)]
	}
	return string(out), nil
}

// GetHousehold returns the caller's household and its members, or ErrNotInHousehold.
func GetHousehold(ctx context.Context, userID int) (model.Household, error) {
	var h model.Household
	err := Pool.QueryRow(ctx, `
		SELECT hh.id::text, hh.name, hh.join_code
		FROM households hh
		JOIN users u ON u.household_id = hh.id
		WHERE u.id = $1`, userID).Scan(&h.ID, &h.Name, &h.JoinCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Household{}, ErrNotInHousehold
	}
	if err != nil {
		return model.Household{}, err
	}

	rows, err := Pool.Query(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(email, ''),
		       dietary_prefs, disliked_ingredients
		FROM users WHERE household_id = $1::uuid
		ORDER BY created_at`, h.ID)
	if err != nil {
		return model.Household{}, err
	}
	defer rows.Close()

	h.Members = []model.HouseholdMember{}
	for rows.Next() {
		var m model.HouseholdMember
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &m.DietaryPrefs, &m.DislikedIngredients); err != nil {
			return model.Household{}, err
		}
		m.IsMe = m.ID == userID
		h.Members = append(h.Members, m)
	}
	return h, rows.Err()
}

// CreateHousehold makes one and moves the caller's existing recipes, pantry and plans into
// it. ErrAlreadyInHousehold if they are in one -- leave first, so the exit rules (which
// decide what happens to a shared week) run exactly once and visibly.
func CreateHousehold(ctx context.Context, userID int, name string) (model.Household, error) {
	code, err := newJoinCode()
	if err != nil {
		return model.Household{}, err
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return model.Household{}, err
	}
	defer tx.Rollback(ctx)

	var existing *string
	if err := tx.QueryRow(ctx, `SELECT household_id::text FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&existing); err != nil {
		return model.Household{}, err
	}
	if existing != nil {
		return model.Household{}, ErrAlreadyInHousehold
	}

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO households (name, join_code, created_by) VALUES ($1, $2, $3)
		RETURNING id::text`, name, code, userID).Scan(&id); err != nil {
		return model.Household{}, err
	}
	if err := adoptInto(ctx, tx, userID, id); err != nil {
		return model.Household{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Household{}, err
	}
	return GetHousehold(ctx, userID)
}

// JoinHousehold moves the caller and everything they own into the household holding code.
// pgx.ErrNoRows for a code that does not resolve -- a wrong code and a rotated one are
// indistinguishable, the same way a missing id and someone else's are everywhere else.
func JoinHousehold(ctx context.Context, userID int, code string) (model.Household, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return model.Household{}, err
	}
	defer tx.Rollback(ctx)

	var existing *string
	if err := tx.QueryRow(ctx, `SELECT household_id::text FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&existing); err != nil {
		return model.Household{}, err
	}
	if existing != nil {
		return model.Household{}, ErrAlreadyInHousehold
	}

	var id string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM households WHERE join_code = $1`, code).Scan(&id); err != nil {
		return model.Household{}, err
	}
	if err := adoptInto(ctx, tx, userID, id); err != nil {
		return model.Household{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Household{}, err
	}
	return GetHousehold(ctx, userID)
}

// LeaveHousehold takes the caller and their own things out: their recipes and pantry items
// go back to being private, and the plan stays behind, because a shared week belongs to the
// table rather than to whoever typed it in.
//
// The last member leaving *is* a disband -- an empty household is a code that still opens a
// door into one person's old data.
func LeaveHousehold(ctx context.Context, userID int) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var id *string
	if err := tx.QueryRow(ctx, `SELECT household_id::text FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&id); err != nil {
		return err
	}
	if id == nil {
		return ErrNotInHousehold
	}

	var heir *int
	if err := tx.QueryRow(ctx, `
		SELECT id FROM users WHERE household_id = $1::uuid AND id <> $2
		ORDER BY created_at LIMIT 1`, *id, userID).Scan(&heir); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if heir == nil {
		if err := disbandTx(ctx, tx, *id); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	// Recipes and the pantry lines they put in go with them; the plan does not. The week
	// belongs to the table, not to whoever typed it in, and ripping half a shared week out
	// from under the people still eating it is the wrong default.
	if err := restamp(ctx, tx, userID, nil, "recipes", "pantry_items"); err != nil {
		return err
	}
	// Which means the plan rows they created have to change hands — this is the one place
	// ownership moves, and it moves precisely because the week was never theirs. Left
	// behind under a departed user_id, the week would survive the disband as nobody's:
	// still on the table, readable by no one who eats at it.
	if _, err := tx.Exec(ctx, `
		UPDATE meal_plans SET user_id = $3
		WHERE household_id = $1::uuid AND user_id = $2`, *id, userID, *heir); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET household_id = NULL WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DisbandHousehold ends it for everyone. Any member may: a household is a mutual
// arrangement, and giving one person the power to hold the others in it would be worse
// than giving each of them the power to end it.
func DisbandHousehold(ctx context.Context, userID int) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var id *string
	if err := tx.QueryRow(ctx, `SELECT household_id::text FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&id); err != nil {
		return err
	}
	if id == nil {
		return ErrNotInHousehold
	}
	if err := disbandTx(ctx, tx, *id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// disbandTx is the whole exit contract, in the order it has to happen.
//
// Future plans disappear and historic ones stay: a planned week is a joint intention and
// does not outlive the household that formed it, but a record of what was actually eaten
// is nobody's to delete. Everything else reverts to the user_id already on the row.
func disbandTx(ctx context.Context, tx pgx.Tx, householdID string) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM meal_plan_items mpi
		USING meal_plans mp
		WHERE mp.id = mpi.meal_plan_id AND mp.household_id = $1::uuid
		  AND mpi.planned_on > CURRENT_DATE`, householdID); err != nil {
		return err
	}
	for _, table := range []string{"recipes", "pantry_items", "meal_plans"} {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET household_id = NULL WHERE household_id = $1::uuid`, householdID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET household_id = NULL WHERE household_id = $1::uuid`, householdID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM households WHERE id = $1::uuid`, householdID)
	return err
}

// adoptInto moves userID and everything they own into a household.
//
// meal_plan_items are not listed anywhere here: they carry no user_id and reach ownership
// only through meal_plans, so moving the plan moves its days with it.
func adoptInto(ctx context.Context, tx pgx.Tx, userID int, household string) error {
	if _, err := tx.Exec(ctx, `UPDATE users SET household_id = $2::uuid WHERE id = $1`, userID, household); err != nil {
		return err
	}
	return restamp(ctx, tx, userID, &household, "recipes", "pantry_items", "meal_plans")
}

// restamp sets household_id on userID's rows in the named tables (nil to take them back
// out). The user_id on each row is untouched, so this is reversible by construction --
// the single property the whole exit contract rests on.
func restamp(ctx context.Context, tx pgx.Tx, userID int, household *string, tables ...string) error {
	for _, table := range tables {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET household_id = $2::uuid WHERE user_id = $1`, userID, household); err != nil {
			return err
		}
	}
	return nil
}

// RotateJoinCode issues a fresh code and invalidates the old one. This is the only way to
// revoke access after somebody leaves, which is why the code is a separate column from the
// household's id rather than being it.
func RotateJoinCode(ctx context.Context, userID int) (string, error) {
	code, err := newJoinCode()
	if err != nil {
		return "", err
	}
	var out string
	err = Pool.QueryRow(ctx, `
		UPDATE households SET join_code = $2
		WHERE id = (SELECT household_id FROM users WHERE id = $1)
		RETURNING join_code`, userID, code).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotInHousehold
	}
	return out, err
}
