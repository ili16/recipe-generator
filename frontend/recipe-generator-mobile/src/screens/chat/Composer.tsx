import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  View, TextInput, TouchableOpacity, ActivityIndicator, Image, Platform, StyleSheet,
} from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import * as ImagePicker from 'expo-image-picker';
import { Theme, useTheme } from '../../context/ThemeContext';
import { type } from '../../theme';
import { useAlert } from '../../context/AlertContext';
import { useLanguage } from '../../context/LanguageContext';
import { useVoiceInput } from '../../hooks/useVoiceInput';
import { ChatAttachment } from '../../types';
import VoiceOverlay from '../../components/VoiceOverlay';

// The one creation surface (BACKLOG.md 3.6): text, photo and voice are attachments on this
// composer, not four modes of a screen. A URL needs no affordance — it is typed or pasted as
// text, and chat_agent_system.txt tells the agent when a link is a recipe to extract.

// Matches the server's maxChatImages.
const MAX_IMAGES = 3;

/** A picked photo: `data` goes to the server, `uri` renders the thumbnail. */
export interface PickedImage {
  uri: string;
  data: string;
}

interface Props {
  sending: boolean;
  onSend: (text: string, attachments: ChatAttachment[]) => void;
  /** Aborts the turn in flight (BACKLOG.md 10.1). Only reachable while `sending`. */
  onStop: () => void;
}

const Composer: React.FC<Props> = ({ sending, onSend, onStop }) => {
  const { theme } = useTheme();
  const { showAlert } = useAlert();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  const [input, setInput] = useState('');
  const [images, setImages] = useState<PickedImage[]>([]);
  const [transcribing, setTranscribing] = useState(false);

  const addImage = useCallback((image: PickedImage) => {
    setImages(prev => (prev.length >= MAX_IMAGES ? prev : [...prev, image]));
  }, []);

  const voice = useVoiceInput({
    onStart: () => {},
    onFinish: () => {},
    onTranscript: (text, replace) =>
      setInput(prev => (replace || !prev.trim() ? text : prev.trimEnd() + ' ' + text)),
    setBusy: message => setTranscribing(message !== null),
    showAlert,
  });

  // Web: pasting a photo attaches it, the same gesture that used to switch GenerateScreen
  // into image mode.
  useEffect(() => {
    if (Platform.OS !== 'web') return;
    const onPaste = (e: ClipboardEvent) => {
      const item = Array.from(e.clipboardData?.items ?? []).find(i => i.type.startsWith('image/'));
      const blob = item?.getAsFile();
      if (!blob) return;
      e.preventDefault();
      const reader = new FileReader();
      reader.onload = () => {
        const dataUrl = reader.result as string;
        addImage({ uri: dataUrl, data: dataUrl });
      };
      reader.readAsDataURL(blob);
    };
    window.addEventListener('paste', onPaste);
    return () => window.removeEventListener('paste', onPaste);
  }, [addImage]);

  const pickImage = async () => {
    if (images.length >= MAX_IMAGES) { showAlert(t('composer.tooManyTitle'), t('composer.tooManyBody', { count: MAX_IMAGES })); return; }
    try {
      const perm = await ImagePicker.requestMediaLibraryPermissionsAsync();
      if (!perm.granted) { showAlert('Permission required', 'Please grant photo library access.', 'error'); return; }
      // base64 is why this asks the picker rather than reading the file itself: /chat is
      // JSON, so the bytes have to travel inline.
      const result = await ImagePicker.launchImageLibraryAsync({
        mediaTypes: ImagePicker.MediaTypeOptions.Images, quality: 0.85, exif: false, base64: true,
      });
      if (result.canceled || !result.assets[0]?.base64) return;
      const asset = result.assets[0];
      addImage({ uri: asset.uri, data: `data:${asset.mimeType ?? 'image/jpeg'};base64,${asset.base64}` });
    } catch { showAlert(t('common.error'), t('composer.photoFailed'), 'error'); }
  };

  const submit = () => {
    const text = input.trim();
    // A photo on its own is a complete ask; the agent is told what the ref means.
    if ((!text && images.length === 0) || sending) return;
    onSend(text || t('composer.photoFallbackPrompt'), images.map(i => ({ type: 'image', data: i.data })));
    setInput('');
    setImages([]);
  };

  const canSend = (!!input.trim() || images.length > 0) && !sending;

  return (
    <View style={styles.wrap}>
      {/* One box: the text owns the full width on its own line, the buttons sit under it. A row
          of icons beside the field cost it ~90px of the screen and clipped what you were typing. */}
      <View style={styles.box}>
        {images.length > 0 && (
          <View style={styles.attachments}>
            {images.map((image, i) => (
              <View key={i} style={styles.thumbWrap}>
                <Image source={{ uri: image.uri }} style={styles.thumb} />
                <TouchableOpacity
                  style={styles.thumbRemove}
                  accessibilityLabel={t('composer.removePhoto')}
                  onPress={() => setImages(prev => prev.filter((_, j) => j !== i))}
                >
                  <Ionicons name="close" size={13} color={theme.onAccent} />
                </TouchableOpacity>
              </View>
            ))}
          </View>
        )}

        <TextInput
          style={styles.input}
          value={input}
          onChangeText={setInput}
          placeholder={t('composer.placeholder')}
          placeholderTextColor={theme.muted}
          multiline
          // Without these, Safari/iOS offers passwords, cards and addresses over a prose field.
          autoComplete="off"
          textContentType="none"
          onSubmitEditing={submit}
          blurOnSubmit={false}
          onKeyPress={(e) => {
            // multiline TextInput on web never fires onSubmitEditing; native does.
            const ne = e.nativeEvent as unknown as { key: string; shiftKey?: boolean };
            if (Platform.OS === 'web' && ne.key === 'Enter' && !ne.shiftKey) {
              e.preventDefault();
              submit();
            }
          }}
        />

        <View style={styles.actions}>
          <TouchableOpacity style={styles.iconBtn} onPress={pickImage} accessibilityLabel="Attach a photo">
            <Ionicons name="image-outline" size={20} color={theme.subtext} />
          </TouchableOpacity>
          <TouchableOpacity style={styles.iconBtn} onPress={voice.toggle} accessibilityLabel="Dictate">
            {transcribing
              ? <ActivityIndicator size="small" color={theme.subtext} />
              : <Ionicons name="mic-outline" size={20} color={voice.isRecording ? theme.accent : theme.subtext} />}
          </TouchableOpacity>

          <View style={styles.spacer} />

          {/* One button, two jobs: while a turn runs it stops it rather than sitting
              there as a spinner you cannot press (BACKLOG.md 10.1). */}
          <TouchableOpacity
            style={[styles.sendBtn, !sending && !canSend && styles.sendBtnOff]}
            onPress={sending ? onStop : submit}
            disabled={!sending && !canSend}
            accessibilityLabel={sending ? t('composer.stop') : t('composer.send')}
          >
            <Ionicons name={sending ? 'square' : 'arrow-up'} size={sending ? 13 : 18} color={theme.onAccent} />
          </TouchableOpacity>
        </View>
      </View>

      <VoiceOverlay voice={voice} />
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  wrap: { paddingHorizontal: 10, paddingVertical: 8, backgroundColor: t.bg },
  box: { backgroundColor: t.surfaceRaised, borderWidth: 1, borderColor: t.border, borderRadius: 20, paddingHorizontal: 12, paddingTop: 8, paddingBottom: 6, gap: 4 },

  attachments: { flexDirection: 'row', gap: 8, paddingBottom: 4 },
  thumbWrap: { width: 56, height: 56 },
  thumb: { width: 56, height: 56, borderRadius: 10, borderWidth: 1, borderColor: t.border },
  thumbRemove: { position: 'absolute', top: -6, right: -6, width: 20, height: 20, borderRadius: 10, backgroundColor: t.accent, alignItems: 'center', justifyContent: 'center' },

  // No background or border of its own: the box around it is the field.
  input: { maxHeight: 160, minHeight: 24, color: t.text, ...type.body, paddingHorizontal: 2, paddingVertical: 0, ...(Platform.OS === 'web' ? { outlineStyle: 'none' } as object : null) },

  actions: { flexDirection: 'row', alignItems: 'center', gap: 4 },
  spacer: { flex: 1 },
  iconBtn: { width: 30, height: 30, borderRadius: 17, alignItems: 'center', justifyContent: 'center' },
  sendBtn: { width: 34, height: 34, borderRadius: 17, backgroundColor: t.accent, alignItems: 'center', justifyContent: 'center' },
  sendBtnOff: { opacity: 0.4 },

});

export default Composer;
