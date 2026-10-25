import { Ionicons } from '@expo/vector-icons';
import { RootStackParamList } from './AppNavigator';

export interface NavItem {
  key: string;
  /** i18n key, not a word — the rail and the tab bar resolve it with `t()` (see src/i18n). */
  labelKey: string;
  icon: React.ComponentProps<typeof Ionicons>['name'];
  route: keyof RootStackParamList;
}

/**
 * The five real destinations, and the bottom tab bar's contents on narrow (BACKLOG 5.7).
 *
 * `route` is required: the three route-less "Soon" entries (Grocery List, Pantry, Discover) are
 * gone. A tab is a promise, and an inert one is worse than an absent one — Grocery List arrived
 * with 6.1, Pantry with 7.1, Discover only if it is ever scheduled. Do not add an entry
 * here before the screen it points at exists.
 */
export const NAV_ITEMS: NavItem[] = [
  { key: 'chat', labelKey: 'nav.chat', icon: 'chatbubbles-outline', route: 'Chat' },
  { key: 'recipes', labelKey: 'nav.recipes', icon: 'book-outline', route: 'Recipes' },
  { key: 'mealplan', labelKey: 'nav.mealplan', icon: 'calendar-outline', route: 'MealPlan' },
  { key: 'grocery', labelKey: 'nav.grocery', icon: 'cart-outline', route: 'GroceryList' },
  { key: 'pantry', labelKey: 'nav.pantry', icon: 'file-tray-stacked-outline', route: 'Pantry' },
];

// Neither of these is a tab: the bottom bar has five and a sixth would not fit a phone.
// History lives in the top row on narrow and in the rail's footer on wide (BACKLOG 10.7)
// — the point of that entry is that it is reachable from the shell, not only from chat.
export const HISTORY_NAV_ITEM: NavItem = {
  key: 'history',
  labelKey: 'chat.history.title',
  icon: 'time-outline',
  route: 'History',
};

export const PROFILE_NAV_ITEM: NavItem = {
  key: 'profile',
  labelKey: 'nav.profile',
  icon: 'person-circle-outline',
  route: 'Profile',
};
