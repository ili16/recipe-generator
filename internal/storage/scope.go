package storage

import "context"

// Household scoping (BACKLOG 15.1). Every read that used to say "this row is mine" now
// says "this row is my kitchen's", and there is exactly one expression for that so the
// two cannot drift apart.
//
// A row belongs to the caller's scope when it carries the caller's household_id, or when
// it carries none and the caller owns it outright. For a caller with no household the
// first branch compares against NULL and never matches, so the predicate collapses to the
// plain user_id check this code did before — which is what makes shipping this against
// live data safe: **it is inert until somebody creates a household.**
//
// What does NOT widen, deliberately:
//   - writes to a recipe (update, delete, restore, share). A recipe is personal property
//     lent to the household: everyone cooks it, only the owner rewrites it. Those keep
//     user_id = $n and a non-owner gets the same not-found an unrelated user gets.
//   - votes and cooked marks. A like and "I cooked this on Tuesday" are about a person.
//   - conversations. A shared chat thread is a different product.
//   - llm_usage and budgets. Who pays for a turn is per-account (BACKLOG 15.10).

// inScope renders the scope predicate for one table alias. param is the placeholder
// holding the caller's user id ("$1", "$2", …) and may legitimately repeat within a
// query. Both arguments are always compile-time literals at the call sites.
func inScope(alias, param string) string {
	return "(" + alias + ".household_id = (SELECT household_id FROM users WHERE id = " + param + ")" +
		" OR (" + alias + ".household_id IS NULL AND " + alias + ".user_id = " + param + "))"
}

// kitchenOwner returns the user whose personal settings stand for the whole kitchen: the
// household's creator while they are still a member, the caller otherwise.
//
// Two settings are household facts wearing a personal column — week_start_day and
// household_size. If they stayed per-caller, two members planning the same week would
// disagree about which day it starts on and file the same days under different plan rows,
// and a day planned by one would be sized for a different table than a day planned by the
// other. One member's answer has to win; whoever set the household up is the least
// arbitrary choice and the only one that does not change as people come and go.
//
// ponytail: the honest home for these is a settings column on households. That is worth
// doing the moment a third such field appears — two is not yet a table.
func kitchenOwner(ctx context.Context, q queryRower, userID int) int {
	var owner int
	err := q.QueryRow(ctx, `
		SELECT h.created_by FROM households h
		JOIN users me ON me.household_id = h.id
		JOIN users creator ON creator.id = h.created_by AND creator.household_id = h.id
		WHERE me.id = $1`, userID).Scan(&owner)
	if err != nil {
		return userID
	}
	return owner
}

// stampHousehold is the write half: a row created inside a household belongs to it, while
// user_id still records who made it. Nothing is ever moved between owners, which is the
// single property the leave/disband contract in households.go rests on.
const stampHousehold = `(SELECT household_id FROM users WHERE id = $1)`
