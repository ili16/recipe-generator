import React, { useMemo } from 'react';
import { View, Text, StyleSheet, ScrollView, TouchableOpacity, ActivityIndicator } from 'react-native';
import { RecipeResponse } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import RecipeView from '../../components/RecipeView';
import { makeChromeStyles } from './styles';

interface Props {
  refined: RecipeResponse;
  saving: boolean;
  onBack: () => void;
  onSave: () => void;
  onDiscard: () => void;
}

const RefinedPhase: React.FC<Props> = ({ refined, saving, onBack, onSave, onDiscard }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const c = useMemo(() => makeChromeStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);

  return (
    <View style={c.container}>
      <View style={c.header}>
        <TouchableOpacity onPress={onBack} style={c.headerSide}>
          <Text style={c.headerAction}>← Back</Text>
        </TouchableOpacity>
        <Text style={c.headerTitle} numberOfLines={1}>{t('cooking.refinedRecipe')}</Text>
        <View style={c.headerSide} />
      </View>

      <ScrollView style={c.scrollView} contentContainerStyle={c.scrollContent}>
        <Text style={styles.refinedTitle}>{refined.recipename}</Text>
        <RecipeView structured={refined.structured} markdown={refined.recipe} />
      </ScrollView>

      <View style={c.footer}>
        <TouchableOpacity style={[c.primaryBtn, saving && c.btnDisabled]} onPress={onSave} disabled={saving}>
          {saving ? (
            <ActivityIndicator color={theme.onAccent} size="small" />
          ) : (
            <Text style={c.primaryBtnText}>{t('chat.saveToRecipes')}</Text>
          )}
        </TouchableOpacity>
        <TouchableOpacity style={c.secondaryBtn} onPress={onDiscard}>
          <Text style={c.secondaryBtnText}>{t('recipes.discard')}</Text>
        </TouchableOpacity>
      </View>
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  refinedTitle: {
    ...type.title, fontSize: 22,
    color: t.text,
    marginBottom: 16,
  },
});

export default RefinedPhase;
