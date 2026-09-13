package service

// AllTagSlugs is the fixed recipe tagging vocabulary. See specs/RECIPE_TAGS.md and
// db/migrations/00006_recipe_tags.sql, which seeds the same slugs into the tags table.
var AllTagSlugs = []string{
	// meal_type
	"breakfast-brunch", "lunch", "dinner", "appetizers-starters", "main-courses",
	"side-dishes", "desserts", "snacks", "beverages", "sauces-dressings",
	// dietary
	"vegan", "vegetarian", "gluten-free", "dairy-free", "nut-free", "keto", "low-carb",
	// time_difficulty
	"under-20-minutes", "30-minute-meals", "easy-prep", "beginner-friendly", "meal-prep",
	// cooking_method
	"one-pot-one-pan", "no-cook", "baking", "air-fryer", "slow-cooker", "instant-pot", "grilling",
	// primary_ingredient
	"chicken-poultry", "beef-red-meat", "pork", "seafood", "pasta-grains", "vegetables",
	// cuisine_vibe
	"comfort-food", "healthy", "budget-friendly", "kid-friendly", "spicy",
}
