import React, { useEffect, useMemo } from 'react';
import { View, TouchableOpacity, StyleSheet } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { useMealPlanContext } from '../../context/MealPlanContext';
import { toISODate } from '../../utils/mealPlanDates';
import { totalTimeMinutes } from '../../utils/recipeTime';
import { RootStackParamList } from '../../navigation/AppNavigator';
import { MealSlot } from '../../types';
import { Badge, Text } from '../../components/ui';
import { radius, space } from '../../theme';

// Roughly when each meal stops being the thing you cook next. Only used to pick which of
// today's meals the banner shows — nothing is scheduled off it, so being an hour out
// costs a banner that reads one meal ahead, not a notification at the wrong time.
const slotEndsByHour: Record<MealSlot, number> = { breakfast: 11, lunch: 15, dinner: 22, snack: 22 };

interface Props {
  navigation: NativeStackScreenProps<RootStackParamList, 'MealPlan'>['navigation'];
}

/**
 * The answer to "what should I eat?", which is the question the planner exists to answer — so it
 * leads the screen and the date navigation does not. Before this it was a thin strip under a
 * bordered week-picker bar, which put the chrome above the content.
 *
 * Also the only place a plan turns into cooking: decide → cook is one tap from here, not a
 * separate feature reached from another screen. (Shopping, the middle step, is its own
 * destination now — `GroceryListScreen`, BACKLOG 6.1.)
 *
 * Lives inside MealPlanProvider so it reads today's item from the shared cache. It used to also
 * schedule a local push at the item's start_time — dropped with the column in BACKLOG 9.5, since
 * nothing in the client could ever set that time.
 */
const TodayBanner: React.FC<Props> = ({ navigation }) => {
  const { itemsByDate, recipes, ensureRange } = useMealPlanContext();
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  const todayISO = toISODate(new Date());
  useEffect(() => { ensureRange(todayISO, todayISO); }, [todayISO, ensureRange]);

  // A day can hold several meals now (BACKLOG 6.4). The banner answers "what should I
  // cook next", so it takes the first of today's meals whose start time is still ahead,
  // and falls back to the last one once the day is over.
  // The server returns a day's meals in slot order, so the first whose slot has not yet
  // passed is the next one to cook.
  const todayMeals = itemsByDate[todayISO] ?? [];
  const nowHour = new Date().getHours();
  const item = todayMeals.find(m => nowHour < slotEndsByHour[m.meal_slot]) ?? todayMeals[todayMeals.length - 1] ?? null;
  const fullRecipe = item ? recipes.find(r => r.id === item.recipe_id) : undefined;
  const totalMinutes = totalTimeMinutes(fullRecipe?.structured);

  // An unplanned today is still an answer, and the useful one is "ask, and it will be planned".
  if (!item) {
    return (
      <View style={styles.hero}>
        <Text variant="caption" tone="muted">{t('plan.tonight')}</Text>
        <Text variant="display">{t('plan.nothingPlannedYet')}</Text>
        <TouchableOpacity
          style={styles.cookButton}
          onPress={() => navigation.navigate('Chat', { prompt: t('plan.whatToCookPrompt') })}
          accessibilityRole="button"
        >
          <Ionicons name="sparkles" size={16} color={theme.onAccent} />
          <Text variant="label" style={{ color: theme.onAccent }}>{t('plan.askWhatToCook')}</Text>
        </TouchableOpacity>
      </View>
    );
  }

  const doc = fullRecipe?.structured;

  return (
    <View style={styles.hero}>
      <Text variant="caption" tone="muted">{item.meal_slot.toUpperCase()}</Text>
      <Text variant="display" numberOfLines={2}>{item.recipe_title}</Text>
      {doc?.summary ? <Text variant="body" tone="subtle" numberOfLines={2}>{doc.summary}</Text> : null}

      <View style={styles.badges}>
        {totalMinutes > 0 && (
          <Badge
            label={t('plan.minutes', { minutes: totalMinutes })}
            icon={<Ionicons name="time-outline" size={12} color={theme.accent} />}
          />
        )}
        {doc?.servings != null && (
          <Badge
            label={t('plan.servingsCount', { count: doc.servings })}
            icon={<Ionicons name="people-outline" size={12} color={theme.accent} />}
          />
        )}
      </View>

      {fullRecipe && (
        <TouchableOpacity
          style={styles.cookButton}
          onPress={() => navigation.navigate('CookingMode', { recipe: fullRecipe })}
          accessibilityRole="button"
        >
          <Ionicons name="flame-outline" size={16} color={theme.onAccent} />
          <Text variant="label" style={{ color: theme.onAccent }}>{t('plan.startCooking')}</Text>
        </TouchableOpacity>
      )}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  hero: {
    gap: space.sm,
    margin: space.md,
    marginBottom: 0,
    padding: space.lg,
    borderRadius: radius.lg,
    backgroundColor: t.surface,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: t.border,
  },
  badges: { flexDirection: 'row', flexWrap: 'wrap', gap: space.xs },
  cookButton: {
    flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space.sm,
    alignSelf: 'flex-start', marginTop: space.xs,
    backgroundColor: t.accent,
    paddingHorizontal: space.lg, paddingVertical: space.md - 2,
    borderRadius: radius.full,
  },
});

export default TodayBanner;
