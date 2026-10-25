import { Weekday, UserPreferences } from '../types';

// Shared by PreferencesScreen (Profile) and WeekView's ad-hoc panel — same two
// scheduling settings, same options, in both places.
// `labelKey` rather than a word: resolved with `t()` at render (see src/i18n).
export const WEEKDAYS: Array<{ value: Weekday; labelKey: string }> = [
  { value: 'monday', labelKey: 'prefs.weekday.monday' },
  { value: 'tuesday', labelKey: 'prefs.weekday.tuesday' },
  { value: 'wednesday', labelKey: 'prefs.weekday.wednesday' },
  { value: 'thursday', labelKey: 'prefs.weekday.thursday' },
  { value: 'friday', labelKey: 'prefs.weekday.friday' },
  { value: 'saturday', labelKey: 'prefs.weekday.saturday' },
  { value: 'sunday', labelKey: 'prefs.weekday.sunday' },
];

export const BATCH_DAYS_OPTIONS: Array<{ value: UserPreferences['meal_plan_batch_days']; labelKey: string }> = [
  { value: 1, labelKey: 'prefs.batch.1' },
  { value: 2, labelKey: 'prefs.batch.2' },
  { value: 3, labelKey: 'prefs.batch.3' },
];
