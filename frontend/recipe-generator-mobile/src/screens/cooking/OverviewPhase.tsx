import React, { useMemo } from 'react';
import { View, Text, StyleSheet, ScrollView, TouchableOpacity } from 'react-native';
import { Recipe, RecipeDocument } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import RecipeView from '../../components/RecipeView';
import { phaseLabelKey, StepGroup, formatTimer, phaseSummary } from './steps';
import { makeChromeStyles } from './styles';

interface Props {
  recipe: Recipe;
  structured?: RecipeDocument | null;
  groups: StepGroup[];
  onPlanFlow: () => void;
  onClose: () => void;
  onStart: () => void;
  // The session owns the scaler so the step cards agree with this list (BACKLOG 17.3).
  servings?: number | null;
  onServingsChange?: (servings: number) => void;
}

const OverviewPhase: React.FC<Props> = ({ recipe, structured, groups, onPlanFlow, onClose, onStart, servings, onServingsChange }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const c = useMemo(() => makeChromeStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);
  // Empty for every recipe saved before the cook flow existed — which is the cue to offer
  // planning one rather than to render a blank summary.
  const phases = useMemo(() => phaseSummary(groups), [groups]);

  return (
    <View style={c.container}>
      <View style={c.header}>
        <TouchableOpacity onPress={onClose} style={c.headerSide}>
          <Text style={c.headerAction}>✕</Text>
        </TouchableOpacity>
        <Text style={c.headerTitle} numberOfLines={1}>{recipe.recipename}</Text>
        <View style={c.headerSide} />
      </View>

      <ScrollView style={c.scrollView} contentContainerStyle={c.scrollContent}>
        <RecipeView
          structured={structured}
          markdown={recipe.recipe}
          showSteps={false}
          scalable
          servings={servings}
          onServingsChange={onServingsChange}
        />

        {groups.length > 0 && (
          phases.length > 0 ? (
            <View style={styles.phaseList}>
              {phases.map(p => (
                <View key={p.phase} style={styles.phaseRow}>
                  <Text style={styles.phaseName}>{t(phaseLabelKey(p.phase))}</Text>
                  <Text style={styles.phaseDetail}>
                    {t('cooking.stepCount', { count: p.groups })}
                    {p.seconds > 0 ? ` · ${formatTimer(p.seconds)}` : ''}
                  </Text>
                </View>
              ))}
            </View>
          ) : (
            // No phases: a recipe saved before the cook flow existed. Offer to plan one
            // rather than silently walking a flat list of whatever steps it happens to have.
            <View style={styles.noFlow}>
              <Text style={styles.stepsCount}>{t('cooking.stepsToCook', { count: groups.length })}</Text>
              <TouchableOpacity style={styles.planFlowButton} onPress={onPlanFlow}>
                <Text style={styles.planFlowText}>{t('cooking.planFlow')}</Text>
              </TouchableOpacity>
              <Text style={styles.planFlowHint}>
                {t('cooking.planFlowHint')}
              </Text>
            </View>
          )
        )}
      </ScrollView>

      {/* No structured steps means there is nothing to walk — the recipe text above is all
          there is, so don't offer a step-by-step run that would open on an empty screen. */}
      {groups.length > 0 && (
        <View style={c.footer}>
          <TouchableOpacity style={styles.startButton} onPress={onStart}>
            <Text style={styles.startButtonText}>{t('cooking.startWithSteps', { count: groups.length })}</Text>
          </TouchableOpacity>
        </View>
      )}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  stepsCount: {
    marginTop: 24,
    ...type.body, fontSize: 14,
    color: t.muted,
    textAlign: 'center',
  },
  noFlow: {
    alignItems: 'center',
  },
  planFlowButton: {
    marginTop: 14,
    paddingVertical: 12,
    paddingHorizontal: 20,
    borderRadius: 14,
    borderWidth: 1.5,
    borderColor: t.border,
  },
  planFlowText: {
    ...type.label, fontSize: 15,
    color: t.text,
  },
  planFlowHint: {
    marginTop: 10,
    ...type.body, fontSize: 12,
    color: t.muted,
    textAlign: 'center',
    maxWidth: 320,
  },
  phaseList: {
    marginTop: 24,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
  },
  phaseRow: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingVertical: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: t.border,
  },
  phaseName: {
    ...type.label, fontSize: 13,
    color: t.accent,
    letterSpacing: 1.2,
    textTransform: 'uppercase',
  },
  phaseDetail: {
    ...type.body, fontSize: 14,
    color: t.muted,
  },
  startButton: {
    backgroundColor: t.accent,
    borderRadius: 14,
    paddingVertical: 18,
    alignItems: 'center',
  },
  startButtonText: {
    color: t.onAccent,
    ...type.label, fontSize: 17,
  },
});

export default OverviewPhase;
