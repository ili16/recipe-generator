import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { View, StyleSheet, ScrollView, TouchableOpacity, TextInput, ActivityIndicator } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import apiService from '../services/apiService';
import { PantryCategory, PantryItem } from '../types';
import { useTheme, Theme } from '../context/ThemeContext';
import { space, radius } from '../theme';
import { Text, Button, Chip, Badge, SignInRequired, Sheet, SheetRow, SwipeRow } from '../components/ui';
import { useEscapeBack } from '../hooks/useEscapeBack';
import { useIsAuthenticated } from '../hooks/useIsAuthenticated';
import { useLanguage } from '../context/LanguageContext';
import { daysUntil, expiryLabel, stockCheckDue, USE_FIRST_DAYS } from '../utils/pantryExpiry';
import { readStaplesCollapsed, writeStaplesCollapsed } from '../utils/pantryViewStore';

type Props = NativeStackScreenProps<RootStackParamList, 'Pantry'>;

// Sections, in the order the mockup's inventory reads (DESIGN_SYSTEM §7.7).
const CATEGORIES: Array<{ key: PantryCategory; labelKey: string }> = [
  { key: 'fridge', labelKey: 'pantry.shelf.fridge' },
  { key: 'freezer', labelKey: 'pantry.shelf.freezer' },
  { key: 'produce', labelKey: 'pantry.shelf.produce' },
  { key: 'spices_dry', labelKey: 'pantry.shelf.spices_dry' },
];

// "Use first" is derived from the expiry date rather than being a column the user has to
// maintain: anything due within USE_FIRST_DAYS is what you cook next (BACKLOG 7.4). A flag
// would be a second source of truth for the same fact, and one that goes stale.

const amountLabel = (item: PantryItem): string => {
  if (item.quantity == null) return '';
  const n = Math.round(item.quantity * 100) / 100;
  return item.unit ? `${n} ${item.unit}` : `${n}`;
};

/**
 * What the user has in the house (BACKLOG 7.1, 7.4) — the inventory half of the Pantry tab.
 *
 * Server-held, unlike the grocery list's ticks: a pantry is an account-level fact, and it is
 * what the grocery list writes into when a line is checked off (7.2). Items arrive already
 * normalised (lowercased name, kg folded into g), so what is shown here is exactly what a
 * recipe's ingredients are matched against.
 */
const PantryScreen: React.FC<Props> = ({ navigation }) => {
  const authed = useIsAuthenticated();
  const [items, setItems] = useState<PantryItem[] | null>(null);
  const [error, setError] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState('');
  const [quantity, setQuantity] = useState('');
  const [unit, setUnit] = useState('');
  const [expiresOn, setExpiresOn] = useState('');
  const [category, setCategory] = useState<PantryCategory>('fridge');
  const [staple, setStaple] = useState(false);
  // The row whose options are open. One sheet serves both tiers: the verbs differ, the
  // component does not (BACKLOG 16.6).
  const [sheetItem, setSheetItem] = useState<PantryItem | null>(null);
  // The things the kitchen always has are the things you never need to read (BACKLOG 12.6).
  // Folded away by default, and the fold is remembered.
  const [staplesCollapsed, setStaplesCollapsed] = useState(true);
  useEffect(() => { readStaplesCollapsed().then(setStaplesCollapsed); }, []);
  const toggleStaples = () => setStaplesCollapsed(v => { writeStaplesCollapsed(!v); return !v; });
  const scroller = useRef<ScrollView>(null);
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  useEscapeBack();

  const load = useCallback(() => {
    if (!authed) return;
    setError(false);
    apiService.getPantry()
      .then(setItems)
      .catch(err => { console.error('Error loading pantry:', err); setError(true); });
  }, [authed]);

  // Re-read on focus: checking a grocery line writes here (7.2), so this list is stale the
  // moment the user comes back from a shopping trip.
  useEffect(load, [load]);
  useEffect(() => navigation.addListener('focus', load), [navigation, load]);

  const add = async () => {
    if (!name.trim() || saving) return;
    setSaving(true);
    try {
      const parsed = parseFloat(quantity.replace(',', '.'));
      // A staple carries no amount and no expiry on purpose: it is a yes/no, and the
      // inputs for them are hidden while the toggle is on.
      const saved = await apiService.savePantryItem({
        name: name.trim(),
        quantity: staple || !Number.isFinite(parsed) ? null : parsed,
        unit: staple ? null : unit.trim() || null,
        category,
        expires_on: staple ? null : expiresOn.trim() || null,
        staple,
      });
      // Upsert: replace the row of the same id if it was already there, else append.
      setItems(prev => [...(prev ?? []).filter(i => i.id !== saved.id), saved]);
      setName(''); setQuantity(''); setUnit(''); setExpiresOn('');
    } catch (err) {
      console.error('Error saving pantry item:', err);
      setError(true);
    } finally {
      setSaving(false);
    }
  };

  const remove = async (item: PantryItem) => {
    setSheetItem(null);
    setItems(prev => (prev ?? []).filter(i => i.id !== item.id));
    try {
      await apiService.deletePantryItem(item.id);
    } catch (err) {
      console.error('Error deleting pantry item:', err);
      load();
    }
  };

  // The only thing that ever changes about a standing fact. Optimistic, because the tap
  // has to feel like flipping a switch; either answer also confirms the line, which is
  // what takes it out of the stock check.
  const setStock = async (item: PantryItem, running_low: boolean) => {
    setSheetItem(null);
    setItems(prev => (prev ?? []).map(i =>
      i.id === item.id ? { ...i, running_low, confirmed_at: new Date().toISOString() } : i));
    try {
      const saved = await apiService.setPantryStock(item.id, running_low);
      setItems(prev => (prev ?? []).map(i => (i.id === saved.id ? saved : i)));
    } catch (err) {
      console.error('Error updating pantry stock:', err);
      load();
    }
  };

  // Editing is the add card with the row's values in it: name and unit are the row's
  // identity, so saving replaces the line rather than making a second one. No second
  // form to keep in step with the first.
  const edit = (item: PantryItem) => {
    setSheetItem(null);
    setName(item.name);
    setQuantity(item.quantity != null ? String(item.quantity) : '');
    setUnit(item.unit ?? '');
    setExpiresOn(item.expires_on ?? '');
    setCategory(item.category);
    setStaple(!!item.staple);
    scroller.current?.scrollTo({ y: 0, animated: true });
  };

  // Both groups are derived, not stored — same reasoning as "Use first" above. A staple
  // is an ordinary row on an ordinary shelf; what marks it out is one flag.
  const staples = (items ?? []).filter(i => i.staple);
  const useFirst = (items ?? []).filter(i => !i.staple && i.expires_on != null && daysUntil(i.expires_on) <= USE_FIRST_DAYS);
  // A staple nobody has confirmed in four weeks. It keeps counting for the recipe match
  // while it waits — the card asks, it does not retract a fact the user never withdrew.
  const stale = staples.filter(i => stockCheckDue(i.confirmed_at));

  // The starter set, offered once and only while there are none. Names are localized
  // because the normalised name is the join key: a German recipe asks for "salz", and an
  // English staple list would never match it.
  const addCommonStaples = async () => {
    if (saving) return;
    setSaving(true);
    try {
      const names = t('pantry.commonStaples') as unknown as string[];
      const saved = await Promise.all(names.map(n => apiService.savePantryItem({
        name: n, quantity: null, unit: null, category: 'spices_dry', expires_on: null, staple: true,
      })));
      const ids = new Set(saved.map(i => i.id));
      setItems(prev => [...(prev ?? []).filter(i => !ids.has(i.id)), ...saved]);
    } catch (err) {
      console.error('Error adding staples:', err);
      setError(true);
    } finally {
      setSaving(false);
    }
  };

  // "Find recipes using these" is a chat turn, not new machinery (BACKLOG 7.4): the agent's
  // search_my_recipes tool answers it through its `ingredients` filter, and offers to invent
  // something when the library has nothing.
  const findRecipes = () => navigation.navigate('Chat', {
    prompt: `What can I cook with these before they go off: ${useFirst.map(i => i.name).join(', ')}?`,
  });

  if (authed === null) {
    return <ActivityIndicator style={styles.pad} color={theme.accent} />;
  }

  if (!authed) {
    return (
      <SignInRequired
        message={t('pantry.signInRequired')}
        onSignIn={() => navigation.navigate('Login')}
      />
    );
  }

  return (
    <ScrollView ref={scroller} style={styles.container} contentContainerStyle={styles.list}>
      {stale.length > 0 && (
        <View style={styles.section}>
          <Text variant="label" tone="subtle">{t('pantry.stockCheckTitle', { count: stale.length })}</Text>
          {stale.map(item => (
            <View key={item.id} style={styles.itemRow}>
              <Text style={styles.flex}>{item.name}</Text>
              <Button title={t('pantry.haveIt')} variant="secondary" size="sm" onPress={() => setStock(item, false)} />
              <Button title={t('pantry.runningLow')} variant="secondary" size="sm" onPress={() => setStock(item, true)} />
            </View>
          ))}
        </View>
      )}

      <View style={styles.addCard}>
        <Text variant="label" tone="subtle">{t('pantry.addItem')}</Text>
        <TextInput
          autoComplete="off"
          style={styles.input}
          placeholder={t('pantry.namePlaceholder')}
          placeholderTextColor={theme.muted}
          value={name}
          onChangeText={setName}
          onSubmitEditing={add}
          returnKeyType="done"
        />
        {!staple && (
        <View style={styles.row}>
          <TextInput
            autoComplete="off"
            style={[styles.input, styles.short]}
            placeholder="500"
            placeholderTextColor={theme.muted}
            keyboardType="numeric"
            value={quantity}
            onChangeText={setQuantity}
          />
          <TextInput
            autoComplete="off"
            style={[styles.input, styles.short]}
            placeholder="g"
            placeholderTextColor={theme.muted}
            value={unit}
            onChangeText={setUnit}
          />
          <TextInput
            autoComplete="off"
            style={[styles.input, styles.flex]}
            placeholder={t('pantry.expiresPlaceholder')}
            placeholderTextColor={theme.muted}
            value={expiresOn}
            onChangeText={setExpiresOn}
          />
        </View>
        )}
        <View style={styles.chipRow}>
          {CATEGORIES.map(c => (
            <Chip key={c.key} label={t(c.labelKey)} selected={category === c.key} onPress={() => setCategory(c.key)} />
          ))}
        </View>
        <TouchableOpacity
          style={styles.stapleRow}
          onPress={() => setStaple(v => !v)}
          accessibilityRole="checkbox"
          accessibilityState={{ checked: staple }}
          accessibilityLabel={t('pantry.staple')}
        >
          <Ionicons
            name={staple ? 'checkbox' : 'square-outline'}
            size={20}
            color={staple ? theme.accent : theme.muted}
          />
          <View style={styles.flex}>
            <Text>{t('pantry.staple')}</Text>
            <Text variant="caption" tone="subtle">{t('pantry.stapleHint')}</Text>
          </View>
        </TouchableOpacity>
        <Button title={t('common.add')} onPress={add} loading={saving} disabled={!name.trim()} />
      </View>

      {error && (
        <View style={styles.section}>
          <Text tone="danger">{t('pantry.loadFailed')}</Text>
          <Button title={t('common.retry')} variant="secondary" size="sm" onPress={load} />
        </View>
      )}

      {staples.length > 0 ? (
        <View style={styles.section}>
          <TouchableOpacity
            style={styles.sectionToggle}
            onPress={toggleStaples}
            accessibilityRole="button"
            accessibilityState={{ expanded: !staplesCollapsed }}
            accessibilityLabel={t(staplesCollapsed ? 'pantry.showStaples' : 'pantry.hideStaples', { count: staples.length })}
          >
            <Text variant="label" tone="subtle">{t('pantry.staplesCount', { count: staples.length })}</Text>
            <Ionicons name={staplesCollapsed ? 'chevron-down' : 'chevron-up'} size={14} color={theme.muted} />
          </TouchableOpacity>
          {!staplesCollapsed && staples.map(item => (
            <SwipeRow
              key={item.id}
              left={{
                icon: item.running_low ? 'checkmark-circle' : 'alert-circle-outline',
                label: t(item.running_low ? 'pantry.haveIt' : 'pantry.runningLow'),
                onAction: () => setStock(item, !item.running_low),
              }}
              right={{ icon: 'trash-outline', label: t('common.remove'), tone: 'danger', onAction: () => remove(item) }}
            >
              <View style={styles.itemRow}>
                {/* One tap flips the only thing that ever changes. Everything rarer —
                    renaming it, taking it off the list — is one tap deeper, in the sheet. */}
                <TouchableOpacity
                  style={styles.stockRow}
                  onPress={() => setStock(item, !item.running_low)}
                  accessibilityRole="checkbox"
                  accessibilityState={{ checked: !item.running_low }}
                  accessibilityLabel={item.name}
                >
                  <Ionicons
                    name={item.running_low ? 'alert-circle-outline' : 'checkmark-circle'}
                    size={20}
                    color={item.running_low ? theme.accent : theme.muted}
                  />
                  <Text style={styles.flex}>{item.name}</Text>
                  {item.running_low && <Badge label={t('pantry.low')} tone="accent" />}
                </TouchableOpacity>
                <TouchableOpacity
                  onPress={() => setSheetItem(item)}
                  accessibilityRole="button"
                  accessibilityLabel={t('pantry.itemActions', { name: item.name })}
                  style={styles.removeButton}
                >
                  <Ionicons name="ellipsis-horizontal" size={18} color={theme.muted} />
                </TouchableOpacity>
              </View>
            </SwipeRow>
          ))}
        </View>
      ) : items !== null && (
        <View style={styles.section}>
          <Text variant="label" tone="subtle">{t('pantry.staples')}</Text>
          <Text variant="caption" tone="subtle">{t('pantry.stapleHint')}</Text>
          <Button title={t('pantry.addCommon')} variant="secondary" size="sm" onPress={addCommonStaples} loading={saving} />
        </View>
      )}

      {useFirst.length > 0 && (
        <View style={styles.section}>
          <Text variant="label" tone="subtle">{t('pantry.useFirst')}</Text>
          <Text tone="subtle" variant="caption">
            {useFirst.map(i => i.name).join(' · ')}
          </Text>
          <Button title={t('pantry.findRecipes')} variant="secondary" size="sm" onPress={findRecipes} />
        </View>
      )}

      {items === null && !error ? (
        <ActivityIndicator style={styles.pad} color={theme.accent} />
      ) : (items ?? []).length === 0 ? (
        <Text tone="subtle">{t('pantry.empty')}</Text>
      ) : (
        CATEGORIES.map(c => {
          const section = (items ?? []).filter(i => !i.staple && i.category === c.key);
          if (section.length === 0) return null;
          return (
            <View key={c.key} style={styles.section}>
              <Text variant="label" tone="subtle">{t(c.labelKey)}</Text>
              {section.map(item => {
                const days = item.expires_on != null ? daysUntil(item.expires_on) : null;
                const amount = amountLabel(item);
                return (
                  // Swipe is the accelerator; the tap still opens the sheet that holds every
                  // verb, including these two (BACKLOG 12.5).
                  <SwipeRow
                    key={item.id}
                    left={{
                      icon: item.running_low ? 'checkmark-circle' : 'alert-circle-outline',
                      label: t(item.running_low ? 'pantry.haveIt' : 'pantry.runningLow'),
                      onAction: () => setStock(item, !item.running_low),
                    }}
                    right={{ icon: 'trash-outline', label: t('pantry.usedItUp'), tone: 'danger', onAction: () => remove(item) }}
                    onPress={() => setSheetItem(item)}
                    accessibilityLabel={t('pantry.itemActions', { name: item.name })}
                  >
                    <View style={styles.itemRow}>
                      <View style={styles.flex}>
                        <Text>{amount ? `${amount} · ` : ''}{item.name}</Text>
                      </View>
                      {item.running_low && <Badge label={t('pantry.low')} tone="accent" />}
                      {days !== null && (
                        <Badge
                          label={expiryLabel(days, t)}
                          tone={days <= USE_FIRST_DAYS ? 'accent' : 'neutral'}
                        />
                      )}
                    </View>
                  </SwipeRow>
                );
              })}
            </View>
          );
        })
      )}

      {/* The verbs a row actually has. "Used it up" and "Remove" both clear the line;
          they are two different things in the user's head, which is the whole reason the
          `×` had to go. */}
      <Sheet visible={sheetItem !== null} onClose={() => setSheetItem(null)} title={sheetItem?.name}>
        {sheetItem && !sheetItem.staple && (
          <SheetRow label={t('pantry.usedItUp')} onPress={() => remove(sheetItem)} />
        )}
        {sheetItem && (
          <SheetRow
            label={sheetItem.running_low ? t('pantry.haveIt') : t('pantry.runningLow')}
            onPress={() => setStock(sheetItem, !sheetItem.running_low)}
          />
        )}
        {sheetItem && <SheetRow label={t('common.edit')} onPress={() => edit(sheetItem)} />}
        {sheetItem && (
          <SheetRow
            label={t('pantry.removeItem', { name: sheetItem.name })}
            destructive
            onPress={() => remove(sheetItem)}
          />
        )}
      </Sheet>
    </ScrollView>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: { flex: 1, backgroundColor: t.bg },
  list: { padding: space.lg, gap: space.xl, maxWidth: 720, width: '100%', alignSelf: 'center' },
  pad: { padding: space.lg },
  addCard: { gap: space.sm },
  row: { flexDirection: 'row', gap: space.sm },
  chipRow: { flexDirection: 'row', flexWrap: 'wrap', gap: space.sm },
  flex: { flex: 1 },
  short: { width: 72 },
  input: {
    borderWidth: 1, borderColor: t.border, borderRadius: radius.md,
    paddingHorizontal: space.md, paddingVertical: space.sm + 2,
    color: t.text, backgroundColor: t.surface,
  },
  section: { gap: space.sm },
  sectionToggle: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingVertical: space.xs },
  stapleRow: { flexDirection: 'row', alignItems: 'center', gap: space.sm, paddingVertical: space.xs },
  itemRow: { flexDirection: 'row', alignItems: 'center', gap: space.sm, paddingVertical: space.xs },
  stockRow: { flex: 1, flexDirection: 'row', alignItems: 'center', gap: space.sm, paddingVertical: space.xs },
  removeButton: { padding: space.xs },
});

export default PantryScreen;
