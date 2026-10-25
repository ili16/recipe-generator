/**
 * Design tokens — BACKLOG 5.1. Spec: `specs/DESIGN_SYSTEM.md` §7.2 (palette, which supersedes §2)
 * and §3 + §7.3 (scales).
 *
 * The only place in `src/` allowed to hold a colour literal (§6). Every semantic colour ships its
 * `on*` pair; red means destructive and nothing else.
 *
 * Contrast, measured with a WCAG checker rather than by eye (AA = 4.5:1 for body text). Light:
 * text/bg 14.2, subtext/bg 8.7, muted/bg 4.6, onPrimary/primary 7.2, onAccent/accent 5.4,
 * onDanger/danger 6.5, onWarning/warning 5.5, onInfo/info 5.2. Dark: text/bg 15.7, subtext/bg 7.0,
 * muted/bg 5.4, onPrimary/primary 8.1, onAccent/accent 6.8, onDanger/danger 10.4.
 *
 * Three deliberate departures from §7.2's table, all of them to clear AA — the mockups are
 * light-only and were never contrast-checked:
 *  - `accent` is #AB4E34, not the mockup's #C86446 (white on #C86446 is 3.9 and it is 3.6 as text).
 *    The darkened value works as a fill *and* as text, so §2's second accent token stays deleted.
 *  - `muted` is darkened in light and lightened in dark; the table's values were ~3.4 and ~3.7.
 *  - The whole dark column is a derivation, not a design. `primary`/`accent`/`danger` are lightened
 *    there because the light values are 2.3:1 and 4.5:1 against #1A1816.
 */

export interface Theme {
  dark: boolean;

  bg: string;
  surface: string;
  surfaceRaised: string;
  border: string;

  text: string;
  subtext: string;
  muted: string;

  primary: string;
  onPrimary: string;
  primaryFaded: string;
  primaryDeep: string; // headers and bars — §7.2's note on DESIGN.md's inverted frontmatter
  onPrimaryDeep: string;

  accent: string;
  onAccent: string;
  accentFaded: string;

  danger: string;
  onDanger: string;
  success: string;
  onSuccess: string;
  warning: string;
  onWarning: string;
  info: string;
  onInfo: string;

  /** Modal backdrop — the dialog behind it is still meant to be read. */
  overlay: string;
  /** Full-screen immersive cover (voice capture, blocking spinner): dark in both themes. */
  scrim: string;
  onScrim: string;

  /** iOS `shadowColor`; elevation is still not tokenised. */
  shadow: string;
}

export const lightTheme: Theme = {
  dark: false,

  bg: '#F9F6F0',
  surface: '#FFFFFF',
  surfaceRaised: '#F2EDE4',
  border: '#E5DEC9',

  text: '#232624',
  subtext: '#424842',
  muted: '#637463',

  primary: '#3A5A40',
  onPrimary: '#F9F6F0',
  primaryFaded: '#EEF2EE',
  primaryDeep: '#23422A',
  onPrimaryDeep: '#F9F6F0',

  accent: '#AB4E34',
  onAccent: '#FFFFFF',
  accentFaded: '#F9EBE7',

  danger: '#BA1A1A',
  onDanger: '#FFFFFF',
  success: '#3A5A40',
  onSuccess: '#F9F6F0',
  warning: '#D97706',
  onWarning: '#1C1917',
  info: '#2563EB',
  onInfo: '#FFFFFF',

  overlay: 'rgba(35,38,36,0.45)',
  scrim: 'rgba(0,0,0,0.92)',
  onScrim: '#FFFFFF',

  shadow: '#000000',
};

export const darkTheme: Theme = {
  dark: true,

  bg: '#1A1816',
  surface: '#232020',
  surfaceRaised: '#2C2927',
  border: '#3A3532',

  text: '#F5F1EC',
  subtext: '#A8A29E',
  muted: '#948D87',

  primary: '#8FB996',
  onPrimary: '#1A1816',
  primaryFaded: '#1E2A20',
  primaryDeep: '#23422A',
  onPrimaryDeep: '#F5F1EC',

  accent: '#E08A6C',
  onAccent: '#1A1816',
  accentFaded: '#2E1D11',

  danger: '#FFB4AB',
  onDanger: '#1A1816',
  success: '#8FB996',
  onSuccess: '#1A1816',
  warning: '#EBB24E',
  onWarning: '#1A1816',
  info: '#93C5FD',
  onInfo: '#1A1816',

  overlay: 'rgba(0,0,0,0.6)',
  scrim: 'rgba(0,0,0,0.92)',
  onScrim: '#FFFFFF',

  shadow: '#000000',
};

export const space = { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 } as const;

/** §7.3 widens §3's scale: cards are `lg`, pills and primary buttons `full`, inputs `md`. */
export const radius = { sm: 4, md: 8, lg: 16, xl: 24, full: 999 } as const;

/**
 * The four font faces the `type` scale below needs — §7.3's Epilogue for headlines, Plus Jakarta
 * Sans for body and labels. Only these four: the spec lists seven weights, the five variants use
 * four. `App.tsx` passes this to `useFonts`; nothing else loads a font.
 */
export { fontAssets } from './fonts';

/**
 * Five variants, mapped onto the mockups' twelve per §7.3. Line heights are part of the scale.
 *
 * The weight lives in the **family name**, not in `fontWeight`: these are static single-weight
 * files, so asking for `fontWeight: '700'` on top of `Epilogue_700Bold` gets faux-bold on web on
 * top of a face that is already bold.
 */
export const type = {
  display: { fontFamily: 'Epilogue_700Bold', fontSize: 26, lineHeight: 34 },
  title: { fontFamily: 'Epilogue_600SemiBold', fontSize: 20, lineHeight: 28 },
  body: { fontFamily: 'PlusJakartaSans_400Regular', fontSize: 15, lineHeight: 24 },
  label: { fontFamily: 'PlusJakartaSans_600SemiBold', fontSize: 14, lineHeight: 20 },
  caption: { fontFamily: 'PlusJakartaSans_400Regular', fontSize: 12, lineHeight: 16 },
} as const;

export const motion = { fast: 120, base: 200, slow: 320 } as const;

/**
 * ONE breakpoint. `AppShell` used 900 and `ChatScreen` 720; 900 wins, and 720 survives as what it
 * always really was — the reading-column cap, not a second breakpoint.
 */
export const layout = { breakpointWide: 900, contentMaxWidth: 720 } as const;
