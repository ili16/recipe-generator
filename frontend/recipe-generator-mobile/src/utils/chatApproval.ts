import { ChatApproval } from '../types';

// What a pending write actually does, read off the tool call's own arguments
// (BACKLOG.md 10.2). The server sends the tool name and its raw JSON rather than a
// sentence, because the sentence has to exist in every language the UI speaks — so the
// wording lives in the catalogs and this only decides which key and which values.

export interface ApprovalSummary {
  /** Catalog key under `chat.approval.`. */
  key: string;
  /** Interpolation values for that key. */
  values: Record<string, string | number>;
}

// A tool whose arguments we cannot read is still approvable: the user sees the tool's own
// label rather than nothing, which is the same fallback the working indicator uses for an
// unknown tool name.
const parse = (args: string): Record<string, unknown> => {
  try {
    const parsed: unknown = JSON.parse(args || '{}');
    return parsed !== null && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
};

export const describeApproval = (approval: ChatApproval): ApprovalSummary => {
  const args = parse(approval.args);

  if (approval.name === 'apply_week_plan') {
    const days = Array.isArray(args.days) ? args.days : [];
    return { key: 'apply_week_plan', values: { count: days.length } };
  }

  if (approval.name === 'clear_plan_day') {
    const date = typeof args.date === 'string' ? args.date : '';
    const slot = typeof args.meal_slot === 'string' ? args.meal_slot : '';
    // Clearing a whole day and clearing one meal of it are different enough to be
    // different sentences — the first is what the user needs warning about.
    return slot
      ? { key: 'clear_plan_slot', values: { date, slot } }
      : { key: 'clear_plan_day', values: { date } };
  }

  if (approval.name === 'add_pantry_items') {
    // The parsed photo *is* the argument list (BACKLOG.md 17.5), so naming the items
    // here is what makes the approval a review rather than a yes/no on a tool name.
    const items = Array.isArray(args.items) ? args.items : [];
    const names = items
      .map((i) => (i !== null && typeof i === 'object' ? (i as Record<string, unknown>).name : undefined))
      .filter((n): n is string => typeof n === 'string' && n.trim() !== '');
    return { key: 'add_pantry_items', values: { count: names.length, items: names.join(', ') } };
  }

  if (approval.name === 'set_preferences') {
    const patch = args.patch !== null && typeof args.patch === 'object' ? args.patch : {};
    return { key: 'set_preferences', values: { fields: Object.keys(patch).join(', ') } };
  }

  return { key: 'unknown', values: { name: approval.name } };
};
