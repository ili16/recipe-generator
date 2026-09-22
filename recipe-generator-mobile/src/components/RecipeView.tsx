import React, { useMemo, useState } from 'react';
import { View, Text, StyleSheet, TouchableOpacity } from 'react-native';
import Markdown from 'react-native-markdown-display';
import { Ionicons } from '@expo/vector-icons';
import { Theme, useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { type, space, radius } from '../theme';
import { Badge, Sheet, SheetRow } from './ui';
import { RecipeDocument } from '../types';
import { totalTimeMinutes } from '../utils/recipeTime';
import { fmtIngredient, scaleIngredient, servingScale, Ingredient } from '../utils/recipeIngredient';


// Themed style object for react-native-markdown-display — the real "render actual
// markdown" fallback used wherever there's no structured doc to display instead.
export const recipeMarkdownStyles = (t: Theme) => ({
  body: { color: t.text, ...type.body, fontSize: 14, lineHeight: 21 },
  heading1: { color: t.text, ...type.title, marginTop: 12, marginBottom: 6 },
  heading2: { color: t.text, ...type.title, fontSize: 17, lineHeight: 24, marginTop: 12, marginBottom: 6 },
  heading3: { color: t.text, ...type.title, fontSize: 15, lineHeight: 22, marginTop: 10, marginBottom: 4 },
  paragraph: { color: t.subtext, ...type.body, fontSize: 14, lineHeight: 21, marginTop: 0, marginBottom: 8 },
  // `strong` only overlays the body face, so it keeps a weight rather than a family.
  strong: { color: t.text, fontWeight: '700' as const },
  em: { color: t.subtext, fontStyle: 'italic' as const },
  bullet_list: { marginBottom: 8 },
  ordered_list: { marginBottom: 8 },
  list_item: { color: t.subtext, ...type.body, fontSize: 14, marginBottom: 4 },
  bullet_list_icon: { color: t.accent },
  ordered_list_icon: { color: t.accent },
  hr: { backgroundColor: t.border, height: StyleSheet.hairlineWidth, marginVertical: 10 },
});

interface Props {
  structured?: RecipeDocument | null;
  markdown: string;
  // Cooking mode's overview walks the steps one screen at a time, so it renders the
  // ingredients here and leaves the steps to StepPhase.
  showSteps?: boolean;
  // How many people the cook wants to feed (BACKLOG 17.3). Nothing is written: this
  // multiplies what is displayed and the saved recipe never moves, which also keeps the
  // meal plan's own scaling — which scales from the STORED yield — honest.
  //
  // Optional and controlled-if-given. Cooking mode passes the session's value so the
  // overview and the step cards cannot disagree; everywhere else RecipeView holds it and
  // it resets when the screen closes, which is the intent of a view-only scaler.
  servings?: number | null;
  onServingsChange?: (servings: number) => void;
  // Opt-in, because a stepper is wrong on two of the five screens that render a recipe:
  // RefinedPhase is reviewing what the AI just changed, and the chat artifact card is a
  // draft. A scaler there invites "did it scale, or did the model rewrite the amounts?"
  // -- a question this component should never make a reader ask.
  scalable?: boolean;
  // Swapping one ingredient for another the kitchen actually holds (BACKLOG 17.4c).
  // Opt-in and saved-recipes-only: a draft has no id to file a swap under, and a shared
  // recipe is somebody else's kitchen. sortOrder is the line's 1-based position, and an
  // empty replacement means "as written".
  onSwap?: (sortOrder: number, replacement: string) => void;
}

const RecipeView: React.FC<Props> = ({ structured, markdown, showSteps = true, servings, onServingsChange, scalable = false, onSwap }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  // Hold the number, derive the lines — never the other way round (the 9.8 rule). The
  // scaled ingredients are recomputed on every render from one scalar.
  const [ownServings, setOwnServings] = useState<number | null>(null);
  // Which line's swap sheet is open, null for none.
  const [swapping, setSwapping] = useState<{ sortOrder: number; ing: Ingredient } | null>(null);
  const [hideInPantry, setHideInPantry] = useState(false);
  const baseServings = structured?.servings ?? null;
  const wanted = servings ?? ownServings ?? baseServings;
  const setWanted = (n: number) => {
    if (n < 1) return; // cooking for nobody is not a thing you can step to
    setOwnServings(n);
    onServingsChange?.(n);
  };
  const scale = scalable ? servingScale(wanted, baseServings) : 1;

  const hasContent = structured && (structured.ingredients.length > 0 || structured.steps.length > 0);
  if (!hasContent) {
    return <Markdown style={recipeMarkdownStyles(theme)}>{markdown}</Markdown>;
  }

  const meta: Array<{ icon: React.ComponentProps<typeof Ionicons>['name']; text: string }> = [];
  if (structured!.servings != null) meta.push({ icon: 'people-outline', text: `${structured!.servings} servings` });
  const totalMinutes = totalTimeMinutes(structured);
  if (totalMinutes > 0) meta.push({ icon: 'time-outline', text: `${totalMinutes} min` });
  if (structured!.difficulty) meta.push({ icon: 'speedometer-outline', text: structured!.difficulty });

  // Per-serving macros, each dropped when the model couldn't estimate it. Nothing at
  // all renders when all four are null — a blank beats a confident 0 (BACKLOG 6.6).
  const macros = ([
    ['kcal', structured!.calories],
    ['g protein', structured!.protein_g],
    ['g carbs', structured!.carbs_g],
    ['g fat', structured!.fat_g],
  ] as const).filter(([, v]) => v != null).map(([unit, v]) => `${v}${unit === 'kcal' ? ' ' : ''}${unit}`);

  // Group ingredients by their optional section label, preserving first-seen order;
  // recipes with no sections fall into a single unlabeled group.
  // sortOrder rides along because the grouping below loses the line's position in the
  // recipe, and that position is the key a swap is filed under.
  // Hiding what the kitchen already holds turns the list into "what I still need" — the
  // question it is usually read for (BACKLOG 12.6). Per-reading, so nothing is persisted.
  const heldCount = structured!.ingredients.filter(i => i.in_pantry).length;
  const sections = new Map<string, Array<{ ing: Ingredient; sortOrder: number }>>();
  structured!.ingredients.forEach((ing, i) => {
    if (hideInPantry && ing.in_pantry) return;
    const key = ing.section ?? '';
    if (!sections.has(key)) sections.set(key, []);
    sections.get(key)!.push({ ing, sortOrder: i + 1 });
  });

  return (
    <View>
      {meta.length > 0 && (
        <View style={styles.metaRow}>
          {meta.map((m, i) => (
            <Badge
              key={i}
              tone="neutral"
              label={m.text}
              icon={<Ionicons name={m.icon} size={13} color={theme.accent} />}
            />
          ))}
        </View>
      )}

      {structured!.summary != null && structured!.summary !== '' && (
        <Text style={styles.summary}>{structured!.summary}</Text>
      )}

      {macros.length > 0 && (
        <View style={styles.macroBlock}>
          <View style={styles.metaRow}>
            {macros.map(m => (
              <Badge key={m} tone="neutral" label={m} />
            ))}
          </View>
          <Text style={styles.macroNote}>{t('recipeView.perServing')}</Text>
        </View>
      )}

      {/* The scaler sits above the ingredients because that is the only thing it moves.
          Hidden when the recipe never said what it yields -- there is nothing to scale
          from, and a stepper starting at a guess would silently invent amounts. */}
      {scalable && structured!.ingredients.length > 0 && baseServings != null && (
        <View style={styles.section}>
          <View style={styles.servingsRow}>
            <Text style={styles.sectionLabel}>{t('recipeView.cookingFor')}</Text>
            <View style={styles.stepper}>
              <TouchableOpacity
                style={styles.stepperBtn}
                onPress={() => setWanted((wanted ?? baseServings) - 1)}
                accessibilityRole="button"
                accessibilityLabel={t('recipeView.oneFewerServing')}
              >
                <Ionicons name="remove" size={16} color={theme.accent} />
              </TouchableOpacity>
              <Text style={styles.servingsValue}>{wanted}</Text>
              <TouchableOpacity
                style={styles.stepperBtn}
                onPress={() => setWanted((wanted ?? baseServings) + 1)}
                accessibilityRole="button"
                accessibilityLabel={t('recipeView.oneMoreServing')}
              >
                <Ionicons name="add" size={16} color={theme.accent} />
              </TouchableOpacity>
            </View>
          </View>
          {scale !== 1 && (
            <Text style={styles.scaledNote}>{t('recipeView.scaledNote', { base: baseServings })}</Text>
          )}
        </View>
      )}

      {structured!.ingredients.length > 0 && (
        <View style={styles.section}>
          <Text style={styles.sectionLabel}>{t('edit.ingredients')}</Text>
          {/* BACKLOG 16.7: only when the kitchen holds something, so an empty pantry
              shows no legend and no marks. */}
          {heldCount > 0 && (
            // The legend was already here; making it the control costs no new chrome.
            <TouchableOpacity
              style={styles.pantryLegend}
              onPress={() => setHideInPantry(v => !v)}
              accessibilityRole="button"
              accessibilityState={{ checked: hideInPantry }}
            >
              <Ionicons name="checkmark-circle" size={13} color={theme.success} />
              <Text style={styles.optLabel}>{t('recipes.pantryLegend')}</Text>
              <Text style={styles.pantryToggle}>
                {t(hideInPantry ? 'recipes.showInPantry' : 'recipes.hideInPantry', { count: heldCount })}
              </Text>
            </TouchableOpacity>
          )}
          {[...sections.entries()].map(([section, items]) => (
            <View key={section || '__default'}>
              {section !== '' && <Text style={styles.subsectionLabel}>{section}</Text>}
              {items.map(({ ing, sortOrder }, idx) => {
                // A line is tappable only when there is something to tap for: a swap
                // already in place to undo, or something in the kitchen to swap to.
                const swappable = !!onSwap && (!!ing.swapped_from || (ing.swap_options?.length ?? 0) > 0);
                const line = (
                  <>
                    {ing.in_pantry ? (
                      <Ionicons
                        name="checkmark-circle"
                        size={14}
                        color={theme.success}
                        style={styles.ingCheck}
                        accessibilityLabel={t('recipes.pantryLegend')}
                      />
                    ) : (
                      <View style={styles.ingBullet} />
                    )}
                    <Text style={styles.ingText}>
                      {fmtIngredient(scaleIngredient(ing, scale), scale !== 1)}
                      {ing.optional ? <Text style={styles.optLabel}>  optional</Text> : null}
                      {ing.swapped_from ? (
                        <Text style={styles.optLabel}>  {t('recipes.swappedFor', { item: ing.swapped_from })}</Text>
                      ) : null}
                    </Text>
                    {swappable && <Ionicons name="swap-horizontal" size={14} color={theme.subtext} />}
                  </>
                );
                if (!swappable) return <View key={idx} style={styles.ingRow}>{line}</View>;
                return (
                  <TouchableOpacity
                    key={idx}
                    style={styles.ingRow}
                    accessibilityRole="button"
                    accessibilityLabel={t('recipes.swapThis')}
                    onPress={() => setSwapping({ sortOrder, ing })}
                  >
                    {line}
                  </TouchableOpacity>
                );
              })}
            </View>
          ))}
        </View>
      )}

      {/* One sheet for the whole list: the options are the kitchen's, and "as written"
          is always there because undoing a swap must be as easy as making one. */}
      <Sheet
        visible={swapping !== null}
        onClose={() => setSwapping(null)}
        title={swapping ? t('recipes.swapTitle', { item: swapping.ing.swapped_from ?? swapping.ing.item }) : undefined}
      >
        {(swapping?.ing.swap_options ?? []).map(option => (
          <SheetRow
            key={option}
            label={option}
            onPress={() => {
              onSwap?.(swapping!.sortOrder, option);
              setSwapping(null);
            }}
          />
        ))}
        {swapping?.ing.swapped_from ? (
          <SheetRow
            label={t('recipes.swapAsWritten', { item: swapping.ing.swapped_from })}
            onPress={() => {
              onSwap?.(swapping.sortOrder, '');
              setSwapping(null);
            }}
          />
        ) : null}
      </Sheet>

      {showSteps && structured!.steps.length > 0 && (
        <View style={styles.section}>
          <Text style={styles.sectionLabel}>{t('edit.steps')}</Text>
          {structured!.steps.map((s, idx) => (
            <View key={idx} style={styles.stepCard}>
              <View style={styles.stepCardMain}>
                <View style={styles.stepNum}><Text style={styles.stepNumText}>{s.sort_order}</Text></View>
                <Text style={styles.stepText}>{s.step_text}</Text>
              </View>
              {(s.timer_seconds != null || s.temperature_c != null) && (
                <View style={styles.badgeRow}>
                  {s.timer_seconds != null && (
                    <Badge
                      label={`${Math.round(s.timer_seconds / 60)} min`}
                      icon={<Ionicons name="timer-outline" size={12} color={theme.accent} />}
                    />
                  )}
                  {s.temperature_c != null && (
                    <Badge
                      label={`${s.temperature_c}°C`}
                      icon={<Ionicons name="thermometer-outline" size={12} color={theme.accent} />}
                    />
                  )}
                </View>
              )}
            </View>
          ))}
        </View>
      )}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  metaRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, marginBottom: 10 },

  summary: { ...type.body, fontSize: 14, color: t.subtext, lineHeight: 20, marginBottom: 12, fontStyle: 'italic' },

  macroBlock: { marginBottom: 12 },
  macroNote: { ...type.caption, color: t.muted, marginTop: -4 },

  section: { marginBottom: 14 },
  sectionLabel: { ...type.label, fontSize: 12, lineHeight: 16, color: t.muted, textTransform: 'uppercase', letterSpacing: 0.8, marginBottom: 8 },
  subsectionLabel: { ...type.label, fontSize: 12, lineHeight: 16, color: t.accent, marginTop: 8, marginBottom: 4 },

  ingRow: { flexDirection: 'row', alignItems: 'flex-start', marginBottom: 6, gap: 8 },
  servingsRow: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  servingsValue: { ...type.title, fontSize: 17, lineHeight: 24, color: t.text, minWidth: 24, textAlign: 'center' },
  stepper: { flexDirection: 'row', alignItems: 'center', gap: space.md },
  stepperBtn: { padding: space.sm, borderRadius: radius.full, borderWidth: 1, borderColor: t.border },
  // Says plainly what the multiplier did NOT touch. Scaling seasoning, cook times and pan
  // sizes linearly is wrong, and a 4x recipe that silently quadrupled the chili is worse
  // than one that admits it only moved the amounts.
  scaledNote: { ...type.caption, color: t.muted, marginTop: 8 },
  ingBullet: { width: 5, height: 5, borderRadius: 2.5, backgroundColor: t.accent, marginTop: 7 },
  ingCheck: { marginTop: 3 },
  pantryLegend: { flexDirection: 'row', alignItems: 'center', gap: 4, marginBottom: 6 },
  pantryToggle: { ...type.caption, color: t.accent },
  ingText: { flex: 1, ...type.body, fontSize: 14, color: t.text, lineHeight: 20 },
  optLabel: { ...type.caption, color: t.muted, fontStyle: 'italic' },

  stepCard: { backgroundColor: t.surfaceRaised, borderRadius: 10, padding: 12, marginBottom: 8, borderWidth: 1, borderColor: t.border },
  stepCardMain: { flexDirection: 'row', alignItems: 'flex-start', gap: 10 },
  stepNum: { width: 22, height: 22, borderRadius: 11, backgroundColor: t.accentFaded, justifyContent: 'center', alignItems: 'center' },
  stepNumText: { ...type.label, fontSize: 12, lineHeight: 16, color: t.accent },
  stepText: { flex: 1, ...type.body, fontSize: 14, color: t.text, lineHeight: 20 },
  badgeRow: { flexDirection: 'row', gap: 6, marginTop: 8, marginLeft: 32 },
});

export default RecipeView;
