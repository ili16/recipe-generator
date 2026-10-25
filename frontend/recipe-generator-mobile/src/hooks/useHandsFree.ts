import { useCallback, useEffect, useRef, useState } from 'react';
import { Platform } from 'react-native';
import * as Speech from 'expo-speech';

/**
 * Hands-free cooking: the app reads the current card aloud and takes "next" by voice, for
 * the half of cooking you do with wet hands.
 *
 * Two halves, deliberately independent, because only one of them works everywhere:
 *
 * - **Speaking** is `expo-speech`, which is SpeechSynthesis on web and the platform TTS on
 *   native. It works everywhere, so it is the half that always runs.
 * - **Listening** is the browser's `SpeechRecognition` in continuous mode — the same API
 *   `useVoiceInput` already drives for dictation, in its other mode. There is no native
 *   equivalent without a new native dependency, so on native (and on a browser without it)
 *   hands-free is read-aloud plus the tap-anywhere target the screen provides; the toggle
 *   still does something useful, it just does not listen. `listening` says which you got.
 *
 * The screen is kept awake for as long as the mode is on: a phone that sleeps mid-simmer
 * is exactly the thing this mode exists to avoid.
 */

interface Options {
  /** Read aloud whenever this changes (i.e. the card the cook is on). */
  text: string;
  /** ISO 639-1 code of the recipe, so the voice matches the language it's reading. */
  language?: string;
  onNext: () => void;
  onPrev: () => void;
}

export interface HandsFree {
  enabled: boolean;
  toggle: () => void;
  /** True when voice commands are actually being listened for (web only, today). */
  listening: boolean;
  /** Read the current card again — the "repeat" command, and a tap target. */
  repeat: () => void;
}

// What counts as each command, in the languages the app generates recipes in. Matched as
// whole words against the tail of the transcript, so "the next thing" does not fire twice.
const COMMANDS: Array<{ run: 'next' | 'prev' | 'repeat' | 'stop'; words: string[] }> = [
  { run: 'next', words: ['next', 'continue', 'weiter', 'nächster', 'nachster', 'siguiente', 'suivant', 'avanti'] },
  { run: 'prev', words: ['back', 'previous', 'zurück', 'zuruck', 'atrás', 'atras', 'retour', 'indietro'] },
  { run: 'repeat', words: ['repeat', 'again', 'wiederholen', 'nochmal', 'repetir', 'répète', 'repete', 'ripeti'] },
  { run: 'stop', words: ['stop', 'quiet', 'stopp', 'ruhe', 'para', 'arrête', 'arrete', 'basta'] },
];

function matchCommand(transcript: string): 'next' | 'prev' | 'repeat' | 'stop' | null {
  // Only the last few words: continuous recognition keeps handing back the whole utterance,
  // and an earlier "next" must not re-fire every time a later word lands.
  const tail = transcript.toLowerCase().replace(/[^\p{L}\s]/gu, ' ').trim().split(/\s+/).slice(-3);
  for (const word of tail.reverse()) {
    const hit = COMMANDS.find(c => c.words.includes(word));
    if (hit) return hit.run;
  }
  return null;
}

export function useHandsFree({ text, language, onNext, onPrev }: Options): HandsFree {
  const [enabled, setEnabled] = useState(false);
  const [listening, setListening] = useState(false);
  const recognitionRef = useRef<any>(null);
  // Commands arrive from a long-lived recognition callback; routing them through a ref
  // keeps that callback from capturing the first render's handlers forever.
  const handlers = useRef({ onNext, onPrev, text, language });
  handlers.current = { onNext, onPrev, text, language };
  // The last utterance we acted on, so one spoken "next" is one step, not a run of them.
  const lastCommand = useRef('');

  // Keep the screen on while cooking — a phone that sleeps mid-simmer is the thing this
  // mode exists to avoid. The browser Wake Lock API is a platform feature, so web costs
  // nothing.
  // ponytail: web only. Native needs expo-keep-awake, which is not installed here and
  // would want a dev-client rebuild — add it when hands-free ships on native.
  useEffect(() => {
    if (!enabled || Platform.OS !== 'web') return;
    const wakeLock = (navigator as any)?.wakeLock;
    if (!wakeLock) return;
    let sentinel: any = null;
    let released = false;
    // The lock is dropped whenever the tab is hidden and must be retaken on return.
    const acquire = () => {
      if (released || document.visibilityState !== 'visible') return;
      wakeLock.request('screen').then((s: any) => { sentinel = s; }).catch(() => {});
    };
    acquire();
    document.addEventListener('visibilitychange', acquire);
    return () => {
      released = true;
      document.removeEventListener('visibilitychange', acquire);
      try { sentinel?.release(); } catch {}
    };
  }, [enabled]);

  const speak = useCallback((what: string) => {
    if (!what.trim()) return;
    Speech.stop(); // never let two cards talk over each other
    Speech.speak(what, {
      language: handlers.current.language || undefined,
      // Slower than the default: this is being followed by someone holding a knife.
      rate: 0.92,
    });
  }, []);

  const repeat = useCallback(() => speak(handlers.current.text), [speak]);

  // Read each new card once, and only while the mode is on.
  useEffect(() => {
    if (!enabled) return;
    speak(text);
  }, [enabled, text, speak]);

  // Listening: web only, and only while enabled.
  useEffect(() => {
    if (!enabled || Platform.OS !== 'web') return;
    const SR = (globalThis as any).SpeechRecognition ?? (globalThis as any).webkitSpeechRecognition;
    if (!SR) return; // Firefox and friends: read-aloud only, no error to show for it

    const recognition = new SR();
    recognition.continuous = true;
    recognition.interimResults = false;
    if (handlers.current.language) recognition.lang = handlers.current.language;

    recognition.onresult = (event: any) => {
      const result = event.results[event.results.length - 1];
      const transcript: string = result[0]?.transcript ?? '';
      if (!result.isFinal || transcript === lastCommand.current) return;
      lastCommand.current = transcript;
      switch (matchCommand(transcript)) {
        case 'next': Speech.stop(); handlers.current.onNext(); break;
        case 'prev': Speech.stop(); handlers.current.onPrev(); break;
        case 'repeat': speak(handlers.current.text); break;
        case 'stop': Speech.stop(); break;
      }
    };
    // Browsers end a continuous session on their own after a pause; restart unless we are
    // the ones shutting it down, or hands-free goes deaf a minute into the cooking.
    let stopped = false;
    recognition.onend = () => { if (!stopped) { try { recognition.start(); } catch {} } };
    recognition.onerror = (e: any) => {
      // A denied mic is permanent for this page — stop retrying and fall back to tapping.
      if (e?.error === 'not-allowed' || e?.error === 'service-not-allowed') {
        stopped = true;
        setListening(false);
      }
    };

    try {
      recognition.start();
      recognitionRef.current = recognition;
      setListening(true);
    } catch {
      setListening(false);
    }

    return () => {
      stopped = true;
      setListening(false);
      recognitionRef.current = null;
      try { recognition.stop(); } catch {}
    };
  }, [enabled, speak]);

  // Silence on the way out, however the screen is left.
  useEffect(() => () => { Speech.stop(); }, []);

  const toggle = useCallback(() => {
    setEnabled(prev => {
      if (prev) Speech.stop();
      return !prev;
    });
  }, []);

  return { enabled, toggle, listening, repeat };
}
