import { useEffect, useMemo, useRef, useState } from 'react';
import apiService from '../services/apiService';
import { recordCook } from '../utils/feedbackPromptStore';
import { isOffline } from '../utils/offlineRecipes';
import { Recipe, RecipeResponse } from '../types';
import { useAlert } from '../context/AlertContext';
import { useLanguage } from '../context/LanguageContext';
import { groupSteps, Ingredient, Step, StepGroup } from '../screens/cooking/steps';

export type Phase = 'loading' | 'overview' | 'cooking' | 'done' | 'refining' | 'refined';

// The cooking run itself: which phase, which step, the notes taken along the way, and
// the AI refine those notes feed.
export function useCookingSession(initialRecipe: Recipe, onSaved: () => void) {
  const { showAlert, confirmAction } = useAlert();
  const { t } = useLanguage();
  const [recipe, setRecipe] = useState<Recipe>(initialRecipe);
  const [phase, setPhase] = useState<Phase>(initialRecipe.structured ? 'overview' : 'loading');
  const [currentGroup, setCurrentGroup] = useState(0);
  const [notes, setNotes] = useState<Record<number, string>>({});
  const [refinedRecipe, setRefinedRecipe] = useState<RecipeResponse | null>(null);
  const [saving, setSaving] = useState(false);
  // How many the cook is cooking for (BACKLOG 17.3). It lives in the session, not in
  // RecipeView, because the overview and the step cards must not disagree -- the
  // overview's RecipeView unmounts the moment cooking starts, and a number held there
  // would reset behind the cook's back. Nothing is written; this scales what is shown.
  const [servings, setServings] = useState<number | null>(null);
  // Ingredients ticked off while cooking (BACKLOG 12.1), keyed by index into
  // `ingredients` so a tick follows the ingredient across every step that uses it.
  // Session-only on purpose: what you have already put in the pan is not a fact about
  // the recipe, and it is worthless the next time you cook it.
  const [checked, setChecked] = useState<Record<number, boolean>>({});
  // Running step timers (BACKLOG 12.3), step index -> the wall-clock ms it ends at. An
  // end time rather than a remaining count is what makes a timer survive backgrounding:
  // the interval below stops when the app sleeps, and the number is still right on wake
  // because it was never the thing counting.
  // ponytail: no alert fires while the app is backgrounded — expo-notifications came out
  // in 9.5 and nothing else has needed it since. Reinstall it if a burnt pan proves it.
  const [timers, setTimers] = useState<Record<number, number>>({});
  const [now, setNow] = useState(() => Date.now());

  // One interval for every running timer, and none at all when nothing runs.
  useEffect(() => {
    if (Object.keys(timers).length === 0) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [timers]);

  useEffect(() => {
    if (initialRecipe.structured) return;
    apiService.getRecipeById(initialRecipe.id)
      .then(setRecipe)
      .catch(() => {/* fall through: overview renders the markdown text only */})
      .finally(() => setPhase('overview'));
  }, []);

  // A recipe with no structured document has no steps to walk: the overview renders its
  // markdown through RecipeView's fallback and hides "Start Cooking" entirely.
  const structured = recipe.structured;
  const steps: Step[] = structured?.steps ?? [];
  const ingredients: Ingredient[] = structured?.ingredients ?? [];
  // What the cook actually walks: one card per step, except where the recipe says two
  // things happen at once. A recipe with no cook flow yields one group per step, so this
  // is the old flat walk unchanged.
  const groups: StepGroup[] = useMemo(() => groupSteps(steps), [steps]);
  const group: StepGroup | undefined = groups[currentGroup];

  // Notes stay keyed by the ORIGINAL step index, not the group index: the refine prompt
  // below quotes steps[idx].step_text, and the recipe the notes get applied to still has
  // steps, not groups. A note taken on a parallel block attaches to its first step.
  const noteIndex = group?.indices[0] ?? 0;

  const setNote = (idx: number, text: string) => {
    if (!text.trim()) return;
    setNotes(prev => ({ ...prev, [idx]: text.trim() }));
  };

  const next = (pendingNote?: string) => {
    if (pendingNote?.trim()) setNote(noteIndex, pendingNote);
    if (currentGroup < groups.length - 1) {
      setCurrentGroup(prev => prev + 1);
      return;
    }
    // Walking off the last step is the one moment we know for certain the recipe was
    // cooked, so it marks itself — no second tap (BACKLOG 8.5, signal from 6.3).
    // Abandoning part-way exits through onClose and never gets here. Fire-and-forget:
    // a failed mark costs a suggestion cooldown, not the user's cooking session.
    setPhase('done');
    apiService.markCooked(recipe.id).catch(() => {});
    // The same moment is the milestone the pulse prompt rides on (BACKLOG 9.16) — it asks
    // after three finished cooks, which is the first point someone has an opinion worth
    // interrupting them for. Local-only and fire-and-forget, like the mark above.
    recordCook().catch(() => {});
  };

  const prev = () => setCurrentGroup(p => (p > 0 ? p - 1 : p));

  const toggleChecked = (i: number) => setChecked(prev => ({ ...prev, [i]: !prev[i] }));

  // Tap a duration to start it, tap it again to clear it. Several can run at once —
  // which is the ordinary case since 12.0, where a card holds two tracks.
  const toggleTimer = (stepIndex: number, seconds: number) => {
    const started = Date.now();
    setNow(started);
    setTimers(prev => {
      if (prev[stepIndex] != null) {
        const { [stepIndex]: _, ...rest } = prev;
        return rest;
      }
      return { ...prev, [stepIndex]: started + seconds * 1000 };
    });
  };

  // Starting a cook is the one moment the ticks are meaningless — a second run through
  // the same recipe begins with an empty board, even without leaving the screen.
  const startCooking = () => {
    setChecked({});
    setTimers({});
    setCurrentGroup(0);
    setPhase('cooking');
  };

  // Every AI call this cooking run makes — the refine below and each "Ask AI" question —
  // is a turn in one agent thread, so a question can follow up on the last answer and the
  // refine sees what was already discussed (BACKLOG 3.13).
  const conversationId = useRef<string | null>(null);

  // Which screen the pending proposal was launched from, so backing out of it — or saving
  // it — lands where the user was. Notes are refined at the end of a cook and belong back
  // on 'done'; a flow is planned before starting and belongs back on 'overview', ready to
  // cook the version just saved rather than bounced out to the library.
  const refineOrigin = useRef<'done' | 'overview'>('done');

  const turn = async (message: string) => {
    const result = await apiService.chatTurn(message, conversationId.current);
    conversationId.current = result.conversationId;
    return result;
  };

  // A question about the step the user is on. Returns the agent's prose; the panel that
  // calls it renders the running Q&A.
  const ask = async (question: string, stepText?: string) => {
    const context = stepText ? `\n\n(I'm on this step: ${stepText})` : '';
    const { text } = await turn(
      `I'm cooking my saved recipe ${recipe.id} ("${recipe.recipename}"). ${question}${context}`,
    );
    return text;
  };

  const refine = async () => {
    const noteEntries = Object.entries(notes).filter(([, n]) => n.trim());
    if (noteEntries.length === 0 || !recipe.structured) return;

    const changePrompt = noteEntries
      .map(([idx, note]) => {
        const s = steps[parseInt(idx, 10)];
        const prefix = s
          ? `After step ${parseInt(idx, 10) + 1} ("${s.step_text.slice(0, 60)}"): `
          : '';
        return prefix + note;
      })
      .join('\n');

    refineOrigin.current = 'done';
    setPhase('refining');
    try {
      // The agent loads the recipe by id itself (transform_recipe takes a saved id), so
      // the document no longer travels up with the request. It proposes; saveRefined
      // below is still the only thing that writes.
      const { text, document } = await turn(
        `Apply my cooking notes to my saved recipe ${recipe.id} and show me the updated version. Do not save it.\n${changePrompt}`,
      );
      if (!document) {
        // No artifact means the agent declined or asked something back — its own words
        // are a better message than the old envelope's canned one.
        showAlert(t('library.applyFailedTitle'), text || t('library.refineFailedBody'), 'error');
        setPhase('done');
        return;
      }
      setRefinedRecipe({ recipename: document.title, recipe: '', structured: document });
      setPhase('refined');
    } catch (err) {
      // The steps and the ingredients are cached; the agent is not (BACKLOG 12.4). Say
      // which of the two failed, or the cook retries a thing that cannot work.
      if (isOffline(err)) showAlert(t('cooking.offlineTitle'), t('cooking.offlineAi'), 'error');
      setPhase('done');
    }
  };

  // Ask the agent to re-plan how this recipe is cooked (its review_cook_flow tool): merge
  // the steps too thin to be worth a screen, phase them, and mark what runs in parallel.
  // Offered on the overview of a recipe that has no flow yet — every recipe saved before
  // it existed. Reuses refining/refined, so reviewing and saving the result is the same
  // screen the cooking-notes refine already goes through; it proposes, saveRefined writes.
  const planFlow = async () => {
    if (!recipe.structured) return;
    refineOrigin.current = 'overview';
    setPhase('refining');
    try {
      const { text, document } = await turn(
        `Review the cooking flow of my saved recipe ${recipe.id} and show me the result. Do not save it.`,
      );
      if (!document) {
        showAlert(t('cooking.planFailedTitle'), text || t('cooking.planFailedBody'), 'error');
        setPhase('overview');
        return;
      }
      setRefinedRecipe({ recipename: document.title, recipe: '', structured: document });
      setPhase('refined');
    } catch (err) {
      if (isOffline(err)) showAlert(t('cooking.offlineTitle'), t('cooking.offlineAi'), 'error');
      setPhase('overview');
    }
  };

  const saveRefined = async () => {
    if (!refinedRecipe?.structured) return;
    if (recipe.manually_edited) {
      const ok = await confirmAction(
        t('library.overwriteTitle'),
        t('library.overwriteBody'),
        { confirmLabel: t('recipes.apply') },
      );
      if (!ok) return;
    }
    setSaving(true);
    try {
      const updated = await apiService.patchRecipe({
        id: recipe.id,
        structured: refinedRecipe.structured,
        ai_sourced: true,
      });
      setRecipe(updated);
      setSaving(false);
      if (refineOrigin.current === 'overview') {
        // A re-planned flow is something to go and cook, not to leave. The overview now
        // renders the saved document's phases.
        setRefinedRecipe(null);
        setPhase('overview');
        return;
      }
      onSaved();
    } catch {
      setSaving(false);
    }
  };

  return {
    recipe, structured, steps, ingredients, groups, group,
    servings, setServings,
    checked, toggleChecked, startCooking,
    timers, now, toggleTimer,
    phase, setPhase, currentGroup, setCurrentGroup, noteIndex, refineOrigin,
    notes, setNote, next, prev,
    ask, refine, planFlow, refinedRecipe, saveRefined, saving,
  };
}
