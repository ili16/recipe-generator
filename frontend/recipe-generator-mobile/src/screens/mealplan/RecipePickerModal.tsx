import React, { useMemo, useState, useEffect } from 'react';
import { View, Text, StyleSheet, TouchableOpacity, Modal, FlatList, TextInput } from 'react-native';
import { Recipe, MealSlot, MEAL_SLOTS } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { pickerRows } from './pickerRows';
import { Chip } from '../../components/ui';
import { slotLabelKey } from './DayCard';
import { space } from '../../theme';
import { type } from '../../theme';

interface Props {
  visible: boolean;
  recipes: Recipe[];
  /** The slot being filled, when one is known — titles the picker and orders the list. */
  slot?: MealSlot;
  /** What each slot of the day already holds, so a chip can say what picking it would cost. */
  takenSlots?: Partial<Record<MealSlot, string>>;
  onSelect: (recipe: Recipe, slot: MealSlot) => void;
  onClose: () => void;
}

/**
 * Shared "choose a saved recipe" picker: the week view's empty day, every row's Swap, and
 * "Add a meal".
 *
 * The slot is known at the moment the picker opens, so it is used: recipes tagged for that
 * meal lead, the rest sit under "Other recipes" (BACKLOG 9.9). Ranked, never filtered —
 * hiding would be wrong, people eat soup for breakfast.
 *
 * When it is *not* known — "Add a meal" on a day that already has one — the slot is a row of
 * chips here rather than a sheet of its own in front of this modal (BACKLOG 12.5). One modal
 * instead of two, and the ranking updates as you tap. Picking a taken slot still goes through
 * `WeekView.assign`'s replace confirmation, so nothing is destroyed silently.
 */
const RecipePickerModal: React.FC<Props> = ({ visible, recipes, slot, takenSlots, onSelect, onClose }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [query, setQuery] = useState('');
  // Only used when the caller did not name a slot. Dinner is the default because it is what
  // the empty-day card already assumes.
  const [chosen, setChosen] = useState<MealSlot>('dinner');
  const effective = slot ?? chosen;

  // A query is about one picking, not about the picker.
  useEffect(() => { if (visible) { setQuery(''); setChosen('dinner'); } }, [visible]);

  const rows = useMemo(() => pickerRows(recipes, effective, query), [recipes, effective, query]);

  const title = slot ? t(`plan.chooseSlot.${slot}`) : t('plan.chooseRecipe');

  return (
    <Modal visible={visible} transparent animationType="fade" onRequestClose={onClose}>
      <TouchableOpacity style={styles.modalOverlay} activeOpacity={1} onPress={onClose}>
        <View style={styles.modalContent} onStartShouldSetResponder={() => true}>
          <Text style={styles.modalTitle}>{title}</Text>
          {recipes.length === 0 ? (
            <Text style={styles.emptySubtext}>{t('plan.noSavedRecipes')}</Text>
          ) : (
            <>
              {!slot && (
                <View style={styles.slotRow}>
                  {MEAL_SLOTS.map(ms => (
                    <Chip
                      key={ms}
                      label={takenSlots?.[ms]
                        ? t('plan.replaceSlot', { slot: t(slotLabelKey(ms)).toLowerCase(), title: takenSlots[ms] })
                        : t(slotLabelKey(ms))}
                      selected={chosen === ms}
                      onPress={() => setChosen(ms)}
                    />
                  ))}
                </View>
              )}
              <TextInput
                style={styles.search}
                value={query}
                onChangeText={setQuery}
                placeholder={t('plan.searchRecipes')}
                placeholderTextColor={theme.muted}
                autoCorrect={false}
                accessibilityLabel={t('plan.searchSavedRecipes')}
              />
              {rows.length === 0 ? (
                <Text style={styles.emptySubtext}>{t('plan.nothingMatches', { query: query.trim() })}</Text>
              ) : (
                <FlatList
                  data={rows}
                  keyboardShouldPersistTaps="handled"
                  keyExtractor={row => row.kind === 'heading' ? `h:${row.labelKey}` : String(row.recipe.id)}
                  renderItem={({ item }) => item.kind === 'heading' ? (
                    <Text style={styles.heading}>{t(item.labelKey)}</Text>
                  ) : (
                    <TouchableOpacity style={styles.pickerRow} onPress={() => onSelect(item.recipe, effective)}>
                      <Text style={styles.pickerRowText} numberOfLines={1}>{item.recipe.recipename}</Text>
                    </TouchableOpacity>
                  )}
                />
              )}
            </>
          )}
        </View>
      </TouchableOpacity>
    </Modal>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  modalOverlay: { flex: 1, backgroundColor: t.overlay, justifyContent: 'center', padding: 24 },
  modalContent: { backgroundColor: t.surface, borderRadius: 16, padding: 16, maxHeight: '70%' },
  modalTitle: { ...type.title, fontSize: 16, lineHeight: 22, color: t.text, marginBottom: 12 },
  emptySubtext: { ...type.body, fontSize: 14, color: t.subtext, textAlign: 'center' },
  search: {
    ...type.body,
    color: t.text,
    backgroundColor: t.surfaceRaised,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 10,
    marginBottom: 8,
  },
  slotRow: { flexDirection: 'row', flexWrap: 'wrap', gap: space.xs, marginBottom: space.sm },
  heading: { ...type.label, color: t.muted, paddingTop: 16, paddingBottom: 4 },
  pickerRow: { paddingVertical: 12, borderBottomWidth: 1, borderBottomColor: t.border },
  pickerRowText: { color: t.text, ...type.body },
});

export default RecipePickerModal;
