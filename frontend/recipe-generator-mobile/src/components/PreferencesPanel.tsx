import React, { useState, useMemo } from 'react';
import { View, Text, StyleSheet, TouchableOpacity, TextInput } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { UserPreferences, Weekday } from '../types';
import { TAGS_BY_GROUP, tagLabelKey } from '../constants/tags';
import { WEEKDAYS, BATCH_DAYS_OPTIONS } from '../constants/mealPlanPrefs';
import { useTheme, Theme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { type } from '../theme';
import { Chip } from './ui';

const SKILL_LEVELS: Array<NonNullable<UserPreferences['skill_level']>> = [
  'beginner', 'intermediate', 'advanced',
];

const DIETARY_CHIPS = TAGS_BY_GROUP.dietary;

interface Props {
  value: UserPreferences;
  onChange: (next: UserPreferences) => void;
}

// The full set of explicit profile settings. Preferences live in Profile only — BACKLOG
// 4.1 deleted WeekView's inline copy, which is what the `compact` size variant existed for.
const PreferencesPanel: React.FC<Props> = ({ value, onChange }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [dislikedInput, setDislikedInput] = useState('');

  const toggleDietaryPref = (slug: string) => {
    onChange({
      ...value,
      dietary_prefs: value.dietary_prefs.includes(slug)
        ? value.dietary_prefs.filter(s => s !== slug)
        : [...value.dietary_prefs, slug],
    });
  };

  const addDislikedIngredient = () => {
    const v = dislikedInput.trim().toLowerCase();
    if (!v || value.disliked_ingredients.includes(v)) { setDislikedInput(''); return; }
    onChange({ ...value, disliked_ingredients: [...value.disliked_ingredients, v] });
    setDislikedInput('');
  };

  const removeDislikedIngredient = (v: string) => {
    onChange({ ...value, disliked_ingredients: value.disliked_ingredients.filter(x => x !== v) });
  };

  // The two day lists are mutually exclusive: a day marked "no food" gets no assignment
  // at all, so also marking it "no cook" would have the carry-forward logic write a
  // leftover onto a day it was told to skip. Selecting in one list clears the other.
  const toggleDay = (field: 'meal_plan_no_food_days' | 'meal_plan_no_cook_days', day: Weekday) => {
    const other = field === 'meal_plan_no_food_days' ? 'meal_plan_no_cook_days' : 'meal_plan_no_food_days';
    const days = value[field];
    onChange({
      ...value,
      [field]: days.includes(day) ? days.filter(d => d !== day) : [...days, day],
      [other]: value[other].filter(d => d !== day),
    });
  };

  return (
    <View style={styles.container}>
      <Text style={styles.label}>{t('prefs.skillLevel')}</Text>
      <View style={styles.segmented}>
        {SKILL_LEVELS.map((v) => {
          const sel = value.skill_level === v;
          return (
            <TouchableOpacity
              key={v}
              style={[styles.segment, sel && styles.segmentSel]}
              onPress={() => onChange({ ...value, skill_level: sel ? null : v })}
            >
              <Text style={[styles.segmentText, sel && styles.segmentTextSel]}>{t(`prefs.skill.${v}`)}</Text>
            </TouchableOpacity>
          );
        })}
      </View>

      <Text style={styles.label}>{t('prefs.dietary')}</Text>
      <View style={styles.chipRow}>
        {DIETARY_CHIPS.map(tag => (
          <Chip
            key={tag.slug}
            label={t(tagLabelKey(tag.slug))}
            selected={value.dietary_prefs.includes(tag.slug)}
            onPress={() => toggleDietaryPref(tag.slug)}
          />
        ))}
      </View>

      <Text style={styles.label}>{t('prefs.dislikes')}</Text>
      <View style={styles.addRow}>
        <TextInput
          autoComplete="off"
          style={styles.addInput}
          placeholder={t('prefs.dislikesPlaceholder')}
          placeholderTextColor={theme.muted}
          value={dislikedInput}
          onChangeText={setDislikedInput}
          onSubmitEditing={addDislikedIngredient}
          returnKeyType="done"
        />
        <TouchableOpacity style={styles.addButton} onPress={addDislikedIngredient}>
          <Ionicons name="add" size={16} color={theme.onAccent} />
        </TouchableOpacity>
      </View>
      <View style={styles.chipRow}>
        {value.disliked_ingredients.map(item => (
          <Chip
            key={item}
            label={item}
            selected
            trailing={
              <TouchableOpacity onPress={() => removeDislikedIngredient(item)} hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}>
                <Ionicons name="close" size={12} color={theme.accent} />
              </TouchableOpacity>
            }
          />
        ))}
      </View>

      {/* Asked once, ever, and the number every planned day starts from (BACKLOG 9.12).
          Blank is a real answer — "never said" — so nothing claims a mismatch until it
          has been set. */}
      <Text style={styles.label}>{t('prefs.householdSize')}</Text>
      <View style={styles.chipRow}>
        {[1, 2, 3, 4, 5, 6].map(n => (
          <Chip
            key={n}
            label={String(n)}
            selected={value.household_size === n}
            onPress={() => onChange({ ...value, household_size: value.household_size === n ? null : n })}
          />
        ))}
      </View>

      <Text style={styles.label}>{t('prefs.noFoodDays')}</Text>
      <View style={styles.chipRow}>
        {WEEKDAYS.map(({ value: v, labelKey }) => (
          <Chip
            key={v}
            label={t(labelKey)}
            selected={value.meal_plan_no_food_days.includes(v)}
            onPress={() => toggleDay('meal_plan_no_food_days', v)}
          />
        ))}
      </View>

      <Text style={styles.label}>{t('prefs.noCookDays')}</Text>
      <View style={styles.chipRow}>
        {WEEKDAYS.map(({ value: v, labelKey }) => (
          <Chip
            key={v}
            label={t(labelKey)}
            selected={value.meal_plan_no_cook_days.includes(v)}
            onPress={() => toggleDay('meal_plan_no_cook_days', v)}
          />
        ))}
      </View>

      <Text style={styles.label}>{t('prefs.batchDays')}</Text>
      <View style={styles.segmented}>
        {BATCH_DAYS_OPTIONS.map(({ value: v, labelKey }) => {
          const sel = value.meal_plan_batch_days === v;
          return (
            <TouchableOpacity
              key={v}
              style={[styles.segment, sel && styles.segmentSel]}
              onPress={() => onChange({ ...value, meal_plan_batch_days: v })}
            >
              <Text style={[styles.segmentText, sel && styles.segmentTextSel]}>{t(labelKey)}</Text>
            </TouchableOpacity>
          );
        })}
      </View>

      <Text style={styles.label}>{t('prefs.weekStart')}</Text>
      <View style={styles.chipRow}>
        {WEEKDAYS.map(({ value: v, labelKey }) => (
          <Chip
            key={v}
            label={t(labelKey)}
            selected={value.week_start_day === v}
            onPress={() => onChange({ ...value, week_start_day: v })}
          />
        ))}
      </View>
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: { gap: 8 },
  label: {
    ...type.label, fontSize: 12, lineHeight: 16, color: t.muted,
    textTransform: 'uppercase', letterSpacing: 0.5, marginTop: 18, marginBottom: 4,
  },
  segmented: { flexDirection: 'row', gap: 8 },
  segment: {
    flex: 1, paddingVertical: 10, borderRadius: 10, borderWidth: 1,
    borderColor: t.border, backgroundColor: t.surface, alignItems: 'center',
  },
  segmentSel: { borderColor: t.accent, backgroundColor: t.accentFaded },
  segmentText: { ...type.body, fontSize: 13, color: t.subtext },
  // Selected swaps the whole variant, not a weight — the faces are single-weight (5.6).
  segmentTextSel: { ...type.label, fontSize: 13, color: t.accent },
  chipRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 8 },
  addRow: { flexDirection: 'row', gap: 8 },
  addInput: {
    flex: 1, borderWidth: 1, borderColor: t.border, borderRadius: 10,
    paddingHorizontal: 12, paddingVertical: 10, color: t.text, backgroundColor: t.surface,
    ...type.body, fontSize: 14,
  },
  addButton: { width: 40, borderRadius: 10, backgroundColor: t.accent, justifyContent: 'center', alignItems: 'center' },
});

export default PreferencesPanel;
