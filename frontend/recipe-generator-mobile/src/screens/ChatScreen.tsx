import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  View, Text, ScrollView, TouchableOpacity, ActivityIndicator, Image,
  KeyboardAvoidingView, Platform, StyleSheet, useWindowDimensions,
} from 'react-native';
import Markdown from 'react-native-markdown-display';
import { Ionicons } from '@expo/vector-icons';
import RecipeView, { recipeMarkdownStyles } from '../components/RecipeView';
import { Theme, useTheme } from '../context/ThemeContext';
import { layout, radius, type } from '../theme';
import apiService, { ApiError } from '../services/apiService';
import { addCachedRecipe, getCachedRecipes } from '../utils/recipesCache';
import { useAlert } from '../context/AlertContext';
import { useLanguage } from '../context/LanguageContext';
import { currentLocale } from '../i18n';
import en from '../i18n/en';
import { parseISODate } from '../utils/mealPlanDates';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import { ChatApproval, ChatArtifact, ChatAttachment, ChatMessage, ChatSource, RecipeDocument } from '../types';
import { hostLabel } from '../utils/recipeOrigin';
import { foldArtifact } from '../utils/chatArtifacts';
import { formatUSD, resetsOn } from '../utils/budget';
import { diffRecipes, isEmptyDiff, diffSize, RecipeDiff } from '../utils/recipeDiff';
import { describeApproval } from '../utils/chatApproval';
import Composer from './chat/Composer';
import { useIsAuthenticated } from '../hooks/useIsAuthenticated';
import { FeedbackPulse } from '../components/FeedbackPulse';

// The home surface (BACKLOG.md 3.3): one thread that can generate, refine, save and plan,
// with the agent picking the action. Everything structured the turn produced arrives as an
// artifact and renders through the shared RecipeView — no second recipe renderer here.

// The tools we have words for, taken straight from the catalog so the two cannot drift: a
// tool added server-side simply has no `chat.tool.*` key and falls back to its own name —
// readable, rather than nothing.
const NAMED_TOOLS = new Set(Object.keys(en.chat.tool));

// Server error codes we have words for, under `chat.error.*`.
const NAMED_ERRORS = new Set([
  'budget_exceeded', 'conversation_not_found', 'timeout', 'iteration_limit', 'approval_expired',
]);

// The extracting tools carry what they are extracting *from* in their own arguments —
// the only place the client can learn it, and it arrives before the recipe does
// (BACKLOG.md 3.7). A spoken prompt needs nothing here: dictation lands in the composer,
// so the transcript is already the user's own bubble.
const sourceFromArgs = (name: string, args: string, attachments: ChatAttachment[]): ChatSource | null => {
  if (name !== 'generate_recipe' && name !== 'fetch_url') return null;
  let parsed: { description?: string; url?: string; image_ref?: string };
  try {
    parsed = JSON.parse(args || '{}');
  } catch {
    return null;
  }
  if (parsed.url) return { kind: 'url', value: parsed.url };
  if (parsed.image_ref) {
    // image1 is the turn's first attachment; the data URL doubles as the thumbnail.
    const image = attachments[Number(parsed.image_ref.replace(/\D/g, '')) - 1];
    return image?.type === 'image' ? { kind: 'photo', value: image.data } : null;
  }
  if (parsed.description) return { kind: 'text', value: parsed.description };
  return null;
};

// A refused turn reports its reason as a code, and it arrives two ways: as the `error`
// event's code mid-stream, and as the JSON body of the 429 billing.Guard returns before
// the stream ever opens. Both end up on ApiError — one as its message, one as its data.
const errorCode = (err: unknown): string => {
  if (!(err instanceof ApiError)) return '';
  const body = err.data as { error?: string; code?: string } | undefined;
  return body?.error || body?.code || err.message;
};

type Props = NativeStackScreenProps<RootStackParamList, 'Chat'>;

const ChatScreen: React.FC<Props> = ({ route, navigation }) => {
  const { theme } = useTheme();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const { showAlert } = useAlert();
  const { t } = useLanguage();
  const { width } = useWindowDimensions();
  const isWide = Platform.OS === 'web' && width > layout.breakpointWide;

  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [sending, setSending] = useState(false);
  const [expiredRefs, setExpiredRefs] = useState<string[]>([]);
  const [hydrating, setHydrating] = useState(false);
  const isAuthenticated = useIsAuthenticated();
  const [activeTool, setActiveTool] = useState<string | null>(null);
  const conversationId = useRef<string | null>(null);
  const scrollRef = useRef<ScrollView>(null);
  const abortRef = useRef<AbortController | null>(null);

  // Saved-recipe titles for week-plan cards, which carry ids rather than names.
  const [titles, setTitles] = useState<Record<number, string>>({});

  // Every recipe the thread has shown, by the name a later card can refer to it under
  // (BACKLOG.md 10.3). transform_recipe returns a *new* draft carrying `derived_from`, so
  // this is how the new card finds the document it replaced and shows the difference. A
  // saved recipe is filed under both spellings of its id, since the model writes either.
  const docsByRef = useMemo(() => {
    const byRef: Record<string, RecipeDocument> = {};
    for (const m of messages) {
      for (const a of m.artifacts ?? []) {
        if (a.kind !== 'recipe') continue;
        if (a.data.draft_ref) byRef[a.data.draft_ref] = a.data.document;
        if (a.data.recipe_id != null) {
          byRef[`recipe:${a.data.recipe_id}`] = a.data.document;
          byRef[String(a.data.recipe_id)] = a.data.document;
        }
      }
    }
    return byRef;
  }, [messages]);
  useEffect(() => {
    getCachedRecipes().then((recipes) => {
      if (recipes) setTitles(Object.fromEntries(recipes.map((r) => [r.id, r.recipename])));
    });
  }, []);

  // Abort the in-flight turn on unmount so an abandoned stream stops server-side too.
  useEffect(() => () => abortRef.current?.abort(), []);

  // One turn of streaming, shared by sending a message and answering an approval
  // (BACKLOG.md 10.2): both fold the same events into the assistant bubble the caller has
  // just appended, which is always the thread's last message.
  const runTurn = useCallback(async (
    attachments: ChatAttachment[],
    message: string,
    approval?: { id: string; approve: boolean },
  ) => {
    const patch = (fn: (m: ChatMessage) => ChatMessage) =>
      setMessages((prev) => prev.map((m, i) => (i === prev.length - 1 ? fn(m) : m)));

    const controller = new AbortController();
    abortRef.current = controller;
    try {
      conversationId.current = await apiService.streamChat(message, conversationId.current, (event) => {
        switch (event.type) {
          case 'token':
            patch((m) => ({ ...m, text: m.text + event.payload.text }));
            break;
          case 'tool_start': {
            setActiveTool(event.payload.name);
            const source = sourceFromArgs(event.payload.name, event.payload.args, attachments);
            patch((m) => ({
              ...m,
              tools: [...(m.tools ?? []), event.payload.name],
              sources: source ? [...(m.sources ?? []), source] : m.sources,
            }));
            break;
          }
          case 'tool_end':
            setActiveTool(null);
            break;
          case 'artifact':
            // A save re-emits the recipe's card carrying its new recipe_id. That usually
            // lands a turn or more after the card it updates ("make me a pasta" … "save
            // it"), so the fold searches the whole thread, not just this turn: the
            // existing card flips to Saved instead of a second copy appearing below it.
            setMessages((prev) => foldArtifact(prev, event.payload));
            break;
          case 'approval_request':
            // The turn ends here and resumes on the answer, so the card lands on this
            // bubble and the composer is free again.
            patch((m) => ({ ...m, approval: event.payload }));
            break;
        }
      }, controller.signal, attachments, approval);
    } catch (err) {
      if (controller.signal.aborted) {
        // Stopped on purpose: keep the partial turn, mark it, and say nothing about errors.
        patch((m) => ({ ...m, stopped: true }));
      } else {
        const code = errorCode(err);
        // The cap is real money, so the refusal names the number and the day it lifts
        // rather than the fact that something went wrong (BACKLOG.md 10.5). The figures
        // come from /usage — not budget-guarded, but a failure there still leaves the
        // plain sentence rather than nothing.
        let text = NAMED_ERRORS.has(code) ? t(`chat.error.${code}`) : t('common.unknownError');
        if (code === 'budget_exceeded') {
          text = await apiService.getUsage().then(
            (u) => t('chat.error.budget_exceeded_detail', { cap: formatUSD(u.cap_usd, currentLocale()), date: resetsOn(u.period_start, currentLocale()) }),
            () => text,
          );
        }
        patch((m) => ({ ...m, error: text }));
      }
    } finally {
      setActiveTool(null);
      setSending(false);
      abortRef.current = null;
    }
  }, [t]);

  // Reopening a thread (BACKLOG.md 10.4). The server has already folded each turn's
  // messages and cards into bubbles, so this is a straight swap of the transcript — the
  // one thing it has to get right is pointing `conversationId` at the thread *before* the
  // next turn, or the follow-up would open a second conversation.
  const openThread = useCallback(async (id: string) => {
    abortRef.current?.abort();
    setHydrating(true);
    try {
      const thread = await apiService.getConversation(id);
      conversationId.current = thread.id;
      setExpiredRefs([]);
      setMessages(thread.messages.map((m) => ({
        role: m.role,
        text: m.text,
        attachmentCount: m.attachment_count || undefined,
        artifacts: m.artifacts,
      })));
    } catch {
      showAlert(t('chat.history.openFailedTitle'), t('chat.history.openFailedBody'), 'error');
    } finally {
      setHydrating(false);
    }
  }, [showAlert, t]);

  // A new thread is the absence of one: drop the id and the transcript, and the next turn
  // opens a fresh conversation server-side. Nothing is deleted — the old thread is in the
  // list the moment this runs.
  const newThread = useCallback(() => {
    abortRef.current?.abort();
    conversationId.current = null;
    setExpiredRefs([]);
    setMessages([]);
  }, []);

  const send = useCallback(async (text: string, attachments: ChatAttachment[] = []) => {
    const message = text.trim();
    if ((!message && attachments.length === 0) || sending) return;

    setSending(true);
    setMessages((prev) => [
      ...prev,
      { role: 'user', text: message, attachmentCount: attachments.length || undefined },
      { role: 'assistant', text: '' },
    ]);
    await runTurn(attachments, message);
  }, [sending, runTurn]);

  // Answering an approval is a turn with no user message: the decision is the whole input,
  // and the server resumes the tool call it parked. The card keeps its outcome so the
  // thread still reads as a history rather than losing what was asked.
  const decide = useCallback(async (approval: ChatApproval, approve: boolean) => {
    if (sending) return;
    setSending(true);
    setMessages((prev) => [
      ...prev.map((m) =>
        m.approval?.id === approval.id
          ? { ...m, approval: { ...m.approval, decision: approve ? ('approved' as const) : ('declined' as const) } }
          : m
      ),
      { role: 'assistant', text: '' },
    ]);
    await runTurn([], '', { id: approval.id, approve });
  }, [sending, runTurn]);

  // Saving a draft the agent wrote, without asking the model to do it again. The server
  // saves through the same idempotent path as the save_recipe tool, so a second tap — or
  // a later "save it" — lands on the recipe already saved.
  const [savingRef, setSavingRef] = useState<string | null>(null);
  const saveDraft = useCallback(async (draftRef: string) => {
    const conversation = conversationId.current;
    if (!conversation || savingRef) return;
    setSavingRef(draftRef);
    try {
      const saved = await apiService.saveChatDraft(conversation, draftRef);
      addCachedRecipe(saved);
      setMessages((prev) =>
        prev.map((m) =>
          m.artifacts
            ? {
                ...m,
                artifacts: m.artifacts.map((a) =>
                  a.kind === 'recipe' && a.data.draft_ref === draftRef
                    ? { ...a, data: { ...a.data, recipe_id: saved.id } }
                    : a
                ),
              }
            : m
        )
      );
    } catch (err) {
      // The server forgets a cold conversation's drafts (2h, and a restart). Nothing the
      // button can do then, so the card falls back to asking the assistant.
      const code = err instanceof ApiError ? (err.data as { code?: string } | undefined)?.code : undefined;
      if (code === 'draft_expired') setExpiredRefs((prev) => [...prev, draftRef]);
      else showAlert(t('chat.saveFailedTitle'), t('chat.saveFailedBody'), 'error');
    } finally {
      setSavingRef(null);
    }
  }, [showAlert, savingRef, t]);

  // A prompt handed over by another screen (the planner's "Plan my week" / "Ask the
  // assistant") is sent as an ordinary turn, then cleared so going back doesn't resend it.
  const handedPrompt = route.params?.prompt;
  useEffect(() => {
    if (!handedPrompt) return;
    navigation.setParams({ prompt: undefined });
    send(handedPrompt);
  }, [handedPrompt, navigation, send]);

  // A thread picked on the History screen (BACKLOG.md 10.7). Same hand-over shape as
  // `prompt`: cleared once acted on, so going back does not re-hydrate it.
  const handedThread = route.params?.threadId;
  useEffect(() => {
    if (!handedThread) return;
    navigation.setParams({ threadId: undefined });
    openThread(handedThread);
  }, [handedThread, navigation, openThread]);

  return (
    <KeyboardAvoidingView style={styles.root} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      {/* The way back into an older conversation, and the way out of this one
          (BACKLOG.md 10.4). Signed-in only: an anonymous thread lives in server memory
          and has no row to list. */}
      {isAuthenticated && (
        <View style={[styles.threadBar, isWide && styles.wide]}>
          <TouchableOpacity
            style={styles.threadBarBtn}
            onPress={() => navigation.navigate('History')}
            accessibilityRole="button"
            accessibilityLabel={t('chat.history.title')}
          >
            <Ionicons name="time-outline" size={16} color={theme.subtext} />
            <Text style={styles.threadBarText}>{t('chat.history.title')}</Text>
          </TouchableOpacity>
          {messages.length > 0 && (
            <TouchableOpacity
              style={styles.threadBarBtn}
              onPress={newThread}
              accessibilityRole="button"
              accessibilityLabel={t('chat.history.new')}
            >
              <Ionicons name="add" size={16} color={theme.subtext} />
              <Text style={styles.threadBarText}>{t('chat.history.new')}</Text>
            </TouchableOpacity>
          )}
        </View>
      )}

      <ScrollView
        ref={scrollRef}
        style={styles.thread}
        contentContainerStyle={[styles.threadContent, isWide && styles.wide]}
        onContentSizeChange={() => scrollRef.current?.scrollToEnd({ animated: true })}
      >
        {/* The occasional "how is it going" card (BACKLOG 9.16). It decides for itself
            whether it is eligible and renders nothing when it is not. */}
        <FeedbackPulse route="Chat" />

        {hydrating && <ActivityIndicator color={theme.accent} />}

        {messages.length === 0 && !hydrating && (
          <View style={styles.empty}>
            <Text style={styles.emptyTitle}>{t('chat.emptyTitle')}</Text>
            {(['chat.opener1', 'chat.opener2', 'chat.opener3'].map((k) => t(k))).map((o) => (
              <TouchableOpacity key={o} style={styles.opener} onPress={() => send(o)}>
                <Text style={styles.openerText}>{o}</Text>
              </TouchableOpacity>
            ))}
          </View>
        )}

        {messages.map((m, i) => (
          <Turn
            key={i}
            message={m}
            theme={theme}
            styles={styles}
            t={t}
            titles={titles}
            docsByRef={docsByRef}
            savingRef={savingRef}
            expiredRefs={expiredRefs}
            onSaveDraft={saveDraft}
            sending={sending}
            live={sending && i === messages.length - 1}
            onDecide={decide}
          />
        ))}

        {activeTool && (
          <View style={styles.working}>
            <ActivityIndicator size="small" color={theme.accent} />
            <Text style={styles.workingText}>{NAMED_TOOLS.has(activeTool) ? t(`chat.tool.${activeTool}`) : activeTool}…</Text>
          </View>
        )}
      </ScrollView>

      <View style={isWide ? styles.wide : undefined}>
        <Composer sending={sending} onSend={send} onStop={() => abortRef.current?.abort()} />
      </View>
    </KeyboardAvoidingView>
  );
};

type Translate = (key: string, options?: Record<string, unknown>) => string;

interface TurnProps {
  message: ChatMessage;
  theme: Theme;
  /** Passed down rather than re-read per card: these are all render-only children. */
  t: Translate;
  styles: ReturnType<typeof makeStyles>;
  titles: Record<number, string>;
  /** Every recipe the thread has shown, so a transformed card can diff against its source. */
  docsByRef: Record<string, RecipeDocument>;
  /** Draft-save plumbing for recipe artifacts: which ref is in flight, which are gone. */
  savingRef: string | null;
  expiredRefs: string[];
  onSaveDraft: (draftRef: string) => void;
  /** A pending write's Apply / Change it (BACKLOG.md 10.2), disabled while a turn runs. */
  sending: boolean;
  /** This is the turn in flight: its steps are still the spinner's job, not a summary's. */
  live?: boolean;
  onDecide: (approval: ChatApproval, approve: boolean) => void;
}

const Turn: React.FC<TurnProps> = ({ message, theme, styles, titles, docsByRef, savingRef, expiredRefs, onSaveDraft, sending, live, onDecide, t }) => {
  if (message.role === 'user') {
    return (
      <View style={styles.userBubble}>
        {message.attachmentCount ? (
          <Text style={styles.userAttachments}>
            <Ionicons name="image-outline" size={12} color={theme.subtext} />
            {` ${t('chat.photoCount', { count: message.attachmentCount })}`}
          </Text>
        ) : null}
        <Text style={styles.userText}>{message.text}</Text>
      </View>
    );
  }
  return (
    <View style={styles.assistantTurn}>
      {message.text !== '' && <Markdown style={recipeMarkdownStyles(theme)}>{message.text}</Markdown>}
      {message.sources?.map((s, i) => (
        <SourceCard key={i} source={s} theme={theme} styles={styles} t={t} />
      ))}
      {message.artifacts?.map((a, i) => (
        <ArtifactCard
          key={i}
          artifact={a}
          theme={theme}
          styles={styles}
          titles={titles}
          docsByRef={docsByRef}
          t={t}
          savingRef={savingRef}
          expiredRefs={expiredRefs}
          onSaveDraft={onSaveDraft}
        />
      ))}
      {message.approval && (
        <ApprovalCard approval={message.approval} styles={styles} t={t} sending={sending} onDecide={onDecide} />
      )}
      {message.tools?.length && !live ? <Steps tools={message.tools} theme={theme} styles={styles} t={t} /> : null}
      {message.stopped && <Text style={styles.stoppedText}>{t('chat.stopped')}</Text>}
      {message.error && (
        <View style={styles.errorBox}>
          <Ionicons name="alert-circle-outline" size={16} color={theme.accent} />
          <Text style={styles.errorText}>{message.error}</Text>
        </View>
      )}
    </View>
  );
};

// What the agent read, shown in the thread ahead of what it produced, so an extracted
// recipe is visibly an extraction rather than an invention.
const SourceCard: React.FC<{ source: ChatSource } & Pick<TurnProps, 'theme' | 'styles' | 't'>> = ({ source, theme, styles, t }) => (
  <View style={styles.sourceCard}>
    {source.kind === 'photo' ? (
      <Image source={{ uri: source.value }} style={styles.sourceThumb} />
    ) : (
      <Ionicons name={source.kind === 'url' ? 'link-outline' : 'chatbubble-outline'} size={16} color={theme.subtext} />
    )}
    <View style={styles.sourceBody}>
      <Text style={styles.sourceLabel}>
        {source.kind === 'photo'
          ? t('chat.source.photo')
          : source.kind === 'url'
          ? t('chat.source.url', { host: hostLabel(source.value) })
          : t('chat.source.text')}
      </Text>
      {source.kind !== 'photo' && (
        <Text style={styles.sourceDetail} numberOfLines={2}>{source.value}</Text>
      )}
    </View>
  </View>
);

// The write the agent is asking to make, shown before it happens (BACKLOG.md 10.2). The
// approval is structural: nothing has run when this renders, so declining leaves the
// database exactly as it was rather than needing an undo.
const ApprovalCard: React.FC<
  { approval: ChatApproval } & Pick<TurnProps, 'styles' | 't' | 'sending' | 'onDecide'>
> = ({ approval, styles, t, sending, onDecide }) => {
  const { key, values } = describeApproval(approval);
  // The summary carries a raw slot and a raw ISO date because the describer is language
  // agnostic; both become readable here, where the catalog and the locale are.
  const readable = {
    ...values,
    ...(typeof values.slot === 'string' ? { slot: t(`plan.slot.${values.slot}`) } : {}),
    ...(typeof values.date === 'string' && values.date
      ? { date: parseISODate(values.date).toLocaleDateString(currentLocale(), { weekday: 'long', day: 'numeric', month: 'long' }) }
      : {}),
  };
  return (
    <View style={styles.card}>
      <Text style={styles.cardTitle}>{t('chat.approval.title')}</Text>
      <Text style={styles.approvalWhat}>{t(`chat.approval.${key}`, readable)}</Text>

      {approval.replaces && approval.replaces.length > 0 && (
        <>
          <Text style={styles.draftTag}>{t('chat.approval.replaces')}</Text>
          {approval.replaces.map((r) => (
            <View key={`${r.date} ${r.meal_slot}`} style={styles.planRow}>
              <Text style={styles.planDay}>
                {parseISODate(r.date).toLocaleDateString(currentLocale(), { weekday: 'short', day: 'numeric', month: 'short' })}
                {` · ${t(`plan.slot.${r.meal_slot}`)}`}
              </Text>
              <Text style={styles.planMeal} numberOfLines={1}>{r.current_recipe}</Text>
            </View>
          ))}
        </>
      )}

      {approval.decision ? (
        <Text style={styles.draftTag}>{t(`chat.approval.${approval.decision}`)}</Text>
      ) : (
        <View style={styles.approvalActions}>
          <TouchableOpacity
            style={styles.approveButton}
            onPress={() => onDecide(approval, true)}
            disabled={sending}
            accessibilityRole="button"
          >
            <Text style={styles.approveButtonText}>{t('chat.approval.apply')}</Text>
          </TouchableOpacity>
          <TouchableOpacity
            style={styles.declineButton}
            onPress={() => onDecide(approval, false)}
            disabled={sending}
            accessibilityRole="button"
          >
            <Text style={styles.declineButtonText}>{t('chat.approval.change')}</Text>
          </TouchableOpacity>
        </View>
      )}
    </View>
  );
};

const ArtifactCard: React.FC<
  { artifact: ChatArtifact } & Pick<TurnProps, 'theme' | 'styles' | 'titles' | 'docsByRef' | 'savingRef' | 'expiredRefs' | 'onSaveDraft' | 't'>
> = ({
  artifact, theme, styles, titles, docsByRef, savingRef, expiredRefs, onSaveDraft, t,
}) => {
  if (artifact.kind === 'recipe') {
    const doc = artifact.data.document;
    const ref = artifact.data.draft_ref;
    const saved = artifact.data.recipe_id != null;
    const expired = !!ref && expiredRefs.includes(ref);
    return (
      <View style={styles.card}>
        <Text style={styles.cardTitle}>{doc.title}</Text>
        {saved ? (
          <Text style={styles.draftTag}>
            <Ionicons name="checkmark-circle" size={12} color={theme.accent} /> {t('chat.savedToRecipes')}
          </Text>
        ) : ref && !expired ? (
          <TouchableOpacity
            style={styles.saveButton}
            onPress={() => onSaveDraft(ref)}
            disabled={savingRef != null}
          >
            {savingRef === ref ? (
              <ActivityIndicator size="small" color={theme.accent} />
            ) : (
              <Ionicons name="bookmark-outline" size={14} color={theme.accent} />
            )}
            <Text style={styles.saveButtonText}>
              {savingRef === ref ? t('common.saving') : t('chat.saveToRecipes')}
            </Text>
          </TouchableOpacity>
        ) : (
          <Text style={styles.draftTag}>{t('chat.unsavedDraft')}</Text>
        )}
        <ChangeSummary
          prev={artifact.data.derived_from ? docsByRef[artifact.data.derived_from] : undefined}
          next={doc}
          theme={theme}
          styles={styles}
          t={t}
        />
        <RecipeView structured={doc} markdown="" />
      </View>
    );
  }

  const { plan, starts_on, ends_on } = artifact.data;
  return (
    <View style={styles.card}>
      <Text style={styles.cardTitle}>{t('chat.weekOf', { start: starts_on, end: ends_on })}</Text>
      {plan.message && <Text style={styles.draftTag}>{plan.message}</Text>}
      {plan.assignments.map((a) => (
        <View key={a.planned_on} style={styles.planRow}>
          <Text style={styles.planDay}>
            {parseISODate(a.planned_on).toLocaleDateString(currentLocale(), { weekday: 'short', day: 'numeric', month: 'short' })}
          </Text>
          <Text style={styles.planMeal} numberOfLines={1}>
            {a.variant?.title ?? (a.recipe_id != null ? titles[a.recipe_id] ?? t('chat.recipeNumber', { id: a.recipe_id }) : '—')}
          </Text>
        </View>
      ))}
      <Text style={styles.draftTag}>{t('chat.proposalOnly')}</Text>
    </View>
  );
};

// What the turn actually did, kept after it ends instead of vanishing with the spinner
// (BACKLOG.md 10.5): a turn that made six tool calls should not read like one. Collapsed,
// because the count is the answer most of the time. Only the live stream carries tool
// names, so a reopened thread shows none — the transcript stores results, not the calls.
const Steps: React.FC<{ tools: string[] } & Pick<TurnProps, 'theme' | 'styles' | 't'>> = ({ tools, theme, styles, t }) => {
  const [open, setOpen] = useState(false);
  return (
    <View style={styles.diffBox}>
      <TouchableOpacity
        style={styles.diffHeader}
        onPress={() => setOpen((v) => !v)}
        accessibilityRole="button"
        accessibilityState={{ expanded: open }}
      >
        <Ionicons name={open ? 'chevron-down' : 'chevron-forward'} size={14} color={theme.subtext} />
        <Text style={styles.diffTitle}>{t('chat.steps.title', { count: tools.length })}</Text>
      </TouchableOpacity>
      {open &&
        tools.map((name, i) => (
          <Text key={`${i}:${name}`} style={styles.diffLine}>
            {`${i + 1}. ${NAMED_TOOLS.has(name) ? t(`chat.tool.${name}`) : name}`}
          </Text>
        ))}
    </View>
  );
};

// What this turn changed about the recipe, instead of a silently re-rendered card
// (BACKLOG.md 10.3). Collapsed by default: the answer to "did it do what I asked" is the
// one-line count, and the lines themselves are for when that is not enough. Renders
// nothing at all when the turn produced a fresh recipe (no `prev`) or changed nothing —
// an empty "Changes" heading is worse than no heading.
const ChangeSummary: React.FC<
  { prev?: RecipeDocument; next: RecipeDocument } & Pick<TurnProps, 'theme' | 'styles' | 't'>
> = ({ prev, next, theme, styles, t }) => {
  const diff = useMemo<RecipeDiff | null>(() => (prev ? diffRecipes(prev, next) : null), [prev, next]);
  const [open, setOpen] = useState(false);
  if (!diff || isEmptyDiff(diff)) return null;

  const lines: Array<{ key: string; text: string; tone: 'added' | 'removed' | 'changed' }> = [
    ...diff.fields.map((f) => ({
      key: `f:${f.key}`,
      text: t(`chat.diff.field.${f.key}`, { from: f.from, to: f.to }),
      tone: 'changed' as const,
    })),
    ...diff.ingredients.changed.map((c) => ({
      key: `c:${c.from}`,
      text: t('chat.diff.changedLine', { from: c.from, to: c.to }),
      tone: 'changed' as const,
    })),
    ...diff.ingredients.added.map((a) => ({ key: `a:${a}`, text: a, tone: 'added' as const })),
    ...diff.ingredients.removed.map((r) => ({ key: `r:${r}`, text: r, tone: 'removed' as const })),
  ];
  if (diff.steps.changed.length) {
    lines.push({
      key: 'steps:changed',
      text: t('chat.diff.stepsChanged', { count: diff.steps.changed.length, steps: diff.steps.changed.join(', ') }),
      tone: 'changed',
    });
  }
  if (diff.steps.added) {
    lines.push({ key: 'steps:added', text: t('chat.diff.stepsAdded', { count: diff.steps.added }), tone: 'added' });
  }
  if (diff.steps.removed) {
    lines.push({ key: 'steps:removed', text: t('chat.diff.stepsRemoved', { count: diff.steps.removed }), tone: 'removed' });
  }

  return (
    <View style={styles.diffBox}>
      <TouchableOpacity
        style={styles.diffHeader}
        onPress={() => setOpen((v) => !v)}
        accessibilityRole="button"
        accessibilityState={{ expanded: open }}
      >
        <Ionicons name={open ? 'chevron-down' : 'chevron-forward'} size={14} color={theme.subtext} />
        <Text style={styles.diffTitle}>{t('chat.diff.title', { count: diffSize(diff) })}</Text>
      </TouchableOpacity>
      {open &&
        lines.map((l) => (
          <Text key={l.key} style={[styles.diffLine, styles[`diff_${l.tone}`]]} >
            {l.tone === 'added' ? '+ ' : l.tone === 'removed' ? '− ' : '~ '}
            {l.text}
          </Text>
        ))}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  root: { flex: 1, backgroundColor: t.bg },
  thread: { flex: 1 },
  threadContent: { paddingHorizontal: 14, paddingTop: 10, paddingBottom: 4, gap: 12 },
  wide: { maxWidth: layout.contentMaxWidth, width: '100%', alignSelf: 'center' },

  threadBar: { flexDirection: 'row', justifyContent: 'space-between', paddingHorizontal: 14, paddingTop: 8 },
  threadBarBtn: { flexDirection: 'row', alignItems: 'center', gap: 4, paddingVertical: 4 },
  threadBarText: { ...type.label, fontSize: 13, color: t.subtext },

  empty: { paddingVertical: 12, gap: 8 },
  emptyTitle: { ...type.title, fontSize: 22, lineHeight: 30, color: t.text, marginBottom: 8 },
  opener: { backgroundColor: t.surfaceRaised, borderWidth: 1, borderColor: t.border, borderRadius: 12, padding: 12 },
  openerText: { color: t.subtext, ...type.body, fontSize: 14 },

  userBubble: { alignSelf: 'flex-end', maxWidth: '85%', backgroundColor: t.accentFaded, borderRadius: 14, paddingHorizontal: 14, paddingVertical: 10 },
  userText: { color: t.text, ...type.body, fontSize: 14, lineHeight: 20 },
  userAttachments: { color: t.subtext, ...type.caption, marginBottom: 4 },
  assistantTurn: { gap: 10 },

  sourceCard: { flexDirection: 'row', alignItems: 'center', gap: 10, backgroundColor: t.surfaceRaised, borderWidth: 1, borderColor: t.border, borderRadius: 12, padding: 10 },
  sourceThumb: { width: 40, height: 40, borderRadius: 8 },
  sourceBody: { flex: 1 },
  sourceLabel: { ...type.label, fontSize: 13, color: t.subtext },
  sourceDetail: { ...type.caption, color: t.muted },

  diffBox: { backgroundColor: t.surfaceRaised, borderWidth: 1, borderColor: t.border, borderRadius: 10, paddingHorizontal: 10, paddingVertical: 8, gap: 4 },
  diffHeader: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  diffTitle: { ...type.label, fontSize: 13, color: t.subtext },
  diffLine: { ...type.body, fontSize: 13, lineHeight: 19 },
  diff_added: { color: t.accent },
  diff_removed: { color: t.muted, textDecorationLine: 'line-through' },
  diff_changed: { color: t.subtext },

  card: { backgroundColor: t.surface, borderWidth: 1, borderColor: t.border, borderRadius: 14, padding: 14, gap: 6 },
  cardTitle: { ...type.title, fontSize: 16, lineHeight: 22, color: t.text },
  draftTag: { ...type.caption, color: t.muted, fontStyle: 'italic' },
  saveButton: {
    flexDirection: 'row',
    alignItems: 'center',
    alignSelf: 'flex-start',
    gap: 6,
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: radius.full,
    backgroundColor: t.accentFaded,
  },
  saveButtonText: { ...type.label, fontSize: 13, color: t.accent },

  planRow: { flexDirection: 'row', alignItems: 'center', gap: 10, paddingVertical: 6, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.border },
  planDay: { width: 96, ...type.label, fontSize: 13, color: t.subtext },
  planMeal: { flex: 1, ...type.body, fontSize: 14, color: t.text },

  working: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  workingText: { ...type.body, fontSize: 13, color: t.muted },

  approvalWhat: { ...type.body, fontSize: 14, color: t.text },
  approvalActions: { flexDirection: 'row', gap: 8, marginTop: 4 },
  approveButton: { paddingHorizontal: 14, paddingVertical: 8, borderRadius: radius.full, backgroundColor: t.accent },
  approveButtonText: { ...type.label, fontSize: 13, color: t.bg },
  declineButton: { paddingHorizontal: 14, paddingVertical: 8, borderRadius: radius.full, borderWidth: 1, borderColor: t.border },
  declineButtonText: { ...type.label, fontSize: 13, color: t.subtext },

  stoppedText: { ...type.caption, color: t.muted, fontStyle: 'italic' },

  errorBox: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  errorText: { ...type.body, fontSize: 13, color: t.accent, flex: 1 },

});

export default ChatScreen;
