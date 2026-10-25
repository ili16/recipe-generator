import React, { useEffect, useState } from 'react';
import { ScrollView, View, StyleSheet } from 'react-native';
import apiService from '../services/apiService';
import { Recipe } from '../types';
import RecipeView from '../components/RecipeView';
import Loading from '../components/Loading';
import { Text as UIText } from '../components/ui';
import { useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';

// The whole of a share link's destination (BACKLOG 8.4): one recipe, read-only, no auth,
// no nav chrome. Rendered by App.tsx instead of the app shell when the URL is /s/<token>,
// so no navigator or deep-link config has to know about it.
const SharedRecipeScreen: React.FC<{ token: string }> = ({ token }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const [recipe, setRecipe] = useState<Recipe | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    apiService.getSharedRecipe(token).then(setRecipe).catch(() => setError(true));
  }, [token]);

  if (error) {
    return (
      <View style={[styles.center, { backgroundColor: theme.bg }]}>
        <UIText variant="title">{t('shared.linkUnavailable')}</UIText>
        <UIText tone="muted" style={styles.sub}>
          {t('shared.recipeGone')}
        </UIText>
      </View>
    );
  }

  if (!recipe) return <Loading visible message={t('shared.loadingRecipe')} />;

  return (
    <ScrollView style={{ backgroundColor: theme.bg }} contentContainerStyle={styles.content}>
      <UIText variant="title">{recipe.recipename}</UIText>
      <RecipeView structured={recipe.structured} markdown={recipe.recipe} scalable />
      <UIText variant="caption" tone="muted" style={styles.sub}>
        {t('shared.fromApp')}
      </UIText>
    </ScrollView>
  );
};

const styles = StyleSheet.create({
  center: { flex: 1, alignItems: 'center', justifyContent: 'center', padding: 24 },
  content: { padding: 20, paddingBottom: 48, maxWidth: 760, width: '100%', alignSelf: 'center' },
  sub: { marginTop: 12, textAlign: 'center' },
});

export default SharedRecipeScreen;
