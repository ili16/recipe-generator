import { ChatArtifact, ChatMessage } from '../types';

// Recipe artifacts are keyed by the draft they describe: the same draft arriving again
// (saved, and so carrying a recipe_id) updates that card wherever in the thread it is.
// Anything else — a saved recipe the agent opened, a week plan — appends to the turn in
// progress, which is always the last message.
export const foldArtifact = (messages: ChatMessage[], incoming: ChatArtifact): ChatMessage[] => {
  const ref = incoming.kind === 'recipe' ? incoming.data.draft_ref : undefined;
  const known = ref != null && messages.some(
    (m) => m.artifacts?.some((a) => a.kind === 'recipe' && a.data.draft_ref === ref)
  );
  if (!known) {
    return messages.map((m, i) =>
      i === messages.length - 1 ? { ...m, artifacts: [...(m.artifacts ?? []), incoming] } : m
    );
  }
  return messages.map((m) =>
    m.artifacts
      ? {
          ...m,
          artifacts: m.artifacts.map((a) =>
            a.kind === 'recipe' && a.data.draft_ref === ref ? incoming : a
          ),
        }
      : m
  );
};
