import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { View, ScrollView, TextInput, TouchableOpacity, ActivityIndicator, StyleSheet } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { Screen, SignInRequired, Text } from '../../components/ui';
import { Theme, useTheme } from '../../context/ThemeContext';
import { space, type } from '../../theme';
import { useLanguage } from '../../context/LanguageContext';
import { useIsAuthenticated } from '../../hooks/useIsAuthenticated';
import { useAlert } from '../../context/AlertContext';
import { currentLocale } from '../../i18n';
import apiService from '../../services/apiService';
import { ConversationSummary } from '../../types';
import { groupThreads, matchesQuery } from '../../utils/threadGroups';
import { RootStackParamList } from '../../navigation/AppNavigator';

// The conversation list (BACKLOG.md 10.4, opened up by 10.7). Threads have been in
// Postgres since 2.1 with no way to get back to one; 10.4 gave them a sheet behind a
// button on the chat screen, which testers did not find. It is a screen now, reachable
// from the shell, grouped by day, searchable, and each row can be renamed or deleted.
//
// Loaded each time it opens rather than cached — it is a list of at most a hundred rows,
// read once per opening, and a stale "yesterday you were planning next week" is worse
// than a spinner.
type Props = NativeStackScreenProps<RootStackParamList, 'History'>;

const ThreadListScreen: React.FC<Props> = ({ navigation }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const isAuthenticated = useIsAuthenticated();
  const { confirmAction, showAlert } = useAlert();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [threads, setThreads] = useState<ConversationSummary[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [query, setQuery] = useState('');
  // The thread whose title is being edited, and the text so far. Renaming in place rather
  // than in a dialog: there is no prompt primitive, and the row already shows the title.
  const [editing, setEditing] = useState<{ id: string; title: string } | null>(null);

  useEffect(() => {
    if (!isAuthenticated) return;
    apiService.listConversations().then(setThreads).catch(() => setFailed(true));
  }, [isAuthenticated]);

  // Opening a thread resets the stack instead of pushing Chat onto it: this screen is
  // reached from the rail (which resets) and from the chat screen (which pushes), and
  // either way what the user wants afterwards is the conversation, with nothing behind it.
  const open = (id: string) =>
    navigation.reset({ index: 0, routes: [{ name: 'Chat', params: { threadId: id } }] });

  const rename = useCallback(async (id: string, title: string) => {
    setEditing(null);
    const previous = threads?.find((c) => c.id === id)?.title ?? '';
    if (title.trim() === previous) return;
    // Optimistic: the row shows the new name at once and falls back to the old one if the
    // write fails, rather than blanking while a PATCH is in flight.
    setThreads((prev) => prev?.map((c) => (c.id === id ? { ...c, title: title.trim() } : c)) ?? prev);
    try {
      const stored = await apiService.renameConversation(id, title);
      setThreads((prev) => prev?.map((c) => (c.id === id ? { ...c, title: stored } : c)) ?? prev);
    } catch {
      setThreads((prev) => prev?.map((c) => (c.id === id ? { ...c, title: previous } : c)) ?? prev);
      showAlert(t('chat.history.renameFailed'), undefined, 'error');
    }
  }, [threads, showAlert, t]);

  const remove = useCallback(async (c: ConversationSummary) => {
    const ok = await confirmAction(
      t('chat.history.deleteTitle'),
      t('chat.history.deleteBody', { title: c.title || c.preview }),
      { confirmLabel: t('common.delete'), destructive: true },
    );
    if (!ok) return;
    try {
      await apiService.deleteConversation(c.id);
      setThreads((prev) => prev?.filter((x) => x.id !== c.id) ?? prev);
    } catch {
      showAlert(t('chat.history.deleteFailed'), undefined, 'error');
    }
  }, [confirmAction, showAlert, t]);

  const groups = useMemo(
    () => groupThreads((threads ?? []).filter((c) => matchesQuery(c, query)), {
      now: new Date(),
      locale: currentLocale(),
      today: t('chat.history.today'),
      yesterday: t('chat.history.yesterday'),
    }),
    [threads, query, t],
  );

  if (!isAuthenticated) {
    return <SignInRequired message={t('chat.history.signIn')} onSignIn={() => navigation.navigate('Login')} />;
  }

  return (
    <Screen>
      {threads === null && !failed && <ActivityIndicator color={theme.accent} style={styles.spinner} />}
      {failed && <Text tone="muted">{t('common.unknownError')}</Text>}
      {threads?.length === 0 && <Text tone="muted">{t('chat.history.empty')}</Text>}

      {/* Search appears once there is enough of a list to be worth searching. */}
      {(threads?.length ?? 0) > 5 && (
        <TextInput
          autoComplete="off"
          style={styles.search}
          placeholder={t('chat.history.searchPlaceholder')}
          placeholderTextColor={theme.muted}
          value={query}
          onChangeText={setQuery}
        />
      )}
      {threads !== null && threads.length > 0 && groups.length === 0 && (
        <Text tone="muted">{t('chat.history.noMatches')}</Text>
      )}

      <ScrollView>
        {groups.map((g) => (
          <View key={g.label}>
            <Text variant="label" tone="muted" style={styles.groupLabel}>{g.label}</Text>
            {g.threads.map((c) => (
              <View key={c.id} style={styles.row}>
                {editing?.id === c.id ? (
                  <TextInput
                    autoComplete="off"
                    autoFocus
                    style={styles.rename}
                    value={editing.title}
                    onChangeText={(title) => setEditing({ id: c.id, title })}
                    onSubmitEditing={() => rename(c.id, editing.title)}
                    onBlur={() => rename(c.id, editing.title)}
                    returnKeyType="done"
                    accessibilityLabel={t('chat.history.rename')}
                  />
                ) : (
                  <TouchableOpacity style={styles.rowBody} onPress={() => open(c.id)} accessibilityRole="button">
                    {/* An untitled thread is one whose naming call has not landed (or failed);
                        its own last message says more than a placeholder would. */}
                    <Text numberOfLines={1} style={styles.title}>{c.title || c.preview}</Text>
                    <Text variant="caption" tone="muted" numberOfLines={1}>
                      {new Date(c.updated_at).toLocaleTimeString(currentLocale(), { hour: '2-digit', minute: '2-digit' })}
                      {c.title ? ` · ${c.preview}` : ''}
                    </Text>
                  </TouchableOpacity>
                )}
                <TouchableOpacity
                  style={styles.action}
                  onPress={() => setEditing({ id: c.id, title: c.title })}
                  hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                  accessibilityRole="button"
                  accessibilityLabel={t('chat.history.rename')}
                >
                  <Ionicons name="pencil-outline" size={18} color={theme.subtext} />
                </TouchableOpacity>
                <TouchableOpacity
                  style={styles.action}
                  onPress={() => remove(c)}
                  hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                  accessibilityRole="button"
                  accessibilityLabel={t('common.delete')}
                >
                  <Ionicons name="trash-outline" size={18} color={theme.danger} />
                </TouchableOpacity>
              </View>
            ))}
          </View>
        ))}
      </ScrollView>
    </Screen>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  spinner: { paddingVertical: space.lg },
  search: {
    backgroundColor: t.surface,
    borderWidth: 1,
    borderColor: t.border,
    borderRadius: 10,
    paddingHorizontal: 14,
    paddingVertical: 10,
    marginBottom: space.sm,
    ...type.body, fontSize: 14,
    color: t.text,
  },
  groupLabel: { marginTop: space.md, marginBottom: space.xs },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space.sm,
    paddingVertical: space.sm,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: t.border,
  },
  rowBody: { flex: 1, gap: 2 },
  rename: {
    flex: 1,
    ...type.body, fontSize: 15,
    color: t.text,
    borderBottomWidth: 1,
    borderBottomColor: t.accent,
    paddingVertical: 2,
  },
  title: { ...type.body, fontSize: 15, color: t.text },
  action: { width: 32, alignItems: 'center' },
});

export default ThreadListScreen;
