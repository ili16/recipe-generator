import React, { useMemo, useState } from 'react';
import { View, Text, TextInput, StyleSheet, ScrollView, TouchableOpacity } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { Collection, RecipeDocument, RecipeVersion } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import { summarizeVersionChange } from '../../utils/versionSummary';
import { makeSharedStyles } from './styles';
import { Chip } from '../../components/ui';

// The inline panels an expanded recipe card can show below the recipe:
// refine-with-AI, create-variant, edit history, and collection membership.

export interface VariantPreview {
  recipename: string;
  recipe: string;
  structured: RecipeDocument;
  variantOfRecipeId: number;
}

export const RefinePanel: React.FC<{ loading: boolean; onApply: (prompt: string) => void }> = ({ loading, onApply }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const s = useMemo(() => makeSharedStyles(theme), [theme]);
  const [prompt, setPrompt] = useState('');

  return (
    <View style={s.refineBox}>
      <TextInput autoComplete="off"
        style={[s.fieldInput, s.fieldInputMultiline]}
        placeholder={t('recipes.refinePlaceholder')}
        placeholderTextColor={theme.muted}
        value={prompt}
        onChangeText={setPrompt}
        editable={!loading}
        multiline
      />
      <TouchableOpacity
        style={[s.cookButton, (!prompt.trim() || loading) && s.btnDisabled]}
        onPress={() => onApply(prompt.trim())}
        disabled={!prompt.trim() || loading}
      >
        <Text style={s.cookButtonText}>{loading ? t('recipes.thinking') : t('recipes.apply')}</Text>
      </TouchableOpacity>
    </View>
  );
};

export const VariantPanel: React.FC<{
  loading: boolean;
  preview: VariantPreview | null;
  onGenerate: (hint: string) => void;
  onDiscard: () => void;
  onAccept: () => void;
}> = ({ loading, preview, onGenerate, onDiscard, onAccept }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const s = useMemo(() => makeSharedStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [hint, setHint] = useState('');

  return (
    <View style={s.refineBox}>
      {preview ? (
        <View style={styles.variantPreviewBox}>
          <Text style={styles.variantPreviewTitle}>{preview.recipename}</Text>
          {preview.structured.summary ? (
            <Text style={styles.variantPreviewSummary}>{preview.structured.summary}</Text>
          ) : null}
          <View style={styles.variantActionsRow}>
            <TouchableOpacity style={styles.variantDiscardButton} onPress={onDiscard}>
              <Text style={styles.variantDiscardButtonText}>{t('recipes.discard')}</Text>
            </TouchableOpacity>
            <TouchableOpacity style={styles.variantAcceptButton} onPress={onAccept}>
              <Text style={styles.variantAcceptButtonText}>{t('recipes.saveAsNew')}</Text>
            </TouchableOpacity>
          </View>
        </View>
      ) : (
        <>
          <TextInput autoComplete="off"
            style={[s.fieldInput, s.fieldInputMultiline]}
            placeholder={t('recipes.variantPlaceholder')}
            placeholderTextColor={theme.muted}
            value={hint}
            onChangeText={setHint}
            editable={!loading}
            multiline
          />
          <TouchableOpacity
            style={[s.cookButton, loading && s.btnDisabled]}
            onPress={() => onGenerate(hint.trim())}
            disabled={loading}
          >
            <Text style={s.cookButtonText}>{loading ? t('recipes.thinking') : t('recipes.generate')}</Text>
          </TouchableOpacity>
        </>
      )}
    </View>
  );
};

const CHANGE_KIND_ICON: Record<RecipeVersion['change_kind'], React.ComponentProps<typeof Ionicons>['name']> = {
  extraction: 'add-circle-outline',
  import: 'add-circle-outline',
  manual: 'create-outline',
  ai_edit: 'sparkles-outline',
};

const CHANGE_KIND_KEY: Record<RecipeVersion['change_kind'], string> = {
  extraction: 'history.kind.extraction',
  import: 'history.kind.import',
  manual: 'history.kind.manual',
  ai_edit: 'history.kind.ai_edit',
};

export const HistoryPanel: React.FC<{ loading: boolean; entries: RecipeVersion[] }> = ({ loading, entries }) => {
  const { theme } = useTheme();
  const { t, locale } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  return (
    <View style={styles.historyBox}>
      {loading ? (
        <Text style={styles.historyEmptyText}>{t('history.loading')}</Text>
      ) : entries.length === 0 ? (
        <Text style={styles.historyEmptyText}>{t('history.empty')}</Text>
      ) : (
        <ScrollView style={styles.historyScroll} nestedScrollEnabled>
          {entries.map((entry, idx) => (
            <View key={entry.version} style={styles.historyEntry}>
              <View style={styles.historyEntryHeader}>
                <Ionicons name={CHANGE_KIND_ICON[entry.change_kind]} size={13} color={theme.accent} style={{ marginRight: 6 }} />
                <Text style={styles.historyKind}>{t(CHANGE_KIND_KEY[entry.change_kind])}</Text>
                <Text style={styles.historyDate}>{new Date(entry.created_at).toLocaleString(locale)}</Text>
              </View>
              <Text style={styles.historySummary}>
                {summarizeVersionChange(entry.data, entries[idx + 1]?.data, t)}
              </Text>
              {entry.change_note ? (
                <Text style={styles.historyNote}>“{entry.change_note}”</Text>
              ) : null}
            </View>
          ))}
        </ScrollView>
      )}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  variantPreviewBox: {
    padding: 10,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: t.border,
    backgroundColor: t.surface,
    gap: 6,
  },
  variantPreviewTitle: {
    ...type.label,
    color: t.text,
  },
  variantPreviewSummary: {
    ...type.body, fontSize: 13,
    color: t.subtext,
    lineHeight: 18,
  },
  variantActionsRow: {
    flexDirection: 'row',
    gap: 10,
    marginTop: 4,
  },
  variantAcceptButton: {
    flex: 1,
    alignItems: 'center',
    paddingVertical: 10,
    borderRadius: 8,
    backgroundColor: t.accent,
  },
  variantAcceptButtonText: {
    color: t.onAccent,
    ...type.label, fontSize: 13,
  },
  variantDiscardButton: {
    flex: 1,
    alignItems: 'center',
    paddingVertical: 10,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: t.border,
  },
  variantDiscardButtonText: {
    color: t.subtext,
    ...type.label, fontSize: 13,
  },
  historyBox: {
    marginBottom: 15,
  },
  historyEmptyText: {
    ...type.body, fontSize: 13,
    color: t.muted,
    fontStyle: 'italic',
  },
  historyScroll: {
    maxHeight: 260,
  },
  historyEntry: {
    paddingVertical: 10,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
  },
  historyEntryHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    marginBottom: 3,
  },
  historyKind: {
    ...type.label, fontSize: 12,
    color: t.text,
    flex: 1,
  },
  historyDate: {
    ...type.caption, fontSize: 11,
    color: t.muted,
  },
  historySummary: {
    ...type.body, fontSize: 13,
    color: t.subtext,
  },
  historyNote: {
    ...type.caption,
    color: t.muted,
    fontStyle: 'italic',
    marginTop: 3,
  },
});

/**
 * Collection membership for one recipe (BACKLOG 8.1): every collection as a chip,
 * selected meaning "this recipe is in it", plus one field to name a new one. Tapping a
 * chip is the whole add/remove interaction — there is no separate confirm.
 */
export const CollectionsPanel: React.FC<{
  collections: Collection[];
  recipeId: number;
  onToggle: (collectionId: number, member: boolean) => void;
  onCreate: (name: string) => Promise<Collection | null>;
}> = ({ collections, recipeId, onToggle, onCreate }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const s = useMemo(() => makeSharedStyles(theme), [theme]);
  const [name, setName] = useState('');
  const [creating, setCreating] = useState(false);

  const create = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setCreating(true);
    const created = await onCreate(trimmed);
    setCreating(false);
    if (created) {
      setName('');
      // A brand-new collection is only useful if the recipe lands in it.
      if (!created.recipe_ids.includes(recipeId)) onToggle(created.id, true);
    }
  };

  return (
    <View style={s.refineBox}>
      {collections.length === 0 ? (
        <Text style={s.fieldLabel}>{t('recipes.noCollections')}</Text>
      ) : (
        <View style={s.tagRow}>
          {collections.map(c => (
            <Chip
              key={c.id}
              label={c.name}
              selected={c.recipe_ids.includes(recipeId)}
              onPress={() => onToggle(c.id, !c.recipe_ids.includes(recipeId))}
            />
          ))}
        </View>
      )}
      <View style={s.fieldRow}>
        <TextInput autoComplete="off"
          style={[s.fieldInput, s.fieldCol]}
          placeholder={t('recipes.newCollectionName')}
          placeholderTextColor={theme.muted}
          value={name}
          onChangeText={setName}
          editable={!creating}
          onSubmitEditing={create}
        />
        <TouchableOpacity
          style={[s.cookButton, (!name.trim() || creating) && s.btnDisabled]}
          onPress={create}
          disabled={!name.trim() || creating}
        >
          <Text style={s.cookButtonText}>{t('common.add')}</Text>
        </TouchableOpacity>
      </View>
    </View>
  );
};
