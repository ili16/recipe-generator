/**
 * Self-check for the chat thread's artifact fold. A save re-emits the recipe's card with
 * its new recipe_id, usually a turn or two after the card it updates — get this wrong and
 * the thread shows the same recipe twice, one copy still offering to save it.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/utils/chatArtifacts.check.ts --outDir /tmp/ca --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/ca/chatArtifacts.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` typechecks it.
 */
import { strict as assert } from 'assert';
import { ChatArtifact, ChatMessage, RecipeDocument } from '../types';
import { foldArtifact } from './chatArtifacts';

const doc = (title: string): RecipeDocument => ({
  title, summary: null, language: 'en', tags: [], ingredients: [], steps: [],
});

const draft = (ref: string, title = 'Pasta'): ChatArtifact =>
  ({ kind: 'recipe', data: { draft_ref: ref, document: doc(title) } });

const saved = (ref: string, id: number, title = 'Pasta'): ChatArtifact =>
  ({ kind: 'recipe', data: { draft_ref: ref, recipe_id: id, document: doc(title) } });

const thread = (...artifacts: ChatArtifact[][]): ChatMessage[] =>
  artifacts.map((a) => ({ role: 'assistant' as const, text: '', artifacts: a }));

const all = (messages: ChatMessage[]) => messages.flatMap((m) => m.artifacts ?? []);

export function check(): void {
  // A new draft lands on the turn in progress — the last message.
  const one = foldArtifact(thread([], []), draft('draft_1'));
  assert.equal(all(one).length, 1);
  assert.equal(one[0].artifacts?.length, 0);

  // The save arrives a turn later ("save it"): the original card is updated where it is,
  // and no second card appears.
  const later = foldArtifact(thread([draft('draft_1')], []), saved('draft_1', 42));
  const cards = all(later);
  assert.equal(cards.length, 1, 'a save must not append a second card');
  assert.equal(cards[0].kind === 'recipe' && cards[0].data.recipe_id, 42);
  assert.equal(later[0].artifacts?.length, 1, 'the card stays on the turn that produced it');

  // A different draft is a different recipe, appended to the current turn.
  const two = foldArtifact(thread([draft('draft_1')], []), draft('draft_2', 'Soup'));
  assert.equal(all(two).length, 2);

  // A week plan carries no draft_ref and is always appended, even twice.
  const plan: ChatArtifact = {
    kind: 'week_plan',
    data: { starts_on: '2026-09-14', ends_on: '2026-09-20', plan: { status: 'applied', message: '', low_variety: false, assignments: [] } },
  };
  assert.equal(all(foldArtifact(foldArtifact(thread([]), plan), plan)).length, 2);

  console.log('chatArtifacts.check: all assertions passed');
}

check();
