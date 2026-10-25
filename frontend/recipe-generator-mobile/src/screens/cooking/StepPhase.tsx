import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  View, Text, StyleSheet, ScrollView, TouchableOpacity,
  KeyboardAvoidingView, Platform,
} from 'react-native';
import { Recipe } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import { fmtIngredient, scaleIngredient } from '../../utils/recipeIngredient';
import { Badge } from '../../components/ui';
import {
  Ingredient, phaseLabelKey, StepGroup, formatCountdown, formatTimer, getStepIngredientIndices,
  groupSpokenText,
} from './steps';
import { makeChromeStyles } from './styles';
import { AskAiPanel, NotePanel, SavedNote } from './StepPanels';
import { useHandsFree } from '../../hooks/useHandsFree';

interface Props {
  recipe: Recipe;
  groups: StepGroup[];
  ingredients: Ingredient[];
  /** Servings multiplier from the overview's scaler, 1 when the cook left it alone. */
  scale: number;
  currentGroup: number;
  /** Original step index this card's notes are keyed on. */
  noteIndex: number;
  notes: Record<number, string>;
  /** Ingredients ticked off this cook, keyed by index into `ingredients`. */
  checked: Record<number, boolean>;
  onToggleChecked: (index: number) => void;
  /** Running timers (BACKLOG 12.3): step index -> the wall-clock ms it ends at. */
  timers: Record<number, number>;
  now: number;
  onToggleTimer: (stepIndex: number, seconds: number) => void;
  onSaveNote: (idx: number, text: string) => void;
  onClose: () => void;
  onPrev: () => void;
  onNext: (pendingNote: string) => void;
  onAsk: (question: string, stepText?: string) => Promise<string>;
}

// One card of cooking at a time — a single step, or the several a cook does at once —
// with the two panels that hang off it: a note the user takes, and a question to the AI.
const StepPhase: React.FC<Props> = ({
  recipe, groups, ingredients, scale, currentGroup, noteIndex, notes, checked, onToggleChecked,
  timers, now, onToggleTimer, onSaveNote, onClose, onPrev, onNext, onAsk,
}) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const c = useMemo(() => makeChromeStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const scrollRef = useRef<ScrollView>(null);

  const [noteInput, setNoteInput] = useState('');
  const [showNoteInput, setShowNoteInput] = useState(false);
  const [showAiInput, setShowAiInput] = useState(false);

  // A duration reads as itself until it is started, then counts down in mm:ss, then says
  // so. One label for both places a duration appears — the card's heading and a parallel
  // track's badge — because they are the same clock.
  const timerLabel = (stepIndex: number, seconds: number) => {
    const endsAt = timers[stepIndex];
    if (endsAt == null) return `⏱ ${formatTimer(seconds)}`;
    const left = endsAt - now;
    return left <= 0 ? `⏰ ${t('cooking.timerDone')}` : `⏱ ${formatCountdown(left)}`;
  };

  const group = groups[currentGroup];
  const parallel = (group?.steps.length ?? 0) > 1;
  const spoken = group ? groupSpokenText(group, t) : '';

  // The note input is uncontrolled by the parent, so hand onNext what is currently typed —
  // same contract as the buttons below, which is why voice routes through this and not
  // straight to the parent's `next`.
  const noteRef = useRef(noteInput);
  noteRef.current = noteInput;
  const handsFree = useHandsFree({
    text: spoken,
    language: recipe.structured?.language,
    onNext: () => onNext(noteRef.current),
    onPrev,
  });

  // Reset the AI/note panels when the card changes.
  useEffect(() => {
    scrollRef.current?.scrollTo({ y: 0, animated: true });
    setShowAiInput(false);
    setShowNoteInput(false);
    setNoteInput(notes[noteIndex] ?? '');
  }, [currentGroup]);

  return (
    <KeyboardAvoidingView
      style={c.container}
      behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
      keyboardVerticalOffset={Platform.OS === 'ios' ? 0 : 20}
    >
      <View style={c.header}>
        <TouchableOpacity onPress={onClose} style={c.headerSide}>
          <Text style={c.headerAction}>✕</Text>
        </TouchableOpacity>
        <Text style={c.headerTitle} numberOfLines={1}>{recipe.recipename}</Text>
        <Text style={styles.stepCounter}>
          {groups.length > 0 ? `${currentGroup + 1}/${groups.length}` : ''}
        </Text>
      </View>

      {groups.length > 1 && (
        <View style={styles.progressBar}>
          <View style={[styles.progressFill, { width: `${((currentGroup + 1) / groups.length) * 100}%` as any }]} />
        </View>
      )}

      <ScrollView
        ref={scrollRef}
        style={c.scrollView}
        contentContainerStyle={styles.stepScrollContent}
        keyboardShouldPersistTaps="handled"
      >
        {group && (
          // Tapping the card re-reads it — the hands-free "repeat", available without
          // saying anything, and the tap-to-advance-adjacent target on native where
          // there is no listening.
          <TouchableOpacity
            activeOpacity={handsFree.enabled ? 0.7 : 1}
            onPress={handsFree.enabled ? handsFree.repeat : undefined}
          >
            <View style={styles.cardHeader}>
              <Text style={styles.stepLabel}>
                {group.phase ? t(phaseLabelKey(group.phase)) : t('cooking.stepNumber', { number: currentGroup + 1 })}
              </Text>
              {/* Tap the duration to run it (BACKLOG 12.3). Only on a single-step card:
                  a parallel block's heading is the longest track, and each track below
                  starts its own. */}
              {group.timerSeconds != null && group.timerSeconds > 0 && !parallel && (
                <TouchableOpacity onPress={() => onToggleTimer(group.indices[0], group.timerSeconds!)}>
                  <Text style={[styles.cardTimer, timers[group.indices[0]] != null && styles.cardTimerRunning]}>
                    {timerLabel(group.indices[0], group.timerSeconds)}
                  </Text>
                </TouchableOpacity>
              )}
              {group.timerSeconds != null && group.timerSeconds > 0 && parallel && (
                <Text style={styles.cardTimer}>⏱ {formatTimer(group.timerSeconds)}</Text>
              )}
            </View>

            {/* The one thing the card has to say when it holds more than one step: these
                are not consecutive, they overlap. */}
            {parallel && <Text style={styles.parallelLabel}>{t('cooking.atTheSameTimeShort')}</Text>}

            {group.steps.map((step, i) => {
              const stepIngredients = getStepIngredientIndices(step, ingredients);
              return (
                <View
                  key={step.sort_order}
                  style={[styles.track, parallel && i > 0 && styles.trackDivided]}
                >
                  <View style={styles.trackHead}>
                    {parallel && <Text style={styles.trackNumber}>{i + 1}</Text>}
                    <Text style={[styles.stepText, parallel && styles.stepTextParallel]}>
                      {step.step_text}
                    </Text>
                  </View>

                  {/* A track's own timer only earns its place next to the group's when
                      the tracks differ — otherwise it is the same number twice. */}
                  {((parallel && (step.timer_seconds ?? 0) > 0) || (step.temperature_c ?? 0) > 0) && (
                    <View style={styles.badgeRow}>
                      {parallel && step.timer_seconds != null && step.timer_seconds > 0 && (
                        <TouchableOpacity onPress={() => onToggleTimer(group.indices[i], step.timer_seconds!)}>
                          <Badge label={timerLabel(group.indices[i], step.timer_seconds)} />
                        </TouchableOpacity>
                      )}
                      {step.temperature_c != null && step.temperature_c > 0 && (
                        <Badge label={`🌡️ ${step.temperature_c}°C`} />
                      )}
                    </View>
                  )}

                  {stepIngredients.length > 0 && (
                    <View style={styles.stepIngSection}>
                      <Text style={styles.stepIngLabel}>{t('cooking.youWillNeed')}</Text>
                      <View style={styles.stepIngChips}>
                        {/* Tap to cross one off (BACKLOG 12.1). The chip stays put and
                            keeps its text — a cook glancing back needs to read what they
                            already added, not find it gone. */}
                        {stepIngredients.map(idx => {
                          const ing = ingredients[idx];
                          const done = !!checked[idx];
                          return (
                            <TouchableOpacity
                              key={idx}
                              onPress={() => onToggleChecked(idx)}
                              accessibilityRole="checkbox"
                              accessibilityState={{ checked: done }}
                              style={done && styles.chipChecked}
                            >
                              <Badge
                                tone="neutral"
                                label={`${done ? '✓ ' : ''}${fmtIngredient(scaleIngredient(ing, scale), scale !== 1)}${ing.optional ? ` ${t('cooking.optional')}` : ''}`}
                              />
                            </TouchableOpacity>
                          );
                        })}
                      </View>
                    </View>
                  )}
                </View>
              );
            })}
          </TouchableOpacity>
        )}

        {showNoteInput ? (
          <NotePanel
            value={noteInput}
            onChange={setNoteInput}
            onSave={() => {
              onSaveNote(noteIndex, noteInput);
              setShowNoteInput(false);
            }}
            onCancel={() => setShowNoteInput(false)}
          />
        ) : notes[noteIndex] ? (
          <SavedNote
            note={notes[noteIndex]}
            onEdit={() => {
              setNoteInput(notes[noteIndex]);
              setShowNoteInput(true);
            }}
          />
        ) : null}

        {showAiInput && (
          <AskAiPanel onAsk={onAsk} stepText={spoken} />
        )}
      </ScrollView>

      <View style={styles.actionBar}>
        <TouchableOpacity
          style={[styles.actionBtn, showNoteInput && styles.actionBtnActive]}
          onPress={() => {
            setShowNoteInput(prev => !prev);
            if (showAiInput) setShowAiInput(false);
          }}
        >
          <Text style={styles.actionBtnText}>{t('cooking.note')}</Text>
        </TouchableOpacity>
        <TouchableOpacity
          style={[styles.actionBtn, showAiInput && styles.actionBtnActive]}
          onPress={() => {
            setShowAiInput(prev => !prev);
            if (showNoteInput) setShowNoteInput(false);
          }}
        >
          <Text style={styles.actionBtnText}>{t('cooking.askAi')}</Text>
        </TouchableOpacity>
        <TouchableOpacity
          style={[styles.actionBtn, handsFree.enabled && styles.actionBtnActive]}
          onPress={handsFree.toggle}
        >
          <Text style={styles.actionBtnText}>
            {t(handsFree.enabled
              ? (handsFree.listening ? 'cooking.listening' : 'cooking.reading')
              : 'cooking.handsFree')}
          </Text>
        </TouchableOpacity>
      </View>

      {/* Say what the mode can actually do here, rather than leaving the cook talking to a
          phone that was never listening: continuous recognition is web-only today. */}
      {handsFree.enabled && (
        <Text style={styles.handsFreeHint}>
          {handsFree.listening
            ? t('cooking.handsFreeListening')
            : t('cooking.handsFreeReading')}
        </Text>
      )}

      <View style={styles.navBar}>
        <TouchableOpacity
          style={[styles.navBtnPrev, currentGroup === 0 && styles.navBtnDisabled]}
          onPress={onPrev}
          disabled={currentGroup === 0}
        >
          <Text style={[styles.navBtnPrevText, currentGroup === 0 && styles.navBtnTextDisabled]}>{t('cooking.prev')}</Text>
        </TouchableOpacity>
        <TouchableOpacity style={styles.navBtnNext} onPress={() => onNext(noteInput)}>
          <Text style={styles.navBtnNextText}>
            {t(currentGroup === groups.length - 1 ? 'cooking.finish' : 'cooking.next')}
          </Text>
        </TouchableOpacity>
      </View>
    </KeyboardAvoidingView>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  stepCounter: {
    ...type.label,
    color: t.muted,
    width: 60,
    textAlign: 'right',
  },
  progressBar: {
    height: 3,
    backgroundColor: t.border,
  },
  progressFill: {
    height: 3,
    backgroundColor: t.accent,
  },
  stepScrollContent: {
    padding: 24,
    paddingBottom: 20,
  },
  cardHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 6,
  },
  stepLabel: {
    ...type.label, fontSize: 13,
    color: t.accent,
    letterSpacing: 1.2,
    textTransform: 'uppercase',
  },
  cardTimer: {
    ...type.label, fontSize: 13,
    color: t.muted,
  },
  cardTimerRunning: {
    color: t.accent,
  },
  parallelLabel: {
    ...type.body, fontSize: 14,
    color: t.subtext,
    marginBottom: 10,
  },
  // One concurrent step. The divider is what separates two things happening at once from
  // two paragraphs of the same instruction.
  track: {
    marginBottom: 8,
  },
  trackDivided: {
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
    paddingTop: 16,
  },
  trackHead: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: 10,
  },
  trackNumber: {
    ...type.label, fontSize: 15,
    color: t.accent,
    lineHeight: 28,
    minWidth: 16,
  },
  stepText: {
    ...type.title, fontSize: 22,
    lineHeight: 32,
    color: t.text,
    marginBottom: 20,
    flexShrink: 1,
  },
  // Tracks are read together, so each one is a touch smaller than a lone step.
  stepTextParallel: {
    fontSize: 19,
    lineHeight: 28,
    marginBottom: 12,
  },
  badgeRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 8,
    marginBottom: 16,
  },
  handsFreeHint: {
    ...type.body, fontSize: 12,
    color: t.muted,
    textAlign: 'center',
    paddingTop: 8,
    backgroundColor: t.bg,
  },
  stepIngSection: {
    marginBottom: 20,
  },
  stepIngLabel: {
    ...type.label, fontSize: 11,
    color: t.muted,
    letterSpacing: 0.8,
    textTransform: 'uppercase',
    marginBottom: 8,
  },
  // Ticked off: dimmed rather than removed, so the list you read is still the list.
  chipChecked: {
    opacity: 0.4,
  },
  stepIngChips: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 6,
  },
  actionBar: {
    flexDirection: 'row',
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
    backgroundColor: t.surface,
  },
  actionBtn: {
    flex: 1,
    paddingVertical: 14,
    alignItems: 'center',
  },
  actionBtnActive: {
    backgroundColor: t.accentFaded,
  },
  actionBtnText: {
    ...type.label, fontSize: 15,
    color: t.subtext,
  },
  navBar: {
    flexDirection: 'row',
    paddingHorizontal: 16,
    paddingVertical: 12,
    paddingBottom: Platform.OS === 'ios' ? 28 : 12,
    gap: 12,
    backgroundColor: t.bg,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
  },
  navBtnPrev: {
    flex: 1,
    paddingVertical: 16,
    alignItems: 'center',
    borderRadius: 14,
    borderWidth: 1.5,
    borderColor: t.border,
  },
  navBtnDisabled: {
    opacity: 0.3,
  },
  navBtnPrevText: {
    ...type.label, fontSize: 16,
    color: t.text,
  },
  navBtnTextDisabled: {
    color: t.muted,
  },
  navBtnNext: {
    flex: 2,
    paddingVertical: 16,
    alignItems: 'center',
    backgroundColor: t.accent,
    borderRadius: 14,
  },
  navBtnNextText: {
    ...type.label, fontSize: 16,
    color: t.onAccent,
  },
});

export default StepPhase;
