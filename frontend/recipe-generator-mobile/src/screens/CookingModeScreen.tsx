import React, { useMemo } from 'react';
import { View, Text, ActivityIndicator } from 'react-native';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import { useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { useCookingSession } from '../hooks/useCookingSession';
import { makeChromeStyles } from './cooking/styles';
import OverviewPhase from './cooking/OverviewPhase';
import StepPhase from './cooking/StepPhase';
import { servingScale } from '../utils/recipeIngredient';
import DonePhase from './cooking/DonePhase';
import RefinedPhase from './cooking/RefinedPhase';

type Props = NativeStackScreenProps<RootStackParamList, 'CookingMode'>;

const CookingModeScreen: React.FC<Props> = ({ navigation, route }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const c = useMemo(() => makeChromeStyles(theme), [theme]);
  const s = useCookingSession(route.params.recipe, () => navigation.navigate('Recipes'));

  if (s.phase === 'loading' || s.phase === 'refining') {
    const refining = s.phase === 'refining';
    const flow = s.refineOrigin.current === 'overview';
    return (
      <View style={[c.container, c.centered]}>
        <ActivityIndicator size="large" color={theme.accent} />
        <Text style={c.loadingText}>
          {t(!refining ? 'cooking.loadingRecipe' : flow ? 'cooking.planningFlow' : 'cooking.refining')}
        </Text>
        {refining && (
          <Text style={c.loadingSubtext}>
            {flow
              ? t('cooking.planningFlowHint')
              : t('cooking.refiningHint')}
          </Text>
        )}
      </View>
    );
  }

  if (s.phase === 'refined' && s.refinedRecipe) {
    return (
      <RefinedPhase
        refined={s.refinedRecipe}
        saving={s.saving}
        onBack={() => s.setPhase(s.refineOrigin.current)}
        onSave={s.saveRefined}
        onDiscard={() => navigation.navigate('Recipes')}
      />
    );
  }

  if (s.phase === 'done') {
    return (
      <DonePhase
        recipeId={s.recipe.id}
        recipeName={s.recipe.recipename}
        steps={s.steps}
        notes={s.notes}
        onClose={() => navigation.goBack()}
        onRefine={s.refine}
      />
    );
  }

  if (s.phase === 'overview') {
    return (
      <OverviewPhase
        recipe={s.recipe}
        structured={s.structured}
        groups={s.groups}
        onPlanFlow={s.planFlow}
        servings={s.servings}
        onServingsChange={s.setServings}
        onClose={() => navigation.goBack()}
        onStart={s.startCooking}
      />
    );
  }

  return (
    <StepPhase
      recipe={s.recipe}
      groups={s.groups}
      ingredients={s.ingredients}
      scale={servingScale(s.servings, s.structured?.servings)}
      currentGroup={s.currentGroup}
      noteIndex={s.noteIndex}
      notes={s.notes}
      checked={s.checked}
      onToggleChecked={s.toggleChecked}
      timers={s.timers}
      now={s.now}
      onToggleTimer={s.toggleTimer}
      onSaveNote={s.setNote}
      onClose={() => s.setPhase('overview')}
      onPrev={s.prev}
      onNext={s.next}
      onAsk={s.ask}
    />
  );
};

export default CookingModeScreen;
