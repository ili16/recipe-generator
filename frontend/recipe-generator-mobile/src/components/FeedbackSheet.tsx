import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Image, Platform, StyleSheet, TextInput, TouchableOpacity, View } from 'react-native';
import * as ImagePicker from 'expo-image-picker';
import { Ionicons } from '@expo/vector-icons';
import Constants from 'expo-constants';
import { currentLocale } from '../i18n';
import { useLanguage } from '../context/LanguageContext';
import { Button, Chip, Sheet, Text } from './ui';
import { Theme, useTheme } from '../context/ThemeContext';
import { space, radius, type } from '../theme';
import { useAlert } from '../context/AlertContext';
import apiService, { ApiError } from '../services/apiService';
import authService from '../services/authService';
import { FeedbackKind } from '../types';

/**
 * The report form (BACKLOG 9.16). Mounted once in `AppShell`, so every screen shares one
 * instance and the top bar / sidebar triggers open the same thing.
 *
 * It works logged out on purpose: the alpha's testers include people who never sign in, and
 * a bug they hit is worth exactly as much as anyone's. The only thing signing in changes is
 * that we already know who they are, so the email field disappears.
 */
export const FeedbackSheet: React.FC<{
  visible: boolean;
  onClose: () => void;
  /** The screen they were on when they tapped report — the single most useful field here. */
  route?: string;
}> = ({ visible, onClose, route }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const { showAlert } = useAlert();

  const [kind, setKind] = useState<FeedbackKind>('bug');
  const [message, setMessage] = useState('');
  const [contact, setContact] = useState('');
  const [signedIn, setSignedIn] = useState<boolean | null>(null);
  const [sending, setSending] = useState(false);
  /** A data: URL — displayed as the thumbnail AND sent as-is; the server decodes it. */
  const [shot, setShot] = useState<string | null>(null);

  // Read auth once per open rather than subscribing: there is no auth context, and screens
  // in this app re-check on mount (ProfileScreen does the same).
  useEffect(() => {
    if (!visible) return;
    let live = true;
    authService.isAuthenticated()
      .then((yes) => { if (live) setSignedIn(yes); })
      .catch(() => { if (live) setSignedIn(false); });
    return () => { live = false; };
  }, [visible]);

  // Web: paste a screenshot straight in — PrintScreen then Ctrl-V, no file dialog. This is
  // the whole point on the platform the beta runs on.
  //
  // Registered on the CAPTURE phase, and this matters: `chat/Composer.tsx` has its own
  // window-level paste listener that attaches the image to the chat composer, it is mounted
  // before this sheet ever opens, and listeners on the same target fire in registration
  // order. A bubble-phase listener here therefore runs *second* and the image lands in both
  // places — which is exactly what happened. `preventDefault()` does not help; it does not
  // stop other listeners. Capture runs before every bubble listener, so claiming the event
  // here and calling stopPropagation is what actually keeps it out of the composer.
  //
  // Only an image is claimed. A text paste falls straight through to the focused input.
  useEffect(() => {
    if (Platform.OS !== 'web' || !visible) return;
    const onPaste = (e: ClipboardEvent) => {
      const item = Array.from(e.clipboardData?.items ?? []).find(i => i.type.startsWith('image/'));
      const blob = item?.getAsFile();
      if (!blob) return;                      // a text paste belongs to the focused input
      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();
      const reader = new FileReader();
      reader.onload = () => setShot(reader.result as string);
      reader.readAsDataURL(blob);
    };
    window.addEventListener('paste', onPaste, true);
    return () => window.removeEventListener('paste', onPaste, true);
  }, [visible]);

  const pickShot = useCallback(async () => {
    try {
      const perm = await ImagePicker.requestMediaLibraryPermissionsAsync();
      if (!perm.granted) { showAlert(t('feedback.permissionTitle'), t('feedback.permissionBody'), 'error'); return; }
      // base64, because the report travels as JSON like every other write here.
      const result = await ImagePicker.launchImageLibraryAsync({
        mediaTypes: ImagePicker.MediaTypeOptions.Images, quality: 0.7, exif: false, base64: true,
      });
      if (result.canceled || !result.assets[0]?.base64) return;
      const asset = result.assets[0];
      setShot(`data:${asset.mimeType ?? 'image/jpeg'};base64,${asset.base64}`);
    } catch { showAlert(t('common.error'), t('feedback.imageFailed'), 'error'); }
  }, [showAlert]);

  const reset = () => { setKind('bug'); setMessage(''); setContact(''); setShot(null); };

  const close = () => { reset(); onClose(); };

  const submit = async () => {
    const text = message.trim();
    if (!text || sending) return;
    setSending(true);
    try {
      await apiService.submitFeedback({
        kind,
        message: text,
        // Only ever asked of an anonymous reporter, and never required of them.
        contact: signedIn ? undefined : contact.trim() || undefined,
        context: collectContext(route),
        screenshot: shot ?? undefined,
      });
      close();
      showAlert(t('feedback.thanks'), t('feedback.thanksBody'), 'success');
    } catch (e) {
      // Deliberately swallowed into an alert: a failed bug report must not look like a
      // second bug. The message and the image stay put so they can retry without redoing it.
      const tooBig = (e as ApiError)?.status === 413;
      showAlert(
        t(tooBig ? 'feedback.tooBigTitle' : 'feedback.sendFailedTitle'),
        t(tooBig ? 'feedback.tooBigBody' : 'feedback.sendFailedBody'),
        'error',
      );
    } finally {
      setSending(false);
    }
  };

  return (
    <Sheet visible={visible} onClose={close} title={t('feedback.title')}>
      <View style={styles.kindRow}>
        <Chip label={t('feedback.kindBug')} selected={kind === 'bug'} onPress={() => setKind('bug')} />
        <Chip label={t('feedback.kindIdea')} selected={kind === 'idea'} onPress={() => setKind('idea')} />
      </View>

      <TextInput
        style={[styles.input, styles.inputMultiline]}
        placeholder={t(kind === 'bug' ? 'feedback.bugPlaceholder' : 'feedback.ideaPlaceholder')}
        placeholderTextColor={theme.muted}
        value={message}
        onChangeText={setMessage}
        multiline
        autoFocus={Platform.OS === 'web'}
      />

      {signedIn === false ? (
        <TextInput
          style={styles.input}
          placeholder={t('feedback.emailPlaceholder')}
          placeholderTextColor={theme.muted}
          value={contact}
          onChangeText={setContact}
          autoCapitalize="none"
          keyboardType="email-address"
          inputMode="email"
        />
      ) : null}

      {shot ? (
        <View style={styles.shotRow}>
          <Image source={{ uri: shot }} style={styles.shotThumb} resizeMode="cover" />
          <Text variant="caption" tone="muted" style={styles.shotLabel}>{t('feedback.screenshotAttached')}</Text>
          <TouchableOpacity onPress={() => setShot(null)} accessibilityRole="button" accessibilityLabel={t('feedback.removeScreenshot')} hitSlop={8}>
            <Ionicons name="close" size={18} color={theme.muted} />
          </TouchableOpacity>
        </View>
      ) : (
        <TouchableOpacity onPress={pickShot} accessibilityRole="button" style={styles.attachRow}>
          <Ionicons name="image-outline" size={18} color={theme.subtext} />
          <Text variant="caption" tone="muted">
            {t(Platform.OS === 'web' ? 'feedback.addScreenshotWeb' : 'feedback.addScreenshot')}
          </Text>
        </TouchableOpacity>
      )}

      {/* Show the actual values rather than describing them. The first wording said "the
          screen you're on", meaning the route NAME — and the first person to read it took it
          as a screen capture. A form that collects data must never be vague about what it
          collects, so it lists the literal fields and says whether an image is going too. */}
      <Text variant="caption" tone="muted">
        {t('feedback.attachedAutomatically', { summary: attachedSummary(route) })}
      </Text>

      <View style={styles.actions}>
        <Button title={t('common.cancel')} variant="ghost" onPress={close} />
        <Button title={t('feedback.send')} onPress={submit} loading={sending} disabled={!message.trim()} />
      </View>
    </Sheet>
  );
};

/**
 * The same fields as `collectContext`, rendered for the person about to send them. Derived
 * from that function rather than written out separately, so adding a field to one cannot
 * leave the other quietly lying about what is collected.
 */
export function attachedSummary(route?: string): string {
  const c = collectContext(route);
  return [c.route, c.platform, c.version && `v${c.version}`].filter(Boolean).join(' · ');
}

/**
 * Everything a report needs that the reporter should never have to type. Kept to what is
 * free: no device library, no permissions, nothing that could identify them beyond the
 * account they are already signed into.
 */
export function collectContext(route?: string): Record<string, string | number | undefined> {
  return {
    route,
    platform: Platform.OS,
    os_version: String(Platform.Version ?? ''),
    version: Constants.expoConfig?.version ?? '',
    // The language the app is actually in, which is what a report is about — not the OS's.
    locale: currentLocale(),
  };
}

const makeStyles = (t: Theme) => StyleSheet.create({
  kindRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: space.sm,
    marginTop: space.sm,
  },
  input: {
    backgroundColor: t.bg,
    borderWidth: 1,
    borderColor: t.border,
    borderRadius: radius.md,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    ...type.body,
    color: t.text,
    marginTop: space.sm,
  },
  inputMultiline: {
    minHeight: 110,
    textAlignVertical: 'top',
  },
  attachRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space.sm,
    paddingVertical: space.sm,
    marginTop: space.xs,
  },
  shotRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space.sm,
    marginTop: space.sm,
  },
  shotThumb: {
    width: 44,
    height: 44,
    borderRadius: radius.sm,
    borderWidth: 1,
    borderColor: t.border,
    backgroundColor: t.bg,
  },
  shotLabel: {
    flex: 1,
  },
  actions: {
    flexDirection: 'row',
    justifyContent: 'flex-end',
    gap: space.sm,
    marginTop: space.md,
  },
});
