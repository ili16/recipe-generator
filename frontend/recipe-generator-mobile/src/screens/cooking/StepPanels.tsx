import React, { useMemo, useState } from 'react';
import { View, Text, StyleSheet, TouchableOpacity, TextInput, ActivityIndicator } from 'react-native';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import { makeChromeStyles } from './styles';
import { isOffline } from '../../utils/offlineRecipes';

// The two panels that hang off a cooking step: the note the user takes, and a
// question to the AI about this step.

export const NotePanel: React.FC<{
  value: string;
  onChange: (text: string) => void;
  onSave: () => void;
  onCancel: () => void;
}> = ({ value, onChange, onSave, onCancel }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  return (
    <View style={styles.noteInputContainer}>
      <Text style={styles.noteInputLabel}>📝 Add a note</Text>
      <TextInput autoComplete="off"
        style={styles.noteInputField}
        value={value}
        onChangeText={onChange}
        placeholder={t('cooking.notePlaceholder')}
        placeholderTextColor={theme.muted}
        multiline
        autoFocus
      />
      <View style={styles.noteInputActions}>
        <TouchableOpacity style={styles.noteSaveBtn} onPress={onSave}>
          <Text style={styles.noteSaveBtnText}>Save</Text>
        </TouchableOpacity>
        <TouchableOpacity style={styles.noteCancelBtn} onPress={onCancel}>
          <Text style={styles.noteCancelBtnText}>Cancel</Text>
        </TouchableOpacity>
      </View>
    </View>
  );
};

export const SavedNote: React.FC<{ note: string; onEdit: () => void }> = ({ note, onEdit }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  return (
    <TouchableOpacity style={styles.savedNote} onPress={onEdit}>
      <Text style={styles.savedNoteLabel}>📝 Your note (tap to edit)</Text>
      <Text style={styles.savedNoteText}>{note}</Text>
    </TouchableOpacity>
  );
};

// Questions go to the cooking session's agent thread (BACKLOG 3.13), so each one can
// follow up on the last — which is why the whole exchange stays on screen instead of a
// single answer being replaced by the next.
export const AskAiPanel: React.FC<{
  onAsk: (question: string, stepText?: string) => Promise<string>;
  stepText?: string;
}> = ({ onAsk, stepText }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const c = useMemo(() => makeChromeStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [question, setQuestion] = useState('');
  const [exchange, setExchange] = useState<{ q: string; a: string }[]>([]);
  const [loading, setLoading] = useState(false);

  const ask = async () => {
    const q = question.trim();
    if (!q || loading) return;
    setLoading(true);
    setQuestion('');
    try {
      const reply = await onAsk(q, stepText);
      setExchange(prev => [...prev, { q, a: reply || t('cooking.noAnswer') }]);
    } catch (err) {
      // BACKLOG 12.4: cooking mode works without a network, Ask AI cannot — and a cook
      // with wet hands should not have to guess which.
      setExchange(prev => [...prev, { q, a: t(isOffline(err) ? 'cooking.offlineAi' : 'cooking.askFailed') }]);
    } finally {
      setLoading(false);
    }
  };

  return (
    <View style={styles.aiContainer}>
      <Text style={styles.aiLabel}>✨ Ask AI</Text>
      <View style={styles.aiInputRow}>
        <TextInput autoComplete="off"
          style={styles.aiInputField}
          value={question}
          onChangeText={setQuestion}
          placeholder={t('cooking.askPlaceholder')}
          placeholderTextColor={theme.muted}
          onSubmitEditing={ask}
          returnKeyType="send"
          editable={!loading}
        />
        <TouchableOpacity
          style={[styles.aiSendBtn, (loading || !question.trim()) && c.btnDisabled]}
          onPress={ask}
          disabled={loading || !question.trim()}
        >
          {loading ? (
            <ActivityIndicator size="small" color={theme.onAccent} />
          ) : (
            <Text style={styles.aiSendBtnText}>Ask</Text>
          )}
        </TouchableOpacity>
      </View>
      {exchange.map(({ q, a }, i) => (
        <View key={i} style={styles.aiAnswer}>
          <Text style={styles.aiQuestionText}>{q}</Text>
          <Text style={styles.aiAnswerText}>{a}</Text>
        </View>
      ))}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  noteInputContainer: {
    backgroundColor: t.surface,
    borderRadius: 12,
    padding: 14,
    marginTop: 16,
    borderWidth: 1,
    borderColor: t.border,
  },
  noteInputLabel: {
    ...type.label, fontSize: 13,
    color: t.text,
    marginBottom: 8,
  },
  noteInputField: {
    ...type.body,
    color: t.text,
    minHeight: 60,
    textAlignVertical: 'top',
  },
  noteInputActions: {
    flexDirection: 'row',
    justifyContent: 'flex-end',
    gap: 10,
    marginTop: 10,
  },
  noteSaveBtn: {
    backgroundColor: t.accent,
    paddingHorizontal: 20,
    paddingVertical: 8,
    borderRadius: 8,
  },
  noteSaveBtnText: {
    color: t.onAccent,
    ...type.label,
  },
  noteCancelBtn: {
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  noteCancelBtnText: {
    color: t.muted,
    ...type.body, fontSize: 14,
  },
  savedNote: {
    backgroundColor: t.surface,
    borderRadius: 12,
    padding: 14,
    marginTop: 16,
    borderLeftWidth: 3,
    borderLeftColor: t.accent,
  },
  savedNoteLabel: {
    ...type.label, fontSize: 12,
    color: t.accent,
    marginBottom: 4,
  },
  savedNoteText: {
    ...type.body, fontSize: 14,
    color: t.text,
    lineHeight: 20,
  },
  aiContainer: {
    backgroundColor: t.surface,
    borderRadius: 12,
    padding: 14,
    marginTop: 16,
    borderWidth: 1,
    borderColor: t.border,
  },
  aiLabel: {
    ...type.label, fontSize: 13,
    color: t.text,
    marginBottom: 10,
  },
  aiInputRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  aiInputField: {
    flex: 1,
    backgroundColor: t.bg,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 10,
    ...type.body,
    color: t.text,
    borderWidth: 1,
    borderColor: t.border,
  },
  aiSendBtn: {
    backgroundColor: t.accent,
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderRadius: 10,
    minWidth: 56,
    alignItems: 'center',
  },
  aiSendBtnText: {
    color: t.onAccent,
    ...type.label,
  },
  aiAnswer: {
    marginTop: 12,
    backgroundColor: t.accentFaded,
    borderRadius: 10,
    padding: 12,
  },
  aiQuestionText: {
    ...type.label, fontSize: 13,
    color: t.muted,
    marginBottom: 6,
  },
  aiAnswerText: {
    ...type.body,
    color: t.text,
    lineHeight: 22,
  },
});
