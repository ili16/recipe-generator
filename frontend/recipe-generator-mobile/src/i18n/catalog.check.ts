/**
 * Self-check for the catalogs. `de.ts` is typed as `typeof en`, so a *missing* German key is
 * already a `tsc --noEmit` failure — what this catches is the two things the type cannot see:
 * an empty string, and a placeholder that appears in one language but not the other (an
 * untranslated `%{count}` silently renders as nothing).
 *
 * No test runner in this repo (see `AGENT.md`), so this is a plain assert script:
 *
 *   npx tsc src/i18n/catalog.check.ts --outDir /tmp/i18n --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck --resolveJsonModule ; \
 *     node /tmp/i18n/catalog.check.js
 *
 * Nothing imports it, so it never reaches a bundle; `npx tsc --noEmit` typechecks it.
 */
import { strict as assert } from 'assert';
import en from './en';
import de from './de';

type Node = { [key: string]: string | Node };

const flatten = (node: Node, prefix = ''): Record<string, string> =>
  Object.entries(node).reduce<Record<string, string>>((acc, [key, value]) => {
    const path = prefix ? `${prefix}.${key}` : key;
    if (typeof value === 'string') acc[path] = value;
    else Object.assign(acc, flatten(value, path));
    return acc;
  }, {});

const placeholders = (s: string): string[] => (s.match(/%\{\w+\}/g) ?? []).sort();

export function check(): void {
  const flatEn = flatten(en as unknown as Node);
  const flatDe = flatten(de as unknown as Node);

  assert.ok(Object.keys(flatEn).length > 200, 'the catalog should not have shrunk to nothing');
  assert.deepEqual(Object.keys(flatDe).sort(), Object.keys(flatEn).sort(), 'key sets differ');

  for (const [key, value] of Object.entries(flatEn)) {
    assert.ok(value.trim() !== '', `en.${key} is empty`);
    assert.ok(flatDe[key].trim() !== '', `de.${key} is empty`);
    // A dropped interpolation is invisible at runtime: the value just renders without it.
    assert.deepEqual(
      placeholders(flatDe[key]),
      placeholders(value),
      `placeholders differ for ${key}: "${value}" vs "${flatDe[key]}"`,
    );
  }

  // Pluralised entries need both forms in both languages, or i18n-js falls back to the key.
  for (const key of Object.keys(flatEn)) {
    if (!key.endsWith('.one')) continue;
    const other = `${key.slice(0, -4)}.other`;
    assert.ok(other in flatEn, `${key} has no matching .other`);
  }

  console.log(`catalog.check: ${Object.keys(flatEn).length} keys, all assertions passed`);
}

check();
