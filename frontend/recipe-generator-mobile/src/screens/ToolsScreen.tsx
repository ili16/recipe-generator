import React, { useEffect, useMemo, useState } from 'react';
import { ScrollView, StyleSheet, TextInput, View } from 'react-native';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import { useTheme, Theme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { useEscapeBack } from '../hooks/useEscapeBack';
import { Badge, Card, Chip, Screen, Text } from '../components/ui';
import { currentLocale } from '../i18n';
import apiService, { ApiError } from '../services/apiService';
import { space, radius, type } from '../theme';
import {
  KitchenCalculator, KitchenField, KitchenInputs, KitchenOutput, LText, SeasonCategory, SeasonEntry, SeasonResult,
} from '../types';

type Props = NativeStackScreenProps<RootStackParamList, 'Tools'>;

const SEASONAL = 'seasonal';

export const pickText = (l: LText) => l[currentLocale()];

// What the user sees for a number: no float noise, locale decimal separator.
export const formatValue = (v: number) => v.toLocaleString(currentLocale(), { maximumFractionDigits: 1 });

// Every tool is public and pure, so there is no auth gate here. The calculators are rendered
// from the backend's catalog (GET /tools): adding one is a backend-only change.
const ToolsScreen: React.FC<Props> = ({ route }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  useEscapeBack();

  const [catalog, setCatalog] = useState<KitchenCalculator[] | null>(null);
  const [selected, setSelected] = useState<string>(route.params?.tool ?? SEASONAL);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    apiService.getKitchenTools().then(setCatalog).catch(() => setFailed(true));
  }, []);

  // A later "Open in Tools" from the chat lands on this already-mounted screen.
  useEffect(() => {
    if (route.params?.tool) setSelected(route.params.tool);
  }, [route.params?.tool]);

  const calc = catalog?.find((c) => c.id === selected);

  return (
    <Screen padding="md">
      <ScrollView contentContainerStyle={{ gap: space.md, paddingBottom: space.xxl }} keyboardShouldPersistTaps="handled">
        <View style={styles.chips}>
          <Chip label={t('tools.seasonal')} selected={selected === SEASONAL} onPress={() => setSelected(SEASONAL)} />
          {catalog?.map((c) => (
            <Chip key={c.id} label={pickText(c.title)} selected={selected === c.id} onPress={() => setSelected(c.id)} />
          ))}
        </View>
        {failed && <Text tone="danger">{t('common.unknownError')}</Text>}
        {selected === SEASONAL && <SeasonalCalendar />}
        {calc && <Calculator key={calc.id} calc={calc} initial={route.params?.tool === calc.id ? route.params.inputs : undefined} />}
      </ScrollView>
    </Screen>
  );
};

const Calculator: React.FC<{ calc: KitchenCalculator; initial?: KitchenInputs }> = ({ calc, initial }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  // Numbers are held as the text being typed; an empty or unparsable field is left out so
  // the server's default applies instead of a 0.
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(calc.fields.map((f) => [f.name, String(initial?.[f.name] ?? f.default)])));
  const [outputs, setOutputs] = useState<KitchenOutput[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const inputs: KitchenInputs = {};
    for (const f of calc.fields) {
      const raw = values[f.name];
      if (f.kind === 'select') inputs[f.name] = raw;
      else if (raw.trim() !== '' && Number.isFinite(Number(raw.replace(',', '.')))) inputs[f.name] = Number(raw.replace(',', '.'));
    }
    let live = true;
    const timer = setTimeout(() => {
      apiService.runKitchenTool(calc.id, inputs)
        .then((o) => { if (live) { setOutputs(o); setError(null); } })
        .catch((e) => { if (live) { setOutputs(null); setError(e instanceof ApiError && typeof e.data === 'string' ? e.data : ''); } });
    }, 250);
    return () => { live = false; clearTimeout(timer); };
  }, [calc, values]);

  const set = (name: string, v: string) => setValues((prev) => ({ ...prev, [name]: v }));

  return (
    <>
      <Text tone="muted">{pickText(calc.desc)}</Text>
      <Card>
        <View style={{ gap: space.md }}>
          {calc.fields.map((f) => <Field key={f.name} f={f} value={values[f.name]} onChange={(v) => set(f.name, v)} styles={styles} />)}
        </View>
      </Card>
      <Card elevation="raised">
        <Text variant="label" tone="muted">{t('tools.results')}</Text>
        {error !== null && <Text tone="danger">{error || t('tools.error')}</Text>}
        {error === null && outputs?.map((o) => (
          <View key={o.key} style={styles.outRow}>
            <Text style={{ flex: 1 }}>{pickText(o.label)}</Text>
            <Text variant="title">{formatValue(o.value)} {o.unit}</Text>
            {o.note && <Text variant="caption" tone="muted">{pickText(o.note)}</Text>}
          </View>
        ))}
      </Card>
    </>
  );
};

const Field: React.FC<{ f: KitchenField; value: string; onChange: (v: string) => void; styles: ReturnType<typeof makeStyles> }> = ({ f, value, onChange, styles }) => (
  <View style={{ gap: space.xs }}>
    <Text variant="label">{pickText(f.label)}{f.unit ? ` (${f.unit})` : ''}</Text>
    {f.kind === 'select' ? (
      <View style={styles.chips}>
        {f.options?.map((o) => <Chip key={o.value} label={pickText(o.label)} selected={value === o.value} onPress={() => onChange(o.value)} />)}
      </View>
    ) : (
      <TextInput
        style={styles.input}
        value={value}
        onChangeText={onChange}
        keyboardType="decimal-pad"
        inputMode="decimal"
        accessibilityLabel={pickText(f.label)}
      />
    )}
  </View>
);

const SeasonalCalendar: React.FC = () => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [month, setMonth] = useState(() => new Date().getMonth() + 1);
  const [category, setCategory] = useState<SeasonCategory | undefined>(undefined);
  const [data, setData] = useState<SeasonResult | null>(null);

  useEffect(() => {
    let live = true;
    apiService.getSeasonal(month, category).then((d) => live && setData(d)).catch(() => live && setData(null));
    return () => { live = false; };
  }, [month, category]);

  const monthName = (m: number) => new Date(2000, m - 1, 1).toLocaleString(currentLocale(), { month: 'short' });
  const cats: (SeasonCategory | undefined)[] = [undefined, 'vegetable', 'fruit', 'game'];

  return (
    <>
      <Badge label={t('tools.germanyOnly')} />
      <View style={styles.chips}>
        {Array.from({ length: 12 }, (_, i) => i + 1).map((m) => (
          <Chip key={m} label={monthName(m)} selected={m === month} onPress={() => setMonth(m)} />
        ))}
      </View>
      <View style={styles.chips}>
        {cats.map((c) => <Chip key={c ?? 'all'} label={t(`tools.${c ?? 'all'}`)} selected={c === category} onPress={() => setCategory(c)} />)}
      </View>
      {data && (
        <>
          <Section title={t('tools.inSeason')} items={data.in_season} styles={styles} />
          <Section title={t('tools.endingSoon')} items={data.ending_soon} styles={styles} />
          <Section title={t('tools.nextMonth')} items={data.next_month} styles={styles} />
        </>
      )}
      <Text variant="caption" tone="muted">{t('tools.seasonalSource')}</Text>
    </>
  );
};

const Section: React.FC<{ title: string; items: SeasonEntry[]; styles: ReturnType<typeof makeStyles> }> = ({ title, items, styles }) => {
  const { t } = useLanguage();
  if (items.length === 0) return null;
  return (
    <Card>
      <Text variant="label" tone="muted">{title}</Text>
      <View style={styles.chips}>
        {items.map((e) => (
          <Badge key={e.name.en + e.category} tone={e.status === 'stored' ? 'neutral' : 'accent'}
            label={pickText(e.name) + (e.status === 'stored' ? ` · ${t('tools.stored')}` : '')} />
        ))}
      </View>
    </Card>
  );
};

const makeStyles = (th: Theme) => StyleSheet.create({
  chips: { flexDirection: 'row', flexWrap: 'wrap', gap: space.sm, marginTop: space.xs },
  input: {
    ...type.body,
    color: th.text,
    backgroundColor: th.bg,
    borderColor: th.border,
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: radius.md,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
  },
  outRow: { flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', gap: space.sm, paddingVertical: space.xs },
});

export default ToolsScreen;
