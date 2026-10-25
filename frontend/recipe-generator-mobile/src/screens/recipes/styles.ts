import { StyleSheet } from 'react-native';
import { Theme } from '../../context/ThemeContext';
import { type } from '../../theme';

// Styles used by more than one of the pieces RecipesScreen was split into
// (BACKLOG 5.0). Phase 5.3 replaces these with real primitives; this is the same
// StyleSheet as before, only shared instead of copied.
export const makeSharedStyles = (t: Theme) => StyleSheet.create({
  btnDisabled: {
    opacity: 0.45,
  },
  fieldLabel: {
    ...type.label, fontSize: 12,
    lineHeight: 16,
    color: t.muted,
    marginTop: 10,
    marginBottom: 4,
  },
  sectionLabel: {
    ...type.label,
    color: t.text,
    marginTop: 16,
    marginBottom: 6,
  },
  fieldInput: {
    backgroundColor: t.bg,
    borderWidth: 1,
    borderColor: t.border,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
    ...type.body, fontSize: 14,
    color: t.text,
  },
  fieldInputMultiline: {
    minHeight: 60,
    textAlignVertical: 'top',
  },
  fieldRow: {
    flexDirection: 'row',
    gap: 8,
  },
  fieldCol: {
    flex: 1,
  },
  filterGroup: {
    marginBottom: 10,
    gap: 6,
  },
  filterGroupLabel: {
    ...type.label, fontSize: 11,
    lineHeight: 15,
    color: t.muted,
    textTransform: 'uppercase',
    letterSpacing: 0.8,
  },
  tagRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 6,
  },
  cardActions: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    justifyContent: 'flex-end',
    gap: 10,
  },
  cookButton: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: t.surface,
    borderWidth: 1,
    borderColor: t.accent,
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderRadius: 8,
  },
  cookButtonText: {
    color: t.accent,
    ...type.label, fontSize: 13,
    letterSpacing: 0.2,
  },
  deleteButton: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: t.accent,
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderRadius: 8,
  },
  deleteButtonText: {
    color: t.onAccent,
    ...type.label, fontSize: 13,
    letterSpacing: 0.2,
  },
  refineBox: {
    marginBottom: 15,
    gap: 8,
  },
});
