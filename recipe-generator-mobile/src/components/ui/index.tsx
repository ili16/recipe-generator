/**
 * UI primitives — BACKLOG 5.3. Spec: `specs/DESIGN_SYSTEM.md` §4.
 *
 * One file, because each of these is a dozen lines; `import { Button, Card } from '../components/ui'`
 * is the whole API. The rule they exist to enforce: a screen never names a colour again — every
 * value here comes from `useTheme()` or the scales in `src/theme/`.
 */
import React, { ReactNode, useMemo } from 'react';
import {
  ActivityIndicator,
  Modal,
  Platform,
  Pressable,
  StyleProp,
  StyleSheet,
  Text as RNText,
  TextStyle,
  TouchableOpacity,
  View,
  ViewStyle,
  useWindowDimensions,
} from 'react-native';
import { Theme, useTheme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { layout, radius, space, type as typeScale } from '../../theme';

/** True on a wide viewport. The one breakpoint (5.1) — do not compare widths anywhere else. */
export const useIsWide = () => useWindowDimensions().width >= layout.breakpointWide;

/**
 * True where the shell shows the `Sidebar` rail instead of bottom tabs (5.7): wide **web** only.
 * A wide native tablet keeps the tabs. `AppShell` and `AppNavigator` both branch on this and must
 * agree — the header's Profile button is only there because narrow has no rail.
 */
export const useIsDesktopNav = () => {
  const wide = useIsWide(); // called unconditionally: `Platform.OS === 'web' && useIsWide()` is a
  return Platform.OS === 'web' && wide; // conditional hook, even though the condition is constant.
};

/* ── Text ─────────────────────────────────────────────────────────────────── */

type TextVariant = keyof typeof typeScale;
type Tone = 'default' | 'subtle' | 'muted' | 'accent' | 'danger' | 'onPrimary';

const toneColor = (t: Theme, tone: Tone) =>
  tone === 'subtle' ? t.subtext
  : tone === 'muted' ? t.muted
  : tone === 'accent' ? t.accent
  : tone === 'danger' ? t.danger
  : tone === 'onPrimary' ? t.onPrimary
  : t.text;

export const Text: React.FC<{
  variant?: TextVariant;
  tone?: Tone;
  style?: StyleProp<TextStyle>;
  numberOfLines?: number;
  children?: ReactNode;
}> = ({ variant = 'body', tone = 'default', style, numberOfLines, children }) => {
  const { theme } = useTheme();
  return (
    <RNText
      numberOfLines={numberOfLines}
      style={[typeScale[variant] as TextStyle, { color: toneColor(theme, tone) }, style]}
    >
      {children}
    </RNText>
  );
};

/* ── Button ───────────────────────────────────────────────────────────────── */

type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
type ButtonSize = 'sm' | 'md' | 'lg';

const BUTTON_PAD: Record<ButtonSize, { paddingVertical: number; paddingHorizontal: number }> = {
  sm: { paddingVertical: space.xs + 2, paddingHorizontal: space.md },
  md: { paddingVertical: space.md - 2, paddingHorizontal: space.lg },
  lg: { paddingVertical: space.lg - 2, paddingHorizontal: space.xl },
};

const buttonColors = (t: Theme, variant: ButtonVariant) => {
  switch (variant) {
    case 'secondary': return { bg: t.surfaceRaised, fg: t.text, border: t.border };
    case 'ghost': return { bg: 'transparent', fg: t.subtext, border: 'transparent' };
    case 'danger': return { bg: t.danger, fg: t.onDanger, border: t.danger };
    default: return { bg: t.primary, fg: t.onPrimary, border: t.primary };
  }
};

export const Button: React.FC<{
  title: string;
  onPress?: () => void;
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  disabled?: boolean;
  icon?: ReactNode;
  fullWidth?: boolean;
  style?: StyleProp<ViewStyle>;
}> = ({ title, onPress, variant = 'primary', size = 'md', loading, disabled, icon, fullWidth, style }) => {
  const { theme } = useTheme();
  const c = buttonColors(theme, variant);
  const off = disabled || loading;
  return (
    <TouchableOpacity
      onPress={onPress}
      disabled={off}
      accessibilityRole="button"
      style={[
        {
          flexDirection: 'row',
          alignItems: 'center',
          justifyContent: 'center',
          gap: space.sm,
          borderRadius: radius.full,
          borderWidth: 1,
          backgroundColor: c.bg,
          borderColor: c.border,
          opacity: off ? 0.5 : 1,
          alignSelf: fullWidth ? 'stretch' : 'flex-start',
        },
        BUTTON_PAD[size],
        style,
      ]}
    >
      {loading ? <ActivityIndicator size="small" color={c.fg} /> : icon}
      <RNText style={[typeScale.label as TextStyle, { color: c.fg }]}>{title}</RNText>
    </TouchableOpacity>
  );
};

/* ── Card ─────────────────────────────────────────────────────────────────── */

export const Card: React.FC<{
  elevation?: 'flat' | 'raised';
  padding?: keyof typeof space;
  style?: StyleProp<ViewStyle>;
  children?: ReactNode;
}> = ({ elevation = 'flat', padding = 'lg', style, children }) => {
  const { theme } = useTheme();
  return (
    <View
      style={[
        {
          backgroundColor: elevation === 'raised' ? theme.surfaceRaised : theme.surface,
          borderColor: theme.border,
          borderWidth: StyleSheet.hairlineWidth,
          borderRadius: radius.lg,
          padding: space[padding],
        },
        style,
      ]}
    >
      {children}
    </View>
  );
};

/* ── Chip ─────────────────────────────────────────────────────────────────── */

/**
 * Selection pill — the tag filters, the preference rows, the Appearance switch. Selected is the
 * *soft* accent fill rather than a solid one: it was the look nine of the ten hand-rolled chip
 * rows already used before 5.8 folded them in here, and it survives a wrapped multi-select row
 * without turning it into a wall of colour.
 *
 * `trailing` is the one escape hatch — a chip that removes itself (a disliked ingredient) needs
 * its own hit target, which `onPress` on the whole pill cannot give it.
 */
export const Chip: React.FC<{
  label: string;
  selected?: boolean;
  onPress?: () => void;
  onLongPress?: () => void;
  icon?: ReactNode;
  trailing?: ReactNode;
  style?: StyleProp<ViewStyle>;
}> = ({ label, selected, onPress, onLongPress, icon, trailing, style }) => {
  const { theme } = useTheme();
  return (
    <TouchableOpacity
      onPress={onPress}
      onLongPress={onLongPress}
      disabled={!onPress}
      accessibilityRole="button"
      accessibilityState={{ selected: !!selected }}
      style={[
        {
          flexDirection: 'row',
          alignItems: 'center',
          gap: space.xs,
          paddingVertical: space.sm - 2,
          paddingHorizontal: space.md + 2,
          borderRadius: radius.full,
          borderWidth: 1,
          backgroundColor: selected ? theme.accentFaded : 'transparent',
          borderColor: selected ? theme.accent : theme.border,
        },
        style,
      ]}
    >
      {icon}
      <RNText
        style={[
          typeScale.caption as TextStyle,
          { color: selected ? theme.accent : theme.subtext, fontWeight: selected ? '600' : '400' },
        ]}
      >
        {label}
      </RNText>
      {trailing}
    </TouchableOpacity>
  );
};

/* ── Badge ────────────────────────────────────────────────────────────────── */

/**
 * Read-only icon+label pill: a recipe's servings/time/difficulty, a step's timer or oven
 * temperature, a saved recipe's tags. `Chip` is the tappable one; this one never is.
 *
 * DESIGN_SYSTEM §7.4 warns the mockups use roughly eight background tints for these. Two are
 * enough for everything the app actually renders — add a tone here when a screen needs one, never
 * a colour at the call site.
 */
export const Badge: React.FC<{
  label: string;
  icon?: ReactNode;
  tone?: 'accent' | 'neutral' | 'success';
  style?: StyleProp<ViewStyle>;
}> = ({ label, icon, tone = 'accent', style }) => {
  const { theme } = useTheme();
  const neutral = tone === 'neutral';
  const success = tone === 'success';
  return (
    <View
      style={[
        {
          flexDirection: 'row',
          alignItems: 'center',
          gap: space.xs,
          paddingHorizontal: space.sm,
          paddingVertical: space.xs - 1,
          borderRadius: radius.full,
          backgroundColor: neutral ? theme.surfaceRaised : success ? theme.success : theme.accentFaded,
          borderWidth: neutral ? 1 : 0,
          borderColor: theme.border,
        },
        style,
      ]}
    >
      {icon}
      <RNText style={[typeScale.caption as TextStyle, { color: neutral ? theme.subtext : success ? theme.onSuccess : theme.accent, fontWeight: '600' }]}>
        {label}
      </RNText>
    </View>
  );
};

/* ── Screen ───────────────────────────────────────────────────────────────── */

/** Page background plus the reading column. `wide` content fills instead of centring. */
export const Screen: React.FC<{
  padding?: keyof typeof space;
  center?: boolean;
  style?: StyleProp<ViewStyle>;
  children?: ReactNode;
}> = ({ padding = 'lg', center, style, children }) => {
  const { theme } = useTheme();
  return (
    <View style={[{ flex: 1, backgroundColor: theme.bg, padding: space[padding] }, center && { justifyContent: 'center' }, style]}>
      <View style={{ flex: center ? undefined : 1, width: '100%', maxWidth: layout.contentMaxWidth, alignSelf: 'center' }}>
        {children}
      </View>
    </View>
  );
};

/* ── SignInRequired ───────────────────────────────────────────────────────── */

/** The gate every signed-in-only screen shows. `children` holds any extra way out. */
export const SignInRequired: React.FC<{
  message: string;
  onSignIn: () => void;
  children?: ReactNode;
}> = ({ message, onSignIn, children }) => {
  const { t } = useLanguage();
  return (
  <Screen center>
    <View style={{ alignItems: 'center', gap: space.sm }}>
      <Text variant="title">{t('recipes.signInRequired')}</Text>
      <Text tone="subtle" style={{ textAlign: 'center' }}>{message}</Text>
      <Button title={t('nav.login')} onPress={onSignIn} style={{ marginTop: space.md }} />
      {children}
    </View>
  </Screen>
  );
};

/* ── Sheet ────────────────────────────────────────────────────────────────── */

/**
 * Bottom sheet: tap the scrim to dismiss. No drag handle — nothing needs one yet.
 *
 * The sheet body is a `Pressable` with a no-op `onPress` so it becomes the touch responder
 * and the scrim's dismiss never fires for a press *inside* the sheet. Without it, focusing a
 * `TextInput` in a sheet bubbles to the scrim and closes it mid-typing — which is exactly
 * what happened to the report form (BACKLOG 9.16). Do not flatten this back to a plain View.
 */
export const Sheet: React.FC<{
  visible: boolean;
  onClose: () => void;
  title?: string;
  children?: ReactNode;
}> = ({ visible, onClose, title, children }) => {
  const { theme } = useTheme();
  const styles = useMemo(() => makeSheetStyles(theme), [theme]);
  return (
    <Modal visible={visible} transparent animationType="fade" onRequestClose={onClose}>
      <Pressable style={styles.overlay} onPress={onClose} accessibilityLabel="Close">
        <Pressable style={styles.sheet} onPress={() => { /* swallow: see the note above */ }}>
          {title ? <Text variant="label" tone="muted">{title}</Text> : null}
          {children}
        </Pressable>
      </Pressable>
    </Modal>
  );
};

const makeSheetStyles = (t: Theme) => StyleSheet.create({
  overlay: {
    flex: 1,
    backgroundColor: t.overlay,
    justifyContent: 'flex-end',
  },
  sheet: {
    backgroundColor: t.surface,
    borderTopLeftRadius: radius.xl,
    borderTopRightRadius: radius.xl,
    padding: space.xl,
    gap: space.sm,
    width: '100%',
    maxWidth: layout.contentMaxWidth,
    alignSelf: 'center',
  },
});

/** A row of tappable options inside a `Sheet` — the planner's day actions (4.2). */
export const SheetRow: React.FC<{ label: string; onPress: () => void; destructive?: boolean }> = ({ label, onPress, destructive }) => (
  <TouchableOpacity onPress={onPress} accessibilityRole="button" style={{ paddingVertical: space.md }}>
    <Text variant="body" tone={destructive ? 'danger' : 'default'}>{label}</Text>
  </TouchableOpacity>
);
