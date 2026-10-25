// On web the app and the API share an origin (beta and live both path-split one host), so
// the page's own origin is the right base wherever the image is served. Native has no
// origin and talks to live (store builds).
const WEB_ORIGIN = typeof window !== 'undefined' && window.location ? window.location.origin : null;
const NATIVE_BASE_URL = 'https://recipe-generator.ili16.de';

// API Configuration
export const API_BASE_URL = __DEV__
  ? 'http://localhost:8080/api/v1'
  : `${WEB_ORIGIN ?? NATIVE_BASE_URL}/api/v1`;

// Where a share link points (BACKLOG 8.4). Native dev has no web origin of its own so it
// hands out the beta one.
export const APP_BASE_URL = WEB_ORIGIN ?? NATIVE_BASE_URL;

export const shareUrl = (token: string) => `${APP_BASE_URL}/s/${token}`;

// The week's public page (BACKLOG 13.2) — same mechanism, different noun.
export const planShareUrl = (token: string) => `${APP_BASE_URL}/p/${token}`;

const KEYCLOAK_DEV_URL = 'https://sso.ili16.de';
const KEYCLOAK_PROD_URL = 'https://sso.ili16.de';
const KEYCLOAK_DEV_REALM = 'recipe-generator';
const KEYCLOAK_PROD_REALM = 'recipe-generator';

// Keycloak OIDC Configuration
export const KEYCLOAK_CONFIG = {
  url: __DEV__ ? KEYCLOAK_DEV_URL : KEYCLOAK_PROD_URL,
  realm: __DEV__ ? KEYCLOAK_DEV_REALM : KEYCLOAK_PROD_REALM,
  clientId: 'frontend',
  redirectUri: 'com.recipegenerator://oauth/callback',
} as const;

// API Endpoints (relative to API_BASE_URL)
export const API_ENDPOINTS = {
  CHAT: 'chat',
  SAVE_CHAT_DRAFT: 'chat/drafts/save',
  CONVERSATIONS: 'conversations',
  TRANSCRIBE_AUDIO: 'transcribe',
  UPDATE_RECIPE: 'update-recipe',
  GET_RECIPES: 'get-recipes',
  DELETE_RECIPE: 'delete-recipe',
  SAVE_RECIPE: 'add-recipe',
  USER_INFO: 'user-info',
  GET_RECIPE: 'recipes',
  PREFERENCES: 'preferences',
  MEAL_PLAN: 'meal-plan',
  GROCERY_LIST: 'grocery-list',
  GROCERY_TICKS: 'grocery-ticks',
  MEAL_PLAN_ITEMS: 'meal-plan/items',
  MEAL_PLAN_VARIANTS: 'meal-plan/variants',
  COLLECTIONS: 'collections',
  PANTRY: 'pantry',
  HOUSEHOLD: 'household',
  SHARED: 'shared',
  SHARED_PLAN: 'shared-plan',
  MEAL_PLAN_SHARE: 'meal-plan/share',
  FEEDBACK: 'feedback',
  USAGE: 'usage',
} as const;

// Storage Keys
export const STORAGE_KEYS = {
  RECIPES: 'recipes',
  USER_TOKEN: 'user_token',
  USER_REFRESH_TOKEN: 'user_refresh_token',
  USER_TOKEN_EXPIRES_AT: 'user_token_expires_at',
  USER_PROFILE: 'user_profile',
  THEME_MODE: 'theme_mode',
  LANGUAGE: 'language',
} as const;


// Keyed by user id so a shared device — or a logout/login within the same session —
// never shows one account's cached meal plan to another before the background refetch lands.
export const mealPlanCacheKeys = (userId: string) => ({
  // v2: the shape changed from one item per date to a list per date (BACKLOG 6.4).
  // A new key is the migration — the old cache is a week of plan data that the next
  // ensureRange refetches anyway.
  items: `mealplan_cache_items_v2_${userId}`,
  recipes: `mealplan_cache_recipes_v1_${userId}`,
});

// The cooking-mode offline cache (BACKLOG 12.4) — full recipe documents, user-keyed for
// the same reason the meal-plan cache is.
export const cookCacheKey = (userId: string) => `cook_cache_v1_${userId}`;

// App Constants
export const MAX_RECIPE_NAME_LENGTH = 25;
