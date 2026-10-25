import { Platform, StyleSheet } from 'react-native';
import { Theme } from '../../context/ThemeContext';
import { type } from '../../theme';

// Chrome shared by the cooking-mode phases (BACKLOG 5.0 split one screen into four).
// Phase 5.3 replaces these with primitives; same StyleSheet as before, only shared.
export const makeChromeStyles = (t: Theme) => StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: t.bg,
  },
  centered: {
    justifyContent: 'center',
    alignItems: 'center',
    padding: 30,
  },
  loadingText: {
    marginTop: 16,
    ...type.title, fontSize: 17, lineHeight: 24,
    color: t.text,
  },
  loadingSubtext: {
    marginTop: 6,
    ...type.body, fontSize: 14,
    color: t.muted,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingTop: Platform.OS === 'ios' ? 56 : 20,
    paddingBottom: 12,
    backgroundColor: t.bg,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: t.border,
  },
  headerSide: {
    width: 60,
  },
  headerAction: {
    ...type.label, fontSize: 16,
    color: t.accent,
  },
  headerTitle: {
    flex: 1,
    ...type.title, fontSize: 17, lineHeight: 24,
    color: t.text,
    textAlign: 'center',
  },
  scrollView: {
    flex: 1,
  },
  scrollContent: {
    padding: 20,
    paddingBottom: 40,
  },
  footer: {
    padding: 16,
    paddingBottom: Platform.OS === 'ios' ? 28 : 16,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
    backgroundColor: t.bg,
  },
  primaryBtn: {
    backgroundColor: t.accent,
    borderRadius: 14,
    paddingVertical: 16,
    alignItems: 'center',
    marginTop: 16,
  },
  primaryBtnText: {
    color: t.onAccent,
    ...type.label, fontSize: 16,
  },
  secondaryBtn: {
    borderRadius: 14,
    paddingVertical: 14,
    alignItems: 'center',
    borderWidth: 1.5,
    borderColor: t.border,
    marginTop: 10,
  },
  secondaryBtnText: {
    color: t.subtext,
    ...type.label, fontSize: 15,
  },
  btnDisabled: {
    opacity: 0.5,
  },
});
