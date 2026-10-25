import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { View, StyleSheet, ScrollView, TouchableOpacity, ActivityIndicator } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import apiService from '../services/apiService';
import { GroceryCategory, GroceryLine, GroceryTick, PantryCategory } from '../types';
import { useTheme, Theme } from '../context/ThemeContext';
import { space, radius } from '../theme';
import { Text, SignInRequired } from '../components/ui';
import { useEscapeBack } from '../hooks/useEscapeBack';
import { usePreferences } from '../hooks/usePreferences';
import { useIsAuthenticated } from '../hooks/useIsAuthenticated';
import { useLanguage } from '../context/LanguageContext';
import { toISODate, addDays, startOfWeek } from '../utils/mealPlanDates';

type Props = NativeStackScreenProps<RootStackParamList, 'GroceryList'>;

// Aisle headings, in the order the server already sorts the lines into (DESIGN_SYSTEM §7.7).
// The words are in `src/i18n` under `grocery.aisle.*`; the server sends the category only.
const aisleLabelKey = (category: GroceryCategory) => `grocery.aisle.${category}`;

// Where a ticked-off line lands in the pantry (BACKLOG 7.2). A shopping aisle and a pantry
// shelf are not the same axis — everything chilled goes in the fridge whether it was bought
// from the meat counter or the dairy one — so this is a mapping, not a shared enum.
const PANTRY_SHELF: Record<GroceryCategory, PantryCategory> = {
  produce: 'produce',
  protein: 'fridge',
  dairy: 'fridge',
  pantry: 'spices_dry',
  other: 'spices_dry',
};

// Identity of a line across reloads: the server consolidates by item + unit, so that pair
// is what a tick is attached to. Not the index — the list reshuffles when the plan changes.
const lineKey = (line: GroceryLine) => `${line.item.toLowerCase()}|${line.unit ?? ''}`;

// "500 g", "2", "" — trailing zeroes dropped, and nothing at all when the amount is only
// a note ("to taste"), which renders separately. A line the pantry covers entirely has a
// quantity of 0 and renders as no amount at all: the caption below it says why.
const formatAmount = (line: GroceryLine): string => {
  if (line.quantity == null || line.quantity === 0) return '';
  return formatQuantity(line.quantity, line.unit);
};

const formatQuantity = (quantity: number, unit?: string | null): string => {
  const n = Math.round(quantity * 100) / 100;
  return unit ? `${n} ${unit}` : `${n}`;
};

// What the pantry takes off this line (BACKLOG 16.5), or null when it takes nothing. The
// server sends `need` only when it actually reduced the line, so this never renders
// "you have 0 of 3".
const pantryCovers = (line: GroceryLine): string | null =>
  line.need != null && line.quantity != null && line.need > line.quantity
    ? formatQuantity(line.need - line.quantity, line.unit)
    : null;

// How often a focused list re-reads the household's ticks. Two people in one shop are the
// case this exists for, and neither of them is watching the screen between aisles — ten
// seconds is under the time it takes to walk to the next one.
//
// ponytail: polling, not push. It is one request per member per ten seconds while the
// screen is open and nowhere else in the app; swap it for SSE off the chat stream's
// machinery if shopping sessions ever get long enough for that to show up in the bill.
const TICK_POLL_MS = 10_000;

/**
 * The week's shopping list (BACKLOG 6.1). Derived, never stored: every load re-aggregates the
 * plan as it stands, so dropping a meal drops its lines. Which lines are ticked off lives on
 * the server, scoped to the household (BACKLOG 15.9) — two people splitting one shop have to
 * see each other's ticks or one of them buys the milk twice — and a tick has one durable
 * effect besides: it adds the line to the pantry, and unticking removes it again (BACKLOG 7.2).
 */
const GroceryListScreen: React.FC<Props> = ({ navigation }) => {
  const authed = useIsAuthenticated();
  const prefs = usePreferences();
  const [lines, setLines] = useState<GroceryLine[] | null>(null);
  const [error, setError] = useState(false);
  // The household's ticks, keyed by line. Server state, mirrored here — the only local
  // edits are the optimistic ones in `toggle`, and `pending` protects those from being
  // stamped back over by a poll that was already in flight when the user tapped.
  const [checked, setChecked] = useState<Record<string, GroceryTick>>({});
  const pending = useRef(new Set<string>());
  const [anchor, setAnchor] = useState(() => new Date());
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  useEscapeBack();

  const weekStartISO = toISODate(startOfWeek(anchor, prefs?.week_start_day ?? 'monday'));
  const weekEndISO = toISODate(addDays(startOfWeek(anchor, prefs?.week_start_day ?? 'monday'), 6));

  const load = useCallback(() => {
    if (!authed) return;
    setError(false);
    apiService.getGroceryList(weekStartISO, weekEndISO)
      .then(list => setLines(list.lines))
      .catch(err => { console.error('Error loading grocery list:', err); setError(true); });
  }, [authed, weekStartISO, weekEndISO]);

  // Re-read on focus for the same reason WeekView does: the assistant writes to the plan
  // server-side, and this list is only ever as current as the plan it was built from.
  useEffect(load, [load]);
  useEffect(() => navigation.addListener('focus', load), [navigation, load]);

  // A poll never contradicts a tap the user has just made and the server has not answered
  // yet: those keys are left exactly as the optimistic update set them.
  const refreshTicks = useCallback(() => {
    if (!authed) return;
    apiService.getGroceryTicks(weekStartISO)
      .then(ticks => setChecked(prev => {
        const next: Record<string, GroceryTick> = {};
        for (const tick of ticks) next[tick.line_key] = tick;
        for (const key of pending.current) {
          if (prev[key]) next[key] = prev[key]; else delete next[key];
        }
        return next;
      }))
      .catch(err => console.error('Error loading grocery ticks:', err));
  }, [authed, weekStartISO]);

  // The other half of "two people in one shop": their ticks have to arrive without either
  // of them leaving the screen and coming back (BACKLOG 15.9). The interval is cleared on
  // blur, so a list left open in a background tab costs nothing.
  useEffect(refreshTicks, [refreshTicks]);
  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | null = null;
    const start = () => {
      refreshTicks();
      timer ??= setInterval(refreshTicks, TICK_POLL_MS);
    };
    const stop = () => { if (timer) { clearInterval(timer); timer = null; } };
    const unsubFocus = navigation.addListener('focus', start);
    const unsubBlur = navigation.addListener('blur', stop);
    if (navigation.isFocused()) start();
    return () => { stop(); unsubFocus(); unsubBlur(); };
  }, [navigation, refreshTicks]);

  // Ticking a line puts it in the pantry, unticking takes it back out (BACKLOG 7.2) — the
  // only way a pantry stays accurate without data entry. The pantry write is the server's
  // now (15.9): with two shoppers, one write per device is one pantry row per device, and
  // the first untick deletes a row the other still believes in.
  //
  // The tick is still applied immediately and never waits on the network — a shopper in an
  // aisle needs the line to go grey now. A failed write is rolled back on the next poll
  // rather than blocked on here.
  const toggle = (line: GroceryLine) => {
    const key = lineKey(line);
    const was = checked[key];
    setChecked(prev => {
      const next = { ...prev };
      if (was) delete next[key];
      // The optimistic stand-in: ticked, no pantry row yet, and mine.
      else next[key] = { line_key: key, mine: true };
      return next;
    });
    pending.current.add(key);
    const done = () => pending.current.delete(key);

    if (was) {
      apiService.untickGroceryLine(weekStartISO, key)
        .catch(err => console.error('Error unticking grocery line:', err))
        .finally(done);
      return;
    }
    // `need` and not `quantity`: the line was already reduced by what the kitchen holds
    // (BACKLOG 16.5), and the upsert REPLACES the pantry row's amount — storing the
    // shortfall would throw away the part you already had.
    apiService.tickGroceryLine(weekStartISO, key, {
      name: line.item,
      quantity: line.need ?? line.quantity ?? null,
      unit: line.unit ?? null,
      category: PANTRY_SHELF[line.category] ?? 'spices_dry',
      expires_on: null,
    })
      .then(tick => setChecked(prev => {
        // Only keep it if the line is still ticked — the user may have unticked it while
        // the write was in flight, in which case the server has already dropped it.
        if (!prev[key]) return prev;
        return { ...prev, [key]: tick };
      }))
      .catch(err => console.error('Error ticking grocery line:', err))
      .finally(done);
  };

  // The server sorts by aisle already, so grouping is a single pass that keeps that order.
  const sections = useMemo(() => {
    const out: Array<{ category: GroceryCategory; lines: GroceryLine[] }> = [];
    for (const line of lines ?? []) {
      const last = out[out.length - 1];
      if (last && last.category === line.category) last.lines.push(line);
      else out.push({ category: line.category, lines: [line] });
    }
    return out;
  }, [lines]);

  const remaining = (lines ?? []).filter(l => !checked[lineKey(l)]).length;

  if (authed === null) {
    return <ActivityIndicator style={styles.pad} color={theme.accent} />;
  }

  if (!authed) {
    return (
      <SignInRequired
        message={t('grocery.signInRequired')}
        onSignIn={() => navigation.navigate('Login')}
      />
    );
  }

  return (
    <View style={styles.container}>
      <View style={styles.weekNav}>
        <TouchableOpacity onPress={() => setAnchor(addDays(anchor, -7))} style={styles.navButton} accessibilityRole="button" accessibilityLabel={t('plan.previousWeek')}>
          <Ionicons name="chevron-back" size={20} color={theme.subtext} />
        </TouchableOpacity>
        <View style={styles.weekLabel}>
          <Text variant="label">{weekStartISO} → {weekEndISO}</Text>
          {lines !== null && <Text variant="caption" tone="subtle">{t('grocery.remaining', { remaining, total: lines.length })}</Text>}
        </View>
        <TouchableOpacity onPress={() => setAnchor(addDays(anchor, 7))} style={styles.navButton} accessibilityRole="button" accessibilityLabel={t('plan.nextWeek')}>
          <Ionicons name="chevron-forward" size={20} color={theme.subtext} />
        </TouchableOpacity>
      </View>

      {lines === null && !error ? (
        <ActivityIndicator style={styles.pad} color={theme.accent} />
      ) : error ? (
        <View style={styles.pad}>
          <Text tone="subtle">{t('grocery.loadFailed')}</Text>
          <TouchableOpacity onPress={load} accessibilityRole="button"><Text tone="accent">{t('common.retry')}</Text></TouchableOpacity>
        </View>
      ) : lines!.length === 0 ? (
        <View style={styles.pad}>
          <Text tone="subtle">{t('grocery.empty')}</Text>
          <TouchableOpacity onPress={() => navigation.navigate('MealPlan')} accessibilityRole="button"><Text tone="accent">{t('grocery.openMealPlan')}</Text></TouchableOpacity>
        </View>
      ) : (
        <ScrollView contentContainerStyle={styles.list}>
          {sections.map(section => (
            <View key={section.category} style={styles.section}>
              <Text variant="label" tone="subtle">{t(aisleLabelKey(section.category))}</Text>
              {section.lines.map(line => {
                const tick = checked[lineKey(line)];
                const isChecked = !!tick;
                const amount = formatAmount(line);
                const covers = pantryCovers(line);
                return (
                  <TouchableOpacity
                    key={lineKey(line)}
                    onPress={() => toggle(line)}
                    style={styles.row}
                    accessibilityRole="checkbox"
                    accessibilityState={{ checked: isChecked }}
                  >
                    <Ionicons
                      name={isChecked ? 'checkbox' : 'square-outline'}
                      size={22}
                      color={isChecked ? theme.accent : theme.muted}
                    />
                    <View style={styles.rowText}>
                      <Text style={isChecked ? styles.struck : undefined}>
                        {amount ? `${amount} · ` : ''}{line.item}{line.note ? ` (${line.note})` : ''}
                      </Text>
                      {covers && <Text variant="caption" tone="subtle">{t('grocery.inPantry', { have: covers, need: formatQuantity(line.need!, line.unit) })}</Text>}
                      {/* Only ever shown in a household: a solo cook's ticks are all their own. */}
                      {tick && !tick.mine && <Text variant="caption" tone="subtle">{t('grocery.byHousemate')}</Text>}
                      <Text variant="caption" tone="subtle">{t('grocery.forMeals', { meals: line.for.join(' · ') })}</Text>
                    </View>
                  </TouchableOpacity>
                );
              })}
            </View>
          ))}
        </ScrollView>
      )}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: { flex: 1, backgroundColor: t.bg },
  weekNav: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', padding: space.md },
  navButton: { padding: space.sm, borderRadius: radius.full },
  weekLabel: { alignItems: 'center' },
  pad: { padding: space.lg, gap: space.sm },
  list: { padding: space.lg, gap: space.xl, maxWidth: 720, width: '100%', alignSelf: 'center' },
  section: { gap: space.sm },
  row: { flexDirection: 'row', alignItems: 'flex-start', gap: space.md, paddingVertical: space.sm },
  rowText: { flex: 1 },
  struck: { textDecorationLine: 'line-through', color: t.muted },
});

export default GroceryListScreen;
