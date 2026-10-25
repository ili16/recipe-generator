import { useCallback, useEffect, useRef, useState } from 'react';
import { Animated, Platform } from 'react-native';
import { Audio } from 'expo-av';
import apiService from '../services/apiService';
import { AlertSeverity } from '../context/AlertContext';

interface Options {
  /** Recording started — caller switches to voice mode / expands the box. */
  onStart: () => void;
  /** Recording ended (transcribed, cancelled or failed) — caller returns to text mode. */
  onFinish: () => void;
  /** `replace` is true for live web SpeechRecognition results, false for Whisper output. */
  onTranscript: (text: string, replace: boolean) => void;
  /** Non-null while transcription is in flight. */
  setBusy: (message: string | null) => void;
  showAlert: (title: string, message: string, severity?: AlertSeverity) => void;
}

export interface VoiceInput {
  isRecording: boolean;
  soundBars: Animated.Value[];
  /** Start recording, or stop and transcribe. */
  toggle: () => Promise<void>;
  /** Stop and throw the recording away. */
  abort: () => Promise<void>;
}

/**
 * The three parallel voice implementations, behind one interface:
 * web SpeechRecognition (Chrome/Safari/Edge), web MediaRecorder → /transcribe
 * (Firefox), and native expo-av → /transcribe.
 */
export function useVoiceInput({ onStart, onFinish, onTranscript, setBusy, showAlert }: Options): VoiceInput {
  const [isRecording, setIsRecording] = useState(false);
  const soundBars = useRef(Array.from({ length: 5 }, () => new Animated.Value(0.3))).current;
  const recordingRef = useRef<Audio.Recording | null>(null);
  const speechRecognitionRef = useRef<any>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  // Lets MediaRecorder.onstop distinguish confirm from abort.
  const abortedRef = useRef(false);
  // Cached so a second tap doesn't re-prompt the OS.
  const micGranted = useRef(false);

  // Pre-request on mount so the first tap has no OS-dialog delay.
  useEffect(() => {
    if (Platform.OS === 'web') return;
    Audio.requestPermissionsAsync()
      .then(({ status }) => { micGranted.current = status === 'granted'; })
      .catch(() => {});
  }, []);

  const startBars = useCallback(() => {
    soundBars.forEach((bar, i) => {
      Animated.loop(Animated.sequence([
        Animated.delay(i * 90),
        Animated.timing(bar, { toValue: 1, duration: 280 + i * 40, useNativeDriver: true }),
        Animated.timing(bar, { toValue: 0.12, duration: 280 + i * 40, useNativeDriver: true }),
      ])).start();
    });
  }, [soundBars]);

  const stopBars = useCallback(() => {
    soundBars.forEach(bar => { bar.stopAnimation(); bar.setValue(0.3); });
  }, [soundBars]);

  const transcribe = useCallback(async (run: () => Promise<string>) => {
    setBusy('🎤 Transcribing...');
    try {
      const text = await run();
      if (text.trim()) onTranscript(text.trim(), false);
    } catch { showAlert('Error', 'Failed to transcribe voice', 'error'); }
    finally { setBusy(null); onFinish(); }
  }, [setBusy, onTranscript, onFinish, showAlert]);

  const toggle = useCallback(async () => {
    if (Platform.OS === 'web') {
      const SR = (window as any).SpeechRecognition ?? (window as any).webkitSpeechRecognition;

      if (isRecording) {
        if (SR && speechRecognitionRef.current) {
          speechRecognitionRef.current.stop();
        } else if (mediaRecorderRef.current) {
          abortedRef.current = false;
          setIsRecording(false); stopBars(); // close the overlay immediately
          mediaRecorderRef.current.stop();   // onstop transcribes
        }
        return;
      }

      if (SR) {
        const recognition = new SR();
        recognition.continuous = false;
        recognition.interimResults = true;
        recognition.onresult = (e: any) => {
          onTranscript(Array.from(e.results as any[]).map((r: any) => r[0].transcript).join(''), true);
        };
        recognition.onend = () => {
          setIsRecording(false); stopBars(); speechRecognitionRef.current = null; onFinish();
        };
        recognition.onerror = () => {
          showAlert('Error', 'Speech recognition failed', 'error');
          setIsRecording(false); stopBars(); speechRecognitionRef.current = null; onFinish();
        };
        speechRecognitionRef.current = recognition;
        recognition.start();
        setIsRecording(true); startBars(); onStart();
        return;
      }

      // Firefox: record ourselves and let the backend transcribe.
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        const mimeType = MediaRecorder.isTypeSupported('audio/webm;codecs=opus')
          ? 'audio/webm;codecs=opus'
          : MediaRecorder.isTypeSupported('audio/ogg;codecs=opus')
          ? 'audio/ogg;codecs=opus'
          : 'audio/webm';
        const ext = mimeType.startsWith('audio/ogg') ? 'ogg' : 'webm';
        audioChunksRef.current = [];
        const recorder = new MediaRecorder(stream, { mimeType });
        recorder.ondataavailable = e => { if (e.data.size > 0) audioChunksRef.current.push(e.data); };
        recorder.onstop = async () => {
          stream.getTracks().forEach(t => t.stop());
          mediaRecorderRef.current = null;
          setIsRecording(false); stopBars();
          if (abortedRef.current) { abortedRef.current = false; onFinish(); return; }
          const blob = new Blob(audioChunksRef.current, { type: mimeType });
          await transcribe(() => apiService.transcribeBlob(blob, `recording.${ext}`));
        };
        mediaRecorderRef.current = recorder;
        recorder.start();
        setIsRecording(true); startBars(); onStart();
      } catch { showAlert('Error', 'Microphone access denied', 'error'); }
      return;
    }

    // ── Native ──
    if (isRecording) {
      try {
        setIsRecording(false); stopBars();
        await recordingRef.current?.stopAndUnloadAsync();
        const uri = recordingRef.current?.getURI();
        recordingRef.current = null;
        await Audio.setAudioModeAsync({ allowsRecordingIOS: false });
        if (uri) await transcribe(() => apiService.transcribeAudio(uri));
        else onFinish();
      } catch {
        showAlert('Error', 'Failed to stop recording', 'error');
        setIsRecording(false); stopBars(); recordingRef.current = null; onFinish();
      }
      return;
    }
    try {
      if (!micGranted.current) {
        const { status } = await Audio.requestPermissionsAsync();
        if (status !== 'granted') { showAlert('Permission Required', 'Please grant microphone access', 'error'); return; }
        micGranted.current = true;
      }
      await Audio.setAudioModeAsync({ allowsRecordingIOS: true, playsInSilentModeIOS: true });
      const { recording } = await Audio.Recording.createAsync(Audio.RecordingOptionsPresets.HIGH_QUALITY);
      recordingRef.current = recording; setIsRecording(true); startBars(); onStart();
    } catch { showAlert('Error', 'Failed to start recording', 'error'); }
  }, [isRecording, startBars, stopBars, transcribe, onStart, onFinish, onTranscript, showAlert]);

  const abort = useCallback(async () => {
    if (Platform.OS === 'web') {
      speechRecognitionRef.current?.abort();
      speechRecognitionRef.current = null;
      if (mediaRecorderRef.current) {
        abortedRef.current = true; // stop onstop from transcribing
        mediaRecorderRef.current.stop();
        mediaRecorderRef.current = null;
      }
      setIsRecording(false); stopBars(); onFinish();
      return;
    }
    try {
      setIsRecording(false); stopBars();
      await recordingRef.current?.stopAndUnloadAsync();
      recordingRef.current = null;
      await Audio.setAudioModeAsync({ allowsRecordingIOS: false });
    } catch { recordingRef.current = null; }
    finally { setIsRecording(false); onFinish(); }
  }, [stopBars, onFinish]);

  return { isRecording, soundBars, toggle, abort };
}
