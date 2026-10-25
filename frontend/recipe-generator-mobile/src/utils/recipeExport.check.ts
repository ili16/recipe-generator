/**
 * Self-check for the Markdown export (BACKLOG 13.1). What can be wrong: a recipe
 * losing its heading, two recipes running into each other, and a title that slugs
 * to nothing taking the filename with it.
 *
 *   npx tsc src/utils/recipeExport.check.ts --outDir /tmp/rx --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/rx/recipeExport.check.js
 */
import { strict as assert } from 'assert';
import { buildMarkdownExport, exportFilename } from './recipeExport';

export function check(): void {
  const pasta = { recipename: 'Pasta al Limone', recipe: '# Pasta al Limone\n\n## Ingredients\n\n- 200 g pasta\n' };
  const soup = { recipename: 'Soup', recipe: '## Ingredients\n\n- 1 onion\n' };

  // A document that already has its heading is passed through, not re-titled.
  const one = buildMarkdownExport([pasta]);
  assert.equal(one.match(/^# Pasta al Limone$/gm)!.length, 1);
  assert.ok(one.includes('- 200 g pasta'), 'quantities survive');
  assert.ok(one.endsWith('\n'));

  // A headless one gets its title back.
  assert.ok(buildMarkdownExport([soup]).startsWith('# Soup\n\n## Ingredients'));

  // Two recipes are separated, and neither is swallowed.
  const both = buildMarkdownExport([pasta, soup]);
  assert.ok(both.includes('\n\n---\n\n'));
  assert.equal(both.match(/^# /gm)!.length, 2);

  // Filenames: one recipe is named after itself, a library after the day.
  const day = new Date(2026, 8, 20);
  assert.equal(exportFilename([pasta], day), 'pasta-al-limone.md');
  assert.equal(exportFilename([{ recipename: 'Crème Brûlée', recipe: 'x' }], day), 'creme-brulee.md');
  assert.equal(exportFilename([pasta, soup], day), 'recipes-2026-09-20.md');
  // A title with nothing sluggable in it falls back rather than producing ".md".
  assert.equal(exportFilename([{ recipename: '中華丼', recipe: 'x' }], day), 'recipes-2026-09-20.md');

  console.log('recipeExport.check: ok');
}

check();
