import React, { useMemo, useState } from 'react';
import { View, Text, TextInput, StyleSheet, ScrollView, TouchableOpacity } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { RecipeDocument } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import { useAlert } from '../../context/AlertContext';
import { TAGS_BY_GROUP, tagGroupLabelKey, tagLabelKey, TagGroup } from '../../constants/tags';
import { makeSharedStyles } from './styles';
import { Chip } from '../../components/ui';

interface Props {
  initial: RecipeDocument;
  saving: boolean;
  onCancel: () => void;
  onSave: (doc: RecipeDocument) => void;
}

// The manual-edit form for one recipe. It owns the draft document; the screen only
// hears about it when the user saves.
const RecipeEditForm: React.FC<Props> = ({ initial, saving, onCancel, onSave }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const { showAlert } = useAlert();
  const s = useMemo(() => makeSharedStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [doc, setDoc] = useState<RecipeDocument>(() => JSON.parse(JSON.stringify(initial)));

  const updateDoc = (patch: Partial<RecipeDocument>) => setDoc(prev => ({ ...prev, ...patch }));

  const updateIngredient = (idx: number, patch: Partial<RecipeDocument['ingredients'][number]>) => {
    setDoc(prev => ({ ...prev, ingredients: prev.ingredients.map((ing, i) => (i === idx ? { ...ing, ...patch } : ing)) }));
  };

  const addIngredient = () => {
    setDoc(prev => ({
      ...prev,
      ingredients: [...prev.ingredients, { item: '', quantity: null, quantity_text: '', unit: null, section: null, optional: false }],
    }));
  };

  const removeIngredient = (idx: number) => {
    setDoc(prev => ({
      ...prev,
      ingredients: prev.ingredients.filter((_, i) => i !== idx),
      // keep step -> ingredient links valid: drop refs to the removed ingredient, shift later indices down
      steps: prev.steps.map(st => ({
        ...st,
        ingredient_indices: (st.ingredient_indices ?? []).filter(i => i !== idx).map(i => (i > idx ? i - 1 : i)),
      })),
    }));
  };

  const updateStep = (idx: number, patch: Partial<RecipeDocument['steps'][number]>) => {
    setDoc(prev => ({ ...prev, steps: prev.steps.map((st, i) => (i === idx ? { ...st, ...patch } : st)) }));
  };

  const addStep = () => {
    setDoc(prev => ({
      ...prev,
      steps: [...prev.steps, { sort_order: prev.steps.length + 1, step_text: '', timer_seconds: null, temperature_c: null, ingredient_indices: [] }],
    }));
  };

  const removeStep = (idx: number) => {
    setDoc(prev => ({
      ...prev,
      steps: prev.steps.filter((_, i) => i !== idx).map((st, i) => ({ ...st, sort_order: i + 1 })),
    }));
  };

  const toggleTag = (slug: string) => {
    setDoc(prev => ({
      ...prev,
      tags: prev.tags.includes(slug) ? prev.tags.filter(t => t !== slug) : [...prev.tags, slug],
    }));
  };

  const save = () => {
    if (!doc.title.trim()) {
      showAlert(t('common.error'), t('edit.titleRequired'), 'error');
      return;
    }
    onSave(doc);
  };

  const numeric = (t: string) => (t.trim() ? parseInt(t, 10) || null : null);

  return (
    <>
      <ScrollView style={styles.editFormScroll} nestedScrollEnabled>
        <Text style={s.fieldLabel}>{t('edit.title')}</Text>
        <TextInput autoComplete="off"
          style={s.fieldInput}
          value={doc.title}
          onChangeText={t => updateDoc({ title: t })}
          placeholderTextColor={theme.muted}
        />

        <Text style={s.fieldLabel}>{t('edit.summary')}</Text>
        <TextInput autoComplete="off"
          style={[s.fieldInput, s.fieldInputMultiline]}
          value={doc.summary ?? ''}
          onChangeText={t => updateDoc({ summary: t })}
          multiline
          placeholderTextColor={theme.muted}
        />

        <View style={s.fieldRow}>
          <View style={s.fieldCol}>
            <Text style={s.fieldLabel}>{t('common.servings')}</Text>
            <TextInput autoComplete="off"
              style={s.fieldInput}
              keyboardType="numeric"
              value={doc.servings != null ? String(doc.servings) : ''}
              onChangeText={t => updateDoc({ servings: numeric(t) })}
              placeholderTextColor={theme.muted}
            />
          </View>
          <View style={s.fieldCol}>
            <Text style={s.fieldLabel}>{t('edit.totalMin')}</Text>
            <TextInput autoComplete="off"
              style={s.fieldInput}
              keyboardType="numeric"
              value={doc.total_minutes != null ? String(doc.total_minutes) : ''}
              onChangeText={t => updateDoc({ total_minutes: numeric(t) })}
              placeholderTextColor={theme.muted}
            />
          </View>
          <View style={s.fieldCol}>
            <Text style={s.fieldLabel}>{t('edit.prepMin')}</Text>
            <TextInput autoComplete="off"
              style={s.fieldInput}
              keyboardType="numeric"
              value={doc.prep_minutes != null ? String(doc.prep_minutes) : ''}
              onChangeText={t => updateDoc({ prep_minutes: numeric(t) })}
              placeholderTextColor={theme.muted}
            />
          </View>
          <View style={s.fieldCol}>
            <Text style={s.fieldLabel}>{t('edit.cookMin')}</Text>
            <TextInput autoComplete="off"
              style={s.fieldInput}
              keyboardType="numeric"
              value={doc.cook_minutes != null ? String(doc.cook_minutes) : ''}
              onChangeText={t => updateDoc({ cook_minutes: numeric(t) })}
              placeholderTextColor={theme.muted}
            />
          </View>
        </View>

        <Text style={s.fieldLabel}>{t('edit.difficulty')}</Text>
        <TextInput autoComplete="off"
          style={s.fieldInput}
          value={doc.difficulty ?? ''}
          onChangeText={t => updateDoc({ difficulty: t })}
          placeholder={t('edit.difficultyPlaceholder')}
          placeholderTextColor={theme.muted}
        />

        <Text style={s.sectionLabel}>{t('edit.ingredients')}</Text>
        {doc.ingredients.map((ing, idx) => (
          <View key={idx} style={styles.ingredientEditRow}>
            <TextInput autoComplete="off"
              style={[s.fieldInput, styles.ingredientAmountInput]}
              placeholder={t('edit.amount')}
              placeholderTextColor={theme.muted}
              value={ing.quantity_text ?? ''}
              onChangeText={t => updateIngredient(idx, { quantity_text: t })}
            />
            <TextInput autoComplete="off"
              style={[s.fieldInput, styles.ingredientItemInput]}
              placeholder={t('edit.ingredient')}
              placeholderTextColor={theme.muted}
              value={ing.item}
              onChangeText={t => updateIngredient(idx, { item: t })}
            />
            <TouchableOpacity
              style={[styles.optionalToggle, ing.optional && styles.optionalToggleOn]}
              onPress={() => updateIngredient(idx, { optional: !ing.optional })}
            >
              <Text style={[styles.optionalToggleText, ing.optional && styles.optionalToggleTextOn]}>opt</Text>
            </TouchableOpacity>
            <TouchableOpacity style={styles.removeRowButton} onPress={() => removeIngredient(idx)}>
              <Ionicons name="close" size={13} color={theme.muted} />
            </TouchableOpacity>
          </View>
        ))}
        <TouchableOpacity style={styles.addRowButton} onPress={addIngredient}>
          <Text style={styles.addRowButtonText}>{t('edit.addIngredient')}</Text>
        </TouchableOpacity>

        <Text style={s.sectionLabel}>{t('edit.steps')}</Text>
        {doc.steps.map((step, idx) => (
          <View key={idx} style={styles.stepEditBlock}>
            <View style={styles.stepEditHeader}>
              <Text style={styles.stepEditNumber}>{idx + 1}.</Text>
              <TouchableOpacity style={styles.removeRowButton} onPress={() => removeStep(idx)}>
                <Ionicons name="close" size={13} color={theme.muted} />
              </TouchableOpacity>
            </View>
            <TextInput autoComplete="off"
              style={[s.fieldInput, styles.stepTextInput]}
              placeholder={t('edit.step')}
              placeholderTextColor={theme.muted}
              value={step.step_text}
              onChangeText={t => updateStep(idx, { step_text: t })}
              multiline
            />
            <View style={s.fieldRow}>
              <View style={s.fieldCol}>
                <Text style={s.fieldLabel}>{t('edit.timerSec')}</Text>
                <TextInput autoComplete="off"
                  style={s.fieldInput}
                  keyboardType="numeric"
                  value={step.timer_seconds != null ? String(step.timer_seconds) : ''}
                  onChangeText={t => updateStep(idx, { timer_seconds: numeric(t) })}
                  placeholderTextColor={theme.muted}
                />
              </View>
              <View style={s.fieldCol}>
                <Text style={s.fieldLabel}>{t('edit.tempC')}</Text>
                <TextInput autoComplete="off"
                  style={s.fieldInput}
                  keyboardType="numeric"
                  value={step.temperature_c != null ? String(step.temperature_c) : ''}
                  onChangeText={t => updateStep(idx, { temperature_c: numeric(t) })}
                  placeholderTextColor={theme.muted}
                />
              </View>
            </View>
          </View>
        ))}
        <TouchableOpacity style={styles.addRowButton} onPress={addStep}>
          <Text style={styles.addRowButtonText}>{t('edit.addStep')}</Text>
        </TouchableOpacity>

        <Text style={s.sectionLabel}>{t('recipes.tags')}</Text>
        {(Object.keys(TAGS_BY_GROUP) as TagGroup[]).map(group => (
          <View key={group} style={s.filterGroup}>
            <Text style={s.filterGroupLabel}>{t(tagGroupLabelKey(group))}</Text>
            <View style={s.tagRow}>
              {TAGS_BY_GROUP[group].map(tag => (
                <Chip
                  key={tag.slug}
                  label={t(tagLabelKey(tag.slug))}
                  selected={doc.tags.includes(tag.slug)}
                  onPress={() => toggleTag(tag.slug)}
                />
              ))}
            </View>
          </View>
        ))}
      </ScrollView>

      <View style={s.cardActions}>
        <TouchableOpacity style={styles.cancelButton} onPress={onCancel} disabled={saving}>
          <Text style={styles.cancelButtonText}>{t('common.cancel')}</Text>
        </TouchableOpacity>
        <TouchableOpacity
          style={[s.deleteButton, saving && s.btnDisabled]}
          onPress={save}
          disabled={saving}
        >
          <Text style={s.deleteButtonText}>{saving ? t('common.saving') : t('common.save')}</Text>
        </TouchableOpacity>
      </View>
    </>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  editFormScroll: {
    maxHeight: 420,
    marginBottom: 15,
  },
  cancelButton: {
    backgroundColor: t.surface,
    borderWidth: 1.5,
    borderColor: t.border,
    paddingHorizontal: 20,
    paddingVertical: 10,
    borderRadius: 8,
  },
  cancelButtonText: {
    color: t.subtext,
    ...type.label,
  },
  ingredientEditRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    marginBottom: 6,
  },
  ingredientAmountInput: {
    flex: 1,
  },
  ingredientItemInput: {
    flex: 2,
  },
  optionalToggle: {
    paddingHorizontal: 8,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: t.border,
    backgroundColor: t.bg,
  },
  optionalToggleOn: {
    borderColor: t.accent,
    backgroundColor: t.accentFaded,
  },
  optionalToggleText: {
    ...type.label, fontSize: 11,
    color: t.muted,
  },
  optionalToggleTextOn: {
    color: t.accent,
  },
  removeRowButton: {
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: t.surface,
    borderWidth: 1,
    borderColor: t.border,
  },
  addRowButton: {
    marginTop: 4,
    paddingVertical: 8,
    alignItems: 'center',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: t.border,
    borderStyle: 'dashed',
  },
  addRowButtonText: {
    color: t.accent,
    ...type.label, fontSize: 13,
  },
  stepEditBlock: {
    marginBottom: 10,
  },
  stepEditHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
  },
  stepEditNumber: {
    ...type.label, fontSize: 13,
    color: t.subtext,
  },
  stepTextInput: {
    minHeight: 44,
    textAlignVertical: 'top',
    marginTop: 4,
    marginBottom: 6,
  },
});

export default RecipeEditForm;
