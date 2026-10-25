import React, { useEffect, useMemo, useState } from 'react';
import { StyleSheet, TextInput, TouchableOpacity, View } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { Button, Card, Text } from './ui';
import { Theme, useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { space, radius, type } from '../theme';
import apiService from '../services/apiService';
import { collectContext } from './FeedbackSheet';
import { shouldAsk } from '../utils/feedbackPrompt';
import { readPromptState, recordAsked, recordAnswered, recordOptOut } from '../utils/feedbackPromptStore';

/**
 * The occasional "how is it going" card (BACKLOG 9.16), rendered inline at the top of the
 * chat screen — never a modal, and never mid-task.
 *
 * The thumb posts *immediately*, before the optional text box appears. That ordering is the
 * whole design: a one-tap question answers at ~93% where a form answers at ~25%, and banking
 * the tap first means someone who abandons the follow-up still counted.
 */
export const FeedbackPulse: React.FC<{ route?: string }> = ({ route }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  const [visible, setVisible] = useState(false);
  const [sentiment, setSentiment] = useState<1 | -1 | null>(null);
  const [note, setNote] = useState('');
  const [done, setDone] = useState(false);

  // Decide once per mount. recordAsked() spends one of the three asks and starts the
  // 14-day clock, so it fires here — the moment it is actually shown — not on submit.
  useEffect(() => {
    let live = true;
    readPromptState().then((s) => {
      if (!live || !shouldAsk(s, Date.now())) return;
      setVisible(true);
      recordAsked();
    }).catch(() => { /* no storage, no prompt — never block the screen on this */ });
    return () => { live = false; };
  }, []);

  if (!visible) return null;

  const answer = (value: 1 | -1) => {
    setSentiment(value);
    recordAnswered();
    // Fire and forget: the thumb is banked here so an abandoned follow-up still counts.
    apiService.submitFeedback({ kind: 'pulse', sentiment: value, context: collectContext(route) })
      .catch(() => { /* a lost pulse is not worth an error dialog */ });
  };

  const sendNote = () => {
    const text = note.trim();
    if (text) {
      apiService.submitFeedback({
        kind: sentiment === -1 ? 'bug' : 'idea',
        message: text,
        context: collectContext(route),
      }).catch(() => {});
    }
    setDone(true);
  };

  const dismiss = () => setVisible(false);

  const optOut = () => { recordOptOut(); setVisible(false); };

  return (
    <Card style={styles.card}>
      <View style={styles.header}>
        <Text variant="label" style={styles.headerText}>
          {t(done ? 'pulse.thanks'
            : sentiment === null ? 'pulse.question'
            : sentiment === 1 ? 'pulse.positive'
            : 'pulse.negative')}
        </Text>
        <TouchableOpacity onPress={dismiss} accessibilityRole="button" accessibilityLabel={t('pulse.dismiss')} hitSlop={8}>
          <Ionicons name="close" size={18} color={theme.muted} />
        </TouchableOpacity>
      </View>

      {done ? null : sentiment === null ? (
        <View style={styles.thumbs}>
          <TouchableOpacity onPress={() => answer(1)} accessibilityRole="button" accessibilityLabel={t('pulse.goingWell')} style={styles.thumb}>
            <Ionicons name="thumbs-up-outline" size={22} color={theme.text} />
          </TouchableOpacity>
          <TouchableOpacity onPress={() => answer(-1)} accessibilityRole="button" accessibilityLabel={t('pulse.notGoingWell')} style={styles.thumb}>
            <Ionicons name="thumbs-down-outline" size={22} color={theme.text} />
          </TouchableOpacity>
          <TouchableOpacity onPress={optOut} accessibilityRole="button" style={styles.optOut}>
            <Text variant="caption" tone="muted">{t('pulse.dontAskAgain')}</Text>
          </TouchableOpacity>
        </View>
      ) : (
        <View style={styles.followUp}>
          <TextInput
            style={styles.input}
            placeholder={t('pulse.notePlaceholder')}
            placeholderTextColor={theme.muted}
            value={note}
            onChangeText={setNote}
            multiline
          />
          <Button title={t(note.trim() ? 'feedback.send' : 'pulse.noThanks')} size="sm" variant={note.trim() ? 'primary' : 'ghost'} onPress={sendNote} />
        </View>
      )}
    </Card>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  card: {
    marginBottom: space.md,
    gap: space.sm,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: space.sm,
  },
  headerText: {
    flex: 1,
  },
  thumbs: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: space.sm,
  },
  thumb: {
    paddingVertical: space.sm,
    paddingHorizontal: space.lg,
    borderWidth: 1,
    borderColor: t.border,
    borderRadius: radius.full,
  },
  optOut: {
    marginLeft: 'auto',
    paddingVertical: space.sm,
  },
  followUp: {
    gap: space.sm,
    alignItems: 'flex-start',
  },
  input: {
    alignSelf: 'stretch',
    backgroundColor: t.bg,
    borderWidth: 1,
    borderColor: t.border,
    borderRadius: radius.md,
    paddingHorizontal: space.md,
    paddingVertical: space.sm,
    minHeight: 44,
    textAlignVertical: 'top',
    ...type.body,
    color: t.text,
  },
});
