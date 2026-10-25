import React, { useEffect, useState } from 'react';
import { ScrollView, View, StyleSheet } from 'react-native';
import apiService from '../services/apiService';
import { SharedPlan } from '../types';
import Loading from '../components/Loading';
import { Text as UIText } from '../components/ui';
import { useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { parseISODate } from '../utils/mealPlanDates';

// The whole of a plan link's destination (BACKLOG 13.2): one week and its shopping list,
// read-only, no auth, no nav chrome — the same shape SharedRecipeScreen has had since 8.4,
// rendered by App.tsx when the URL is /p/<token>.
//
// The use it exists for is coordination with someone who does not have the app, so it
// answers exactly two questions: what are we eating, and what do we have to buy.
const SharedPlanScreen: React.FC<{ token: string }> = ({ token }) => {
  const { theme } = useTheme();
  const { t, locale } = useLanguage();
  const [plan, setPlan] = useState<SharedPlan | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    apiService.getSharedPlan(token).then(setPlan).catch(() => setError(true));
  }, [token]);

  if (error) {
    return (
      <View style={[styles.center, { backgroundColor: theme.bg }]}>
        <UIText variant="title">{t('shared.linkUnavailable')}</UIText>
        <UIText tone="muted" style={styles.sub}>{t('shared.planGone')}</UIText>
      </View>
    );
  }

  if (!plan) return <Loading visible message={t('shared.loadingPlan')} />;

  const dayFormat = { weekday: 'long', month: 'short', day: 'numeric' } as const;
  // One block per planned day, in the order the server already sorted them (day, then
  // meal of day). Days nobody planned simply do not appear — an empty row is noise on a
  // page whose whole job is "here is the week".
  const days = plan.items.reduce<{ iso: string; items: typeof plan.items }[]>((acc, item) => {
    const last = acc[acc.length - 1];
    if (last && last.iso === item.planned_on) last.items.push(item);
    else acc.push({ iso: item.planned_on, items: [item] });
    return acc;
  }, []);

  return (
    <ScrollView style={{ backgroundColor: theme.bg }} contentContainerStyle={styles.content}>
      <UIText variant="title">{t('shared.planTitle')}</UIText>
      <UIText variant="caption" tone="muted" style={styles.range}>
        {parseISODate(plan.starts_on).toLocaleDateString(locale, { month: 'short', day: 'numeric' })}
        {' – '}
        {parseISODate(plan.ends_on).toLocaleDateString(locale, { month: 'short', day: 'numeric' })}
      </UIText>

      {days.length === 0 && <UIText tone="muted" style={styles.sub}>{t('shared.planEmpty')}</UIText>}

      {days.map(day => (
        <View key={day.iso} style={[styles.block, { borderColor: theme.border }]}>
          <UIText variant="caption" tone="subtle">
            {parseISODate(day.iso).toLocaleDateString(locale, dayFormat)}
          </UIText>
          {day.items.map(item => (
            <UIText key={item.id} variant="body" style={styles.row}>
              {`${t(`plan.slot.${item.meal_slot}`)} · ${item.recipe_title}`}
              {item.servings ? ` · ${t('plan.servingsCount', { count: item.servings })}` : ''}
            </UIText>
          ))}
        </View>
      ))}

      {plan.lines.length > 0 && (
        <View style={[styles.block, { borderColor: theme.border }]}>
          <UIText variant="caption" tone="subtle">{t('shared.planGroceries')}</UIText>
          {plan.lines.map(line => (
            <UIText key={`${line.item}|${line.unit ?? ''}`} variant="body" style={styles.row}>
              {line.quantity ? `${Math.round(line.quantity * 100) / 100}${line.unit ? ` ${line.unit}` : ''} · ` : ''}
              {line.item}
            </UIText>
          ))}
        </View>
      )}

      <UIText variant="caption" tone="muted" style={styles.sub}>{t('shared.fromApp')}</UIText>
    </ScrollView>
  );
};

const styles = StyleSheet.create({
  center: { flex: 1, alignItems: 'center', justifyContent: 'center', padding: 24 },
  content: { padding: 20, paddingBottom: 48, maxWidth: 760, width: '100%', alignSelf: 'center' },
  range: { marginTop: 4, marginBottom: 16 },
  block: { borderWidth: StyleSheet.hairlineWidth, borderRadius: 12, padding: 14, marginBottom: 12, gap: 4 },
  row: { marginTop: 2 },
  sub: { marginTop: 12, textAlign: 'center' },
});

export default SharedPlanScreen;
