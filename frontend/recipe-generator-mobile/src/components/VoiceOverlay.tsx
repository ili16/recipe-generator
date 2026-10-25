import React, { useMemo } from 'react';
import { View, Text, StyleSheet, TouchableOpacity, Modal, Animated } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { Theme, useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { type } from '../theme';
import { VoiceInput } from '../hooks/useVoiceInput';

const VoiceOverlay: React.FC<{ voice: VoiceInput }> = ({ voice }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  return (
    <Modal visible={voice.isRecording} transparent animationType="fade" onRequestClose={voice.abort} statusBarTranslucent>
      <View style={styles.voiceOverlay}>
        <Text style={styles.voiceTitle}>{t('voice.listening')}</Text>
        <Text style={styles.voiceSubtitle}>{t('voice.subtitle')}</Text>
        <View style={styles.soundbarRow}>
          {voice.soundBars.map((bar, i) => (
            <Animated.View key={i} style={[styles.soundbarBar, {
              transform: [{ scaleY: bar }],
              backgroundColor: i === 2 ? theme.accent : theme.onScrim,
              opacity: bar.interpolate({ inputRange: [0.12, 1], outputRange: [0.4, 1] }),
            }]} />
          ))}
        </View>
        <View style={styles.voiceActions}>
          <TouchableOpacity style={styles.voiceAbortBtn} onPress={voice.abort}><Ionicons name="close" size={26} color={theme.onScrim} /></TouchableOpacity>
          <TouchableOpacity style={styles.voiceConfirmBtn} onPress={voice.toggle}><Ionicons name="checkmark" size={26} color={theme.onAccent} /></TouchableOpacity>
        </View>
        <Text style={styles.voiceHint}>{t('voice.hint')}</Text>
      </View>
    </Modal>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  // The four translucent whites this used are `onScrim` + `opacity`: one token, same result.
  voiceOverlay: { flex: 1, backgroundColor: t.scrim, justifyContent: 'center', alignItems: 'center', gap: 24 },
  voiceTitle: { ...type.display, color: t.onScrim, letterSpacing: -0.5 },
  voiceSubtitle: { ...type.body, color: t.onScrim, opacity: 0.55, marginTop: -16 },
  soundbarRow: { flexDirection: 'row', alignItems: 'center', gap: 6, height: 64 },
  soundbarBar: { width: 6, height: 48, borderRadius: 3 },
  voiceActions: { flexDirection: 'row', gap: 32 },
  voiceAbortBtn: { width: 60, height: 60, borderRadius: 30, borderWidth: 1.5, borderColor: t.onScrim, justifyContent: 'center', alignItems: 'center' },
  voiceConfirmBtn: { width: 60, height: 60, borderRadius: 30, backgroundColor: t.accent, justifyContent: 'center', alignItems: 'center' },
  voiceHint: { ...type.caption, color: t.onScrim, opacity: 0.35, letterSpacing: 0.3 },
});

export default VoiceOverlay;
