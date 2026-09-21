import React, { useMemo, useState } from 'react';
import { View, Text, TextInput, StyleSheet, ScrollView, TouchableOpacity } from 'react-native';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import { TAGS_BY_GROUP, tagGroupLabelKey, tagLabelKey, TagGroup } from '../../constants/tags';
import { makeSharedStyles } from './styles';
import { Chip } from '../../components/ui';
import { Collection } from '../../types';
import { QuickFilter, QUICK_FILTER_LABEL_KEYS } from './quickFilters';

export type SortMode = 'recent' | 'name' | 'pantry';

interface Props {
  search: string;
  onSearch: (value: string) => void;
  selectedTags: Set<string>;
  onToggleTag: (slug: string) => void;
  sortMode: SortMode;
  onToggleSort: () => void;
  trashMode: boolean;
  onToggleTrash: () => void;
  collections: Collection[];
  activeCollectionId: number | null;
  onSelectCollection: (id: number | null) => void;
  onDeleteCollection: (collection: Collection) => void;
  quickFilters: Set<QuickFilter>;
  onToggleQuickFilter: (filter: QuickFilter) => void;
  /** Export everything currently listed as one Markdown file — BACKLOG 13.1. */
  onExport: () => void;
}

const RecipeToolbar: React.FC<Props> = ({ search, onSearch, selectedTags, onToggleTag, sortMode, onToggleSort, trashMode, onToggleTrash,
  collections, activeCollectionId, onSelectCollection, onDeleteCollection, quickFilters, onToggleQuickFilter, onExport }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const s = useMemo(() => makeSharedStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [showFilters, setShowFilters] = useState(false);

  return (
    <View style={styles.toolbar}>
      <View style={styles.searchRow}>
        {/* Search, tags and sort are library-only — the trash is a short flat list. */}
        {!trashMode && (
          <>
            <TextInput autoComplete="off"
              style={styles.searchInput}
              placeholder={t('recipes.searchPlaceholder')}
              placeholderTextColor={theme.muted}
              value={search}
              onChangeText={onSearch}
            />
            <TouchableOpacity
              style={[styles.filterToggle, (showFilters || selectedTags.size > 0) && styles.filterToggleActive]}
              onPress={() => setShowFilters(v => !v)}
            >
              <Text style={[styles.filterToggleText, (showFilters || selectedTags.size > 0) && styles.filterToggleTextActive]}>
                {t('recipes.tags')}{selectedTags.size > 0 ? ` (${selectedTags.size})` : ''}
              </Text>
            </TouchableOpacity>
            <TouchableOpacity style={styles.sortToggle} onPress={onToggleSort}>
              <Text style={styles.sortToggleText}>{t(sortMode === 'recent' ? 'recipes.sortRecent' : sortMode === 'name' ? 'recipes.sortAlpha' : 'recipes.sortPantry')}</Text>
            </TouchableOpacity>
            {/* Exports what is on screen, not the whole table: the filters above are
                how you choose what to take with you (BACKLOG 13.1). */}
            <TouchableOpacity style={styles.sortToggle} onPress={onExport}>
              <Text style={styles.sortToggleText}>{t('recipes.exportAll')}</Text>
            </TouchableOpacity>
          </>
        )}
        <TouchableOpacity
          style={[styles.sortToggle, trashMode && styles.filterToggleActive, trashMode && styles.trashToggleWide]}
          onPress={onToggleTrash}
        >
          <Text style={[styles.sortToggleText, trashMode && styles.filterToggleTextActive]}>
            {trashMode ? t('recipes.backToRecipes') : t('recipes.trash')}
          </Text>
        </TouchableOpacity>
      </View>

      {/* The three cheap filters (BACKLOG 6.5): they read fields the list already carries,
          and unlike collections they combine. */}
      {!trashMode && (
        <View style={[s.tagRow, styles.quickRow]}>
          {(Object.keys(QUICK_FILTER_LABEL_KEYS) as QuickFilter[]).map(f => (
            <Chip
              key={f}
              label={t(QUICK_FILTER_LABEL_KEYS[f])}
              selected={quickFilters.has(f)}
              onPress={() => onToggleQuickFilter(f)}
            />
          ))}
        </View>
      )}

      {/* Collections are a one-at-a-time filter, unlike tags: "which of these are mine
          for Sunday" is a single answer. Long-press a chip to delete the collection. */}
      {!trashMode && collections.length > 0 && (
        <ScrollView horizontal showsHorizontalScrollIndicator={false} style={styles.collectionRow}>
          <View style={s.tagRow}>
            <Chip label={t('recipes.allCollections')} selected={activeCollectionId === null} onPress={() => onSelectCollection(null)} />
            {collections.map(c => (
              <Chip
                key={c.id}
                label={`${c.name} (${c.recipe_ids.length})`}
                selected={activeCollectionId === c.id}
                onPress={() => onSelectCollection(activeCollectionId === c.id ? null : c.id)}
                onLongPress={() => onDeleteCollection(c)}
              />
            ))}
          </View>
        </ScrollView>
      )}

      {!trashMode && showFilters && (
        <ScrollView style={styles.filterPanel} nestedScrollEnabled>
          {(Object.keys(TAGS_BY_GROUP) as TagGroup[]).map(group => (
            <View key={group} style={s.filterGroup}>
              <Text style={s.filterGroupLabel}>{t(tagGroupLabelKey(group))}</Text>
              <View style={s.tagRow}>
                {TAGS_BY_GROUP[group].map(tag => (
                  <Chip
                    key={tag.slug}
                    label={t(tagLabelKey(tag.slug))}
                    selected={selectedTags.has(tag.slug)}
                    onPress={() => onToggleTag(tag.slug)}
                  />
                ))}
              </View>
            </View>
          ))}
        </ScrollView>
      )}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  toolbar: {
    paddingHorizontal: 15,
    paddingTop: 12,
  },
  searchRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  searchInput: {
    flex: 1,
    backgroundColor: t.surface,
    borderWidth: 1,
    borderColor: t.border,
    borderRadius: 10,
    paddingHorizontal: 14,
    paddingVertical: 10,
    ...type.body, fontSize: 14,
    color: t.text,
  },
  filterToggle: {
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: t.border,
    backgroundColor: t.surface,
  },
  filterToggleActive: {
    borderColor: t.accent,
    backgroundColor: t.accentFaded,
  },
  filterToggleText: {
    ...type.label, fontSize: 13,
    color: t.subtext,
  },
  filterToggleTextActive: {
    color: t.accent,
  },
  sortToggle: {
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: t.border,
    backgroundColor: t.surface,
  },
  collectionRow: {
    marginTop: 10,
  },
  quickRow: {
    marginTop: 10,
  },
  trashToggleWide: {
    flex: 1,
  },
  sortToggleText: {
    ...type.body, fontSize: 13,
    color: t.subtext,
  },
  filterPanel: {
    marginTop: 10,
    maxHeight: 220,
    backgroundColor: t.surface,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: t.border,
    padding: 12,
  },
});

export default RecipeToolbar;
