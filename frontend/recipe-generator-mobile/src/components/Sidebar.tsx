import React, { useMemo } from 'react';
import { View, Text, StyleSheet, TouchableOpacity } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { useTheme, Theme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { type } from '../theme';
import { NAV_ITEMS, HISTORY_NAV_ITEM, PROFILE_NAV_ITEM, NavItem } from '../navigation/navItems';
import { RootStackParamList } from '../navigation/AppNavigator';

/**
 * The wide-web nav rail. BACKLOG 5.7 deleted its `drawer` variant — narrow uses bottom tabs now —
 * and with it the route-less "Soon" pill, since every nav entry has a route.
 */
interface Props {
  activeRoute: string;
  onNavigate: (route: keyof RootStackParamList) => void;
  collapsed?: boolean;
  onToggleCollapse?: () => void;
  /** Opens the report sheet (BACKLOG 9.16). Not a nav item — it goes nowhere. */
  onReport?: () => void;
}

const Sidebar: React.FC<Props> = ({ activeRoute, onNavigate, collapsed = false, onToggleCollapse, onReport }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  const renderItem = (item: NavItem) => {
    const active = item.route === activeRoute;
    return (
      <TouchableOpacity
        key={item.key}
        style={[styles.item, collapsed && styles.itemCollapsed, active && styles.itemActive]}
        onPress={() => onNavigate(item.route)}
        accessibilityRole="tab"
        accessibilityState={{ selected: active }}
        accessibilityLabel={t(item.labelKey)}
      >
        <Ionicons name={item.icon} size={19} color={active ? theme.accent : theme.subtext} style={[!collapsed && styles.itemIcon]} />
        {!collapsed && <Text style={[styles.itemLabel, active && styles.itemLabelActive]} numberOfLines={1}>{t(item.labelKey)}</Text>}
      </TouchableOpacity>
    );
  };

  return (
    <View style={[styles.rail, collapsed && styles.railCollapsed]}>
      <View style={[styles.topRow, collapsed && styles.topRowCollapsed]}>
        {!collapsed && <Text style={styles.logo}>RecipeGenerator</Text>}
        <TouchableOpacity onPress={onToggleCollapse} hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}>
          <Ionicons
            name={collapsed ? 'chevron-forward' : 'chevron-back'}
            size={18}
            color={theme.subtext}
            accessibilityLabel={t(collapsed ? 'nav.expand' : 'nav.collapse')}
          />
        </TouchableOpacity>
      </View>
      <View style={styles.list}>
        {NAV_ITEMS.map(renderItem)}
      </View>
      <View style={styles.footer}>
        {onReport && (
          // Above Profile, and not in NAV_ITEMS: this opens a sheet rather than navigating,
          // so it is not a destination and must not render as a selected tab.
          <TouchableOpacity
            style={[styles.item, collapsed && styles.itemCollapsed]}
            onPress={onReport}
            accessibilityRole="button"
            accessibilityLabel={t('nav.report')}
          >
            <Ionicons name="megaphone-outline" size={19} color={theme.subtext} style={[!collapsed && styles.itemIcon]} />
            {!collapsed && <Text style={styles.itemLabel} numberOfLines={1}>{t('nav.report')}</Text>}
          </TouchableOpacity>
        )}
        {renderItem(HISTORY_NAV_ITEM)}
        {renderItem(PROFILE_NAV_ITEM)}
      </View>
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  rail: {
    width: 240,
    backgroundColor: t.surface,
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: t.border,
    paddingTop: 24,
    paddingHorizontal: 12,
  },
  railCollapsed: {
    width: 64,
    paddingHorizontal: 8,
  },
  topRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 8,
    marginBottom: 16,
  },
  topRowCollapsed: {
    justifyContent: 'center',
    paddingHorizontal: 0,
  },
  logo: {
    ...type.title, fontSize: 16,
    lineHeight: 22,
    color: t.text,
  },
  list: {
    gap: 2,
  },
  footer: {
    marginTop: 'auto',
    paddingTop: 12,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
  },
  item: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingVertical: 10,
    paddingHorizontal: 8,
    borderRadius: 8,
  },
  itemCollapsed: {
    justifyContent: 'center',
    paddingHorizontal: 0,
  },
  itemActive: {
    backgroundColor: t.accentFaded,
  },
  itemIcon: {
    marginRight: 10,
  },
  itemLabel: {
    ...type.label,
    color: t.subtext,
    flex: 1,
  },
  // The family already carries the weight (5.6); active only changes colour.
  itemLabelActive: {
    color: t.accent,
  },
});

export default Sidebar;
