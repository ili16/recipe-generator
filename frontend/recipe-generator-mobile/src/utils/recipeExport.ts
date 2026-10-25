// BACKLOG 13.1 — get your recipes out, as Markdown.
//
// There is nothing to render: `recipes.content` *is* the markdown (the backend's
// RenderMarkdown writes it on every save and every edit) and the library list already
// carries it. So an export is a join, not a formatter — which is why this file is
// twelve lines and has no backend half.
//
// Pure on purpose, so recipeExport.check.ts runs under plain node.

export interface ExportableRecipe {
  recipename: string;
  recipe: string;
}

/**
 * One markdown document for the given recipes, separated by a horizontal rule.
 * A recipe whose content has lost its `# Title` heading (hand-edited, or saved
 * before the renderer wrote one) gets it back, so every entry is navigable.
 */
export function buildMarkdownExport(recipes: ExportableRecipe[]): string {
  return recipes
    .map(r => {
      const body = r.recipe.trim();
      return body.startsWith('# ') ? body : `# ${r.recipename}\n\n${body}`;
    })
    .join('\n\n---\n\n') + '\n';
}

/** `pasta-al-limone.md` for one recipe, `recipes-2026-09-20.md` for a library. */
export function exportFilename(recipes: ExportableRecipe[], today: Date): string {
  if (recipes.length === 1) {
    const slug = recipes[0].recipename
      .toLowerCase()
      .normalize('NFD')
      .replace(/[̀-ͯ]/g, '')
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-|-$/g, '');
    if (slug) return `${slug}.md`;
  }
  const iso = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`;
  return `recipes-${iso}.md`;
}
