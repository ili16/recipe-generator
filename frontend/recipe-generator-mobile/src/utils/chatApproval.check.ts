/**
 * Self-check for the approval card's summary (BACKLOG.md 10.2). This is the sentence that
 * tells the user what is about to be written to their meal plan — if it reads the tool's
 * arguments wrong, they approve something other than what they were shown, which is worse
 * than having no approval step at all.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/utils/chatApproval.check.ts --outDir /tmp/cap --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/cap/chatApproval.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` typechecks it.
 */
import { strict as assert } from 'assert';
import { ChatApproval } from '../types';
import { describeApproval } from './chatApproval';

const approval = (name: string, args: string): ChatApproval => ({ id: 'p1', name, args });

export function check(): void {
  // A week is counted from the days the call actually lists, not from "7".
  assert.deepEqual(
    describeApproval(approval('apply_week_plan', '{"days":[{"date":"2026-09-20"},{"date":"2026-09-21"}]}')),
    { key: 'apply_week_plan', values: { count: 2 } }
  );

  // Clearing one meal and clearing a whole day are different sentences: the second is the
  // destructive one, and must not be described as the first.
  assert.deepEqual(
    describeApproval(approval('clear_plan_day', '{"date":"2026-09-20"}')),
    { key: 'clear_plan_day', values: { date: '2026-09-20' } }
  );
  assert.deepEqual(
    describeApproval(approval('clear_plan_day', '{"date":"2026-09-20","meal_slot":"lunch"}')),
    { key: 'clear_plan_slot', values: { date: '2026-09-20', slot: 'lunch' } }
  );

  assert.deepEqual(
    describeApproval(approval('set_preferences', '{"patch":{"dietary_prefs":["vegan"],"household_size":3}}')),
    { key: 'set_preferences', values: { fields: 'dietary_prefs, household_size' } }
  );

  // A scanned shelf is approved by reading the items back, so a nameless or malformed
  // line must not silently pad the count the user is agreeing to.
  assert.deepEqual(
    describeApproval(approval('add_pantry_items', '{"items":[{"name":"milk"},{"name":"eggs"},{"name":"  "},{},7]}')),
    { key: 'add_pantry_items', values: { count: 2, items: 'milk, eggs' } }
  );
  assert.deepEqual(describeApproval(approval('add_pantry_items', '{}')), {
    key: 'add_pantry_items', values: { count: 0, items: '' },
  });

  // Unreadable or surprising arguments must still produce a card with buttons — falling
  // through to a crash would strand the turn with no way to approve or decline it.
  assert.deepEqual(describeApproval(approval('apply_week_plan', 'not json')), {
    key: 'apply_week_plan', values: { count: 0 },
  });
  assert.deepEqual(describeApproval(approval('apply_week_plan', '[1,2]')), {
    key: 'apply_week_plan', values: { count: 0 },
  });
  assert.deepEqual(describeApproval(approval('clear_plan_day', '{"date":null}')), {
    key: 'clear_plan_day', values: { date: '' },
  });
  assert.deepEqual(describeApproval(approval('set_preferences', '{"patch":null}')), {
    key: 'set_preferences', values: { fields: '' },
  });
  assert.deepEqual(describeApproval(approval('some_new_tool', '{}')), {
    key: 'unknown', values: { name: 'some_new_tool' },
  });

  console.log('chatApproval.check: all assertions passed');
}

check();
