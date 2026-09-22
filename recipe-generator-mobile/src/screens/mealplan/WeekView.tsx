import React, { useState, useEffect, useMemo } from 'react';
import { Animated, View, StyleSheet, ScrollView, TouchableOpacity } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import apiService from '../../services/apiService';
import { Recipe, MealPlanItem, MealSlot } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useAlert } from '../../context/AlertContext';
import { useLanguage } from '../../context/LanguageContext';
import { useMealPlanContext } from '../../context/MealPlanContext';
import * as Clipboard from 'expo-clipboard';
import { toISODate, addDays, startOfWeek } from '../../utils/mealPlanDates';
import { planShareUrl } from '../../constants';
import { RootStackParamList } from '../../navigation/AppNavigator';
import RecipePickerModal from './RecipePickerModal';
import DayCard, { slotLabelKey } from './DayCard';
import type { PlannedMeal } from './DayCard';
import { buildWeek } from './planDays';
import { usePreferences } from '../../hooks/usePreferences';
import { radius, space } from '../../theme';
import { Button, Chip, Sheet, SheetRow, Text } from '../../components/ui';
import { useSwipe } from '../../hooks/useSwipe';

interface Props {
  selectedDate: Date;
  onChangeDate: (d: Date) => void;
  navigation: NativeStackScreenProps<RootStackParamList, 'MealPlan'>['navigation'];
}

/**
 * The week, as cards rather than a settings list.
 *
 * Three things drive the layout, all of them complaints about what this used to be:
 *  - **"What should I eat?" is the question**, so the answer leads the screen (`TodayBanner`,
 *    rendered above by `MealPlanScreen`) and the date navigation is a quiet row, not the
 *    dominant element it was when it sat in a bordered bar at the top.
 *  - **A repeated meal is deliberate**, and now says so — see `planDays.ts`.
 *  - **Changing a day should not cost three taps.** Swap opens the picker directly and Cook
 *    starts cooking; the sheet holds only what is left (ask the assistant, clear).
 *
 * Everything conversational still happens in the chat thread (BACKLOG 4.2) — this screen shows
 * what is saved and hands over a prompt.
 */
const WeekView: React.FC<Props> = ({ selectedDate, onChangeDate, navigation }) => {
  const prefs = usePreferences();
  const weekStart = useMemo(() => startOfWeek(selectedDate, prefs?.week_start_day ?? 'monday'), [selectedDate, prefs?.week_start_day]);
  // One day before the window: a carried-forward Monday takes its dish from Sunday, and without
  // that day loaded the first card of every week mislabels itself as freshly cooked.
  const fetchStartISO = toISODate(addDays(weekStart, -1));
  const weekStartISO = toISODate(weekStart);
  const weekEndISO = toISODate(addDays(weekStart, 6));

  const { itemsByDate, recipes, ensureRange, refreshRecipes, upsertItem, removeItem, shareToken, setShareToken } = useMealPlanContext();
  // A sheet and a picker are both about one *slot* of one day since BACKLOG 6.4, so both
  // carry the slot they were opened for. The sheet holds the day+slot rather than the
  // PlannedMeal itself: a snapshot goes stale the moment the stepper writes, so the number
  // never moved and every tap re-sent the same value (BACKLOG 9.8).
  const [sheetKey, setSheetKey] = useState<{ iso: string; slot: MealSlot } | null>(null);
  // The slot is undefined when the picker was opened by "Add a meal": the picker asks for it
  // with a row of chips instead of a whole sheet of its own (BACKLOG 12.5).
  const [picker, setPicker] = useState<{ iso: string; slot?: MealSlot } | null>(null);
  // The batch-cook control (BACKLOG 9.13): which cooking day is being extended, and the
  // days ticked so far. One control, two directions — unticking a day is the undo.
  const [covers, setCovers] = useState<{ item: MealPlanItem; picked: string[] } | null>(null);

  // Changing the week is a swipe on the week strip, not only a pair of 16px chevrons. It sits
  // on the strip rather than over the day cards on purpose: the cards swipe too (a meal's
  // Swap and Clear), and two horizontal gestures stacked on the same pixels means whichever
  // wins the responder is the one the user did not mean.
  const weekSwipe = useSwipe({
    axis: 'x',
    threshold: 56,
    onLeft: () => onChangeDate(addDays(selectedDate, 7)),
    onRight: () => onChangeDate(addDays(selectedDate, -7)),
  });

  const { theme } = useTheme();
  const { showAlert, confirmAction } = useAlert();
  const { t, locale } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  useEffect(() => { ensureRange(fetchStartISO, weekEndISO); }, [fetchStartISO, weekEndISO, ensureRange]);

  // The assistant writes to the plan server-side, so re-read the week whenever we come
  // back from the chat thread instead of trusting the cache we left with.
  // The assistant also *saves* recipes, and the library was fetched once per provider mount,
  // so a recipe saved from chat was missing from the picker entirely (BACKLOG 9.9).
  useEffect(() => navigation.addListener('focus', () => {
    ensureRange(fetchStartISO, weekEndISO);
    refreshRecipes();
  }), [navigation, fetchStartISO, weekEndISO, ensureRange, refreshRecipes]);

  const askAssistant = (prompt: string) => {
    setSheetKey(null);
    navigation.navigate('Chat', { prompt });
  };

  // Writing to a taken slot is a delete plus an insert server-side (storage/mealplan.go),
  // so ask first — the agent path already does, and the UI path silently destroyed a meal
  // (BACKLOG 9.10). An empty slot still writes silently: adding is not a decision.
  const assign = async (recipe: Recipe, slot: MealSlot) => {
    if (!picker) return;
    const { iso } = picker;
    const taken = mealAt(iso, slot);
    setPicker(null);
    if (taken) {
      const ok = await confirmAction(
        t('plan.replaceMealTitle'),
        t('plan.replaceMealBody', {
          slot: t(slotLabelKey(slot)),
          day: dayOf(iso)?.weekday ?? iso,
          current: taken.title,
          next: recipe.recipename,
        }),
        { confirmLabel: t('plan.replace'), destructive: true },
      );
      if (!ok) return;
    }
    try {
      upsertItem(await apiService.addMealPlanItem(recipe.id, iso, undefined, slot));
    } catch (error) {
      console.error('Error assigning recipe:', error);
      showAlert(t('common.error'), t('plan.addFailed'), 'error');
    }
  };

  // Servings are set by re-assigning the same recipe to the same day and slot: POST
  // /meal-plan/items upserts on exactly that key, so there is no separate endpoint to
  // call (BACKLOG 6.2).
  const setServings = async (item: MealPlanItem, servings: number) => {
    if (servings < 1 || servings > 99) return;
    try {
      upsertItem(await apiService.addMealPlanItem(item.recipe_id, item.planned_on, servings, item.meal_slot));
    } catch (error) {
      console.error('Error setting servings:', error);
      showAlert(t('common.error'), t('plan.servingsFailed'), 'error');
    }
  };

  // "Cook once, eat three days", as one atomic write: the cooking day scales to feed the
  // household for every day it covers, and each covered day becomes a leftover of it.
  // This is the only place a leftover's quantity is ever edited — you increase the cook's,
  // on the day it happens, and the leftovers follow (BACKLOG 9.13).
  const applyCovers = async () => {
    if (!covers) return;
    const { item, picked } = covers;
    const replacing = picked
      .map(iso => mealAt(iso, item.meal_slot))
      .filter(m => m && m.item.source_item_id !== item.id);
    setCovers(null);
    if (replacing.length) {
      const ok = await confirmAction(
        t('plan.replaceMealsTitle'),
        t('plan.replaceMealsBody', {
          meals: replacing.map(m => m!.title).join(', '),
          recipe: item.recipe_title,
        }),
        { confirmLabel: t('plan.replace'), destructive: true },
      );
      if (!ok) return;
    }
    const household = prefs?.household_size ?? null;
    try {
      await apiService.setBatchCook(
        item.id,
        picked,
        household != null ? household * (1 + picked.length) : undefined,
      );
      // The write touches rows on days other than the one edited, so re-read the window
      // rather than patching the cache day by day.
      ensureRange(fetchStartISO, weekEndISO);
    } catch (error) {
      console.error('Error setting batch cook:', error);
      showAlert(t('common.error'), t('plan.coversFailed'), 'error');
    }
  };

  // Marking a meal cooked is the one action here that reaches outside the plan: the meal
  // leaves the week's grocery list and its ingredients come out of the pantry, in one
  // server-side transaction. Both halves are reported back rather than applied quietly —
  // an app that edits your fridge without saying so is one you stop trusting, and the
  // deduction refuses to guess at anything it cannot convert.
  const markCooked = async (meal: PlannedMeal) => {
    setSheetKey(null);
    try {
      const { pantry } = await apiService.markMealCooked(meal.item.id);
      upsertItem({ ...meal.item, cooked_at: new Date().toISOString() });
      // A batch cook's leftovers read their state from this row, so re-read the window
      // rather than patching each covered day by hand.
      if (meal.covers > 0) ensureRange(fetchStartISO, weekEndISO);

      const changes = pantry.applied.map(c => (c.removed
        ? t('plan.pantryUsedUp', { name: c.name })
        : `−${c.used}${c.unit ? ` ${c.unit}` : ''} ${c.name}`)).join(' · ');
      const left = pantry.skipped.length
        ? t('plan.pantryLeftAlone', { count: pantry.skipped.length, names: pantry.skipped.join(', ') })
        : '';
      showAlert(
        changes ? t('plan.pantryUpdated', { changes }) : t('plan.pantryUnchanged'),
        left || undefined,
        'success',
      );
    } catch (error) {
      console.error('Error marking meal cooked:', error);
      showAlert(t('common.error'), t('plan.cookedFailed'), 'error');
    }
  };

  const uncook = async (meal: PlannedMeal) => {
    try {
      await apiService.unmarkMealCooked(meal.item.id);
      upsertItem({ ...meal.item, cooked_at: null });
      if (meal.covers > 0) ensureRange(fetchStartISO, weekEndISO);
      // Say the part the user cannot see: the meal is back, the pantry is not.
      showAlert(t('plan.undoKeepsPantry'), undefined, 'success');
    } catch (error) {
      console.error('Error unmarking meal cooked:', error);
      showAlert(t('common.error'), t('plan.cookedFailed'), 'error');
    }
  };

  const clear = async (item: MealPlanItem) => {
    setSheetKey(null);
    try {
      await apiService.deleteMealPlanItem(item.id);
      removeItem(item);
    } catch (error) {
      console.error('Error removing meal plan item:', error);
      showAlert(t('common.error'), t('plan.removeFailed'), 'error');
    }
  };

  const recipesById = useMemo(
    () => Object.fromEntries(recipes.map(r => [r.id, r])) as Record<number, Recipe>,
    [recipes],
  );

  const days = useMemo(() => buildWeek({
    locale,
    weekStart,
    today: new Date(),
    itemsByDate,
    priorDay: itemsByDate[fetchStartISO] ?? [],
    recipesById,
    noCookDays: prefs?.meal_plan_no_cook_days ?? [],
  }), [locale, weekStart, itemsByDate, fetchStartISO, recipesById, prefs?.meal_plan_no_cook_days]);

  const dayOf = (iso: string) => days.find(d => d.iso === iso);
  const mealAt = (iso: string, slot: MealSlot) => dayOf(iso)?.meals.find(m => m.slot === slot);

  // The week as a page anyone can read (BACKLOG 13.2). Share on: issue (or reuse) the
  // token and put pasteable text on the clipboard. Share off: revoke, which kills the
  // link everyone already has — the same contract a shared recipe has had since 8.4.
  const toggleWeekShare = async () => {
    try {
      if (shareToken) {
        if (!(await confirmAction(t('library.stopSharingTitle'), t('library.stopSharingBody'),
          { confirmLabel: t('recipes.stopSharing'), destructive: true }))) return;
        await apiService.unsharePlan(weekStartISO);
        setShareToken('');
        showAlert(t('library.linkRevoked'), t('library.linkRevokedBody'), 'success');
        return;
      }
      const token = await apiService.sharePlan(weekStartISO, weekEndISO);
      setShareToken(token);
      const url = planShareUrl(token);
      await Clipboard.setStringAsync(`${t('shared.planTitle')} ${weekStartISO} – ${weekEndISO}\n${url}`);
      showAlert(t('plan.weekLinkCopied'), t('plan.shareWeekBody'), 'success');
    } catch (error) {
      console.error('Error sharing week:', error);
      showAlert(t('common.error'), t('library.shareFailed'), 'error');
    }
  };

  // Re-derived from `days` on every render, so a write through upsertItem is visible in the
  // open sheet (BACKLOG 9.8).
  const sheetDay = sheetKey ? days.find(d => d.iso === sheetKey.iso) : undefined;
  const sheetMeal = sheetKey ? sheetDay?.meals.find(m => m.slot === sheetKey.slot) ?? null : null;
  const sheetItem = sheetMeal?.item ?? null;
  const sheetWeekday = sheetDay?.weekday ?? '';
  const planned = days.filter(d => d.meals.length > 0).length;
  const showsThisWeek = weekStartISO === toISODate(startOfWeek(new Date(), prefs?.week_start_day ?? 'monday'));
  // Unset servings mean "as the recipe is written", so the stepper starts from what the
  // recipe yields (or 2, when the library cache doesn't know) rather than from zero.
  const sheetServings = sheetItem?.servings ?? sheetMeal?.recipe?.structured?.servings ?? 2;

  return (
    <View style={styles.container}>
      <ScrollView contentContainerStyle={styles.list}>
        <Animated.View
          style={[styles.weekNav, { transform: [{ translateX: weekSwipe.offset }] }]}
          {...weekSwipe.panHandlers}
          accessibilityHint={t('plan.swipeWeekHint')}
        >
          <TouchableOpacity
            onPress={() => onChangeDate(addDays(selectedDate, -7))}
            style={styles.navButton}
            accessibilityRole="button"
            accessibilityLabel={t('plan.previousWeek')}
          >
            <Ionicons name="chevron-back" size={16} color={theme.subtext} />
          </TouchableOpacity>
          <View style={styles.weekLabel}>
            <Text variant="caption" tone="subtle">
              {weekStart.toLocaleDateString(locale, { month: 'short', day: 'numeric' })}
              {' – '}
              {addDays(weekStart, 6).toLocaleDateString(locale, { month: 'short', day: 'numeric' })}
              {`  ·  ${t('plan.plannedOfSeven', { count: planned })}`}
            </Text>
            {/* There was no way back to the current week once you had paged away. */}
            {!showsThisWeek && (
              <TouchableOpacity onPress={() => onChangeDate(new Date())} accessibilityRole="button">
                <Text variant="caption" tone="accent">{t('plan.backToThisWeek')}</Text>
              </TouchableOpacity>
            )}
          </View>
          <TouchableOpacity
            onPress={() => onChangeDate(addDays(selectedDate, 7))}
            style={styles.navButton}
            accessibilityRole="button"
            accessibilityLabel={t('plan.nextWeek')}
          >
            <Ionicons name="chevron-forward" size={16} color={theme.subtext} />
          </TouchableOpacity>
        </Animated.View>

        {days.map(day => (
          <DayCard
            key={day.iso}
            day={day}
            onCook={recipe => navigation.navigate('CookingMode', { recipe })}
            onSwap={slot => setPicker({ iso: day.iso, slot })}
            onMore={meal => setSheetKey({ iso: meal.item.planned_on, slot: meal.slot })}
            onAdd={() => setPicker({ iso: day.iso })}
            onClear={meal => clear(meal.item)}
            householdSize={prefs?.household_size ?? null}
            onScale={(meal, servings) => setServings(meal.item, servings)}
            onUncook={uncook}
          />
        ))}

        <Button
          title={t(planned === 0 ? 'plan.planMyWeek' : 'plan.rePlanWeek')}
          variant="secondary"
          fullWidth
          icon={<Ionicons name="sparkles" size={16} color={theme.text} />}
          onPress={() => askAssistant(`Plan my week starting ${weekStartISO}, using my saved recipes.`)}
        />

        <Button
          title={t(shareToken ? 'plan.stopSharingWeek' : 'plan.shareWeek')}
          variant="secondary"
          fullWidth
          icon={<Ionicons name={shareToken ? 'link' : 'link-outline'} size={16} color={theme.text} />}
          onPress={toggleWeekShare}
        />

        <TouchableOpacity
          style={styles.prefsLinkRow}
          onPress={() => navigation.navigate('Preferences')}
          accessibilityRole="button"
        >
          <Text variant="caption" tone="accent">{t('plan.planningPreferences')}</Text>
          <Ionicons name="chevron-forward" size={14} color={theme.accent} />
        </TouchableOpacity>
      </ScrollView>

      <Sheet visible={sheetMeal !== null} onClose={() => setSheetKey(null)} title={sheetWeekday}>
        {sheetItem && (
          <View style={styles.servingsRow}>
            <Text variant="body">{t('plan.cookingFor')}</Text>
            <View style={styles.stepper}>
              <TouchableOpacity
                style={styles.stepperBtn}
                onPress={() => setServings(sheetItem, sheetServings - 1)}
                accessibilityRole="button"
                accessibilityLabel={t('plan.oneFewerServing')}
              >
                <Ionicons name="remove" size={16} color={theme.accent} />
              </TouchableOpacity>
              <Text variant="title">{sheetServings}</Text>
              <TouchableOpacity
                style={styles.stepperBtn}
                onPress={() => setServings(sheetItem, sheetServings + 1)}
                accessibilityRole="button"
                accessibilityLabel={t('plan.oneMoreServing')}
              >
                <Ionicons name="add" size={16} color={theme.accent} />
              </TouchableOpacity>
            </View>
          </View>
        )}
        {sheetMeal && (
          <SheetRow
            label={t('plan.askAssistant')}
            onPress={() => askAssistant(
              `Change ${sheetWeekday} ${sheetMeal.item.planned_on} ${sheetMeal.slot} — it's currently ${sheetMeal.title}.`)}
          />
        )}
        {/* Only a cook can be marked: a leftover ate a pot that was already deducted, so
            marking one would take a second set of ingredients for a meal that used none. */}
        {sheetMeal?.kind === 'cook' && !sheetMeal.cooked && (
          <SheetRow label={t('plan.markCooked')} onPress={() => markCooked(sheetMeal)} />
        )}
        {sheetItem && sheetMeal?.kind === 'cook' && (
          <SheetRow
            label={t('plan.cookForMoreDays')}
            onPress={() => {
              setCovers({
                item: sheetItem,
                picked: days.flatMap(d => d.meals)
                  .filter(m => m.item.source_item_id === sheetItem.id)
                  .map(m => m.item.planned_on),
              });
              setSheetKey(null);
            }}
          />
        )}
        {sheetItem && <SheetRow label={t('common.clear')} destructive onPress={() => clear(sheetItem)} />}
      </Sheet>

      {/* Tick the days this pot covers. Reopening it on a day that already batch-cooks
          shows what it covers now, so unticking is the undo (BACKLOG 9.13). */}
      <Sheet visible={covers !== null} onClose={() => setCovers(null)} title={t('plan.whichDaysCovered')}>
        <View style={styles.coverDays}>
          {days.filter(d => covers && d.iso > covers.item.planned_on).map(d => (
            <Chip
              key={d.iso}
              label={d.weekday}
              selected={covers!.picked.includes(d.iso)}
              onPress={() => setCovers(c => c && ({
                ...c,
                picked: c.picked.includes(d.iso) ? c.picked.filter(x => x !== d.iso) : [...c.picked, d.iso],
              }))}
            />
          ))}
        </View>
        {prefs?.household_size != null && covers && (
          <Text variant="caption" tone="subtle">
            {t('plan.coversSummary', {
              total: prefs.household_size * (1 + covers.picked.length),
              people: prefs.household_size,
              days: 1 + covers.picked.length,
            })}
          </Text>
        )}
        <Button title={t('plan.confirm')} fullWidth onPress={applyCovers} />
      </Sheet>

      <RecipePickerModal
        visible={picker !== null}
        recipes={recipes}
        slot={picker?.slot}
        takenSlots={picker ? Object.fromEntries(
          (dayOf(picker.iso)?.meals ?? []).map(m => [m.slot, m.title]),
        ) as Partial<Record<MealSlot, string>> : {}}
        onSelect={assign}
        onClose={() => setPicker(null)}
      />
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: { flex: 1, backgroundColor: t.bg },
  list: { padding: space.md, gap: space.md },
  weekNav: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingVertical: space.sm,
  },
  weekLabel: { alignItems: 'center', gap: 2 },
  navButton: { padding: space.xs },
  servingsRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingVertical: space.sm,
  },
  stepper: { flexDirection: 'row', alignItems: 'center', gap: space.md },
  stepperBtn: {
    padding: space.sm,
    borderRadius: radius.full,
    borderWidth: 1,
    borderColor: t.border,
  },
  coverDays: { flexDirection: 'row', flexWrap: 'wrap', gap: space.xs, paddingVertical: space.sm },
  prefsLinkRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: space.xs,
    paddingVertical: space.sm,
  },
});

export default WeekView;
