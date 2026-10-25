import React, { useMemo, useState } from 'react';
import { View, StyleSheet, TouchableOpacity, Platform } from 'react-native';
import { NavigationContainer, createNavigationContainerRef } from '@react-navigation/native';
import { Ionicons } from '@expo/vector-icons';
import { useTheme, Theme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import Sidebar from '../components/Sidebar';
import { Text, useIsDesktopNav } from '../components/ui';
import { space } from '../theme';
import { useKeyboardOpen } from '../hooks/useKeyboardOpen';
import { FeedbackSheet } from '../components/FeedbackSheet';
import { NAV_ITEMS } from './navItems';
import AppNavigator, { RootStackParamList } from './AppNavigator';

const navigationRef = createNavigationContainerRef<RootStackParamList>();

type NavigateFn = (route: keyof RootStackParamList) => void;

/**
 * The narrow shell (BACKLOG 5.7 / DESIGN_SYSTEM §7.6): three fixed bottom tabs, replacing the
 * menu FAB and the slide-in drawer.
 *
 * Hand-rolled rather than `@react-navigation/bottom-tabs`, because the shell already owns the
 * active route and a `navigate`: a real tab navigator would mean nesting the three top-level
 * screens under a second navigator and reworking `RootStackParamList` and the in-screen
 * `navigate('Chat')` calls, to buy a row of buttons.
 *
 * It hides itself while the keyboard is up (`useKeyboardOpen`): on a phone the keyboard already
 * owns half the screen, and the tabs are unreachable behind it anyway.
 */
const TabBar: React.FC<{ activeRoute: string; onNavigate: NavigateFn }> = ({ activeRoute, onNavigate }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  return (
    <View style={styles.tabBar}>
      {NAV_ITEMS.map((item) => {
        const active = item.route === activeRoute;
        return (
          <TouchableOpacity
            key={item.key}
            style={styles.tab}
            onPress={() => onNavigate(item.route)}
            accessibilityRole="tab"
            accessibilityState={{ selected: active }}
            accessibilityLabel={t(item.labelKey)}
          >
            <Ionicons name={item.icon} size={22} color={active ? theme.accent : theme.muted} />
            <Text variant="caption" tone={active ? 'accent' : 'muted'} numberOfLines={1}>{t(item.labelKey)}</Text>
          </TouchableOpacity>
        );
      })}
    </View>
  );
};

/**
 * The narrow top row. The native stack header used to sit here spelling out "Recipe Generator"
 * above every screen — a title the tab bar already gives, on a phone that has none to spare. All
 * that is left is the way into Profile, which narrow has no rail for.
 */
const TopBar: React.FC<{ onPress: () => void; onReport: () => void; onHistory: () => void }> = ({ onPress, onReport, onHistory }) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  return (
    <View style={styles.topBar}>
      <TouchableOpacity
        style={styles.topBtn}
        onPress={onHistory}
        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
        accessibilityRole="button"
        accessibilityLabel={t('chat.history.title')}
      >
        <Ionicons name="time-outline" size={23} color={theme.subtext} />
      </TouchableOpacity>
      <TouchableOpacity
        style={styles.topBtn}
        onPress={onReport}
        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
        accessibilityRole="button"
        accessibilityLabel={t('nav.report')}
      >
        <Ionicons name="megaphone-outline" size={24} color={theme.subtext} />
      </TouchableOpacity>
      <TouchableOpacity
        style={styles.topBtn}
        onPress={onPress}
        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
        accessibilityRole="button"
        accessibilityLabel={t('nav.profile')}
      >
        <Ionicons name="person-circle-outline" size={26} color={theme.subtext} />
      </TouchableOpacity>
    </View>
  );
};

const AppShell: React.FC = () => {
  const { theme } = useTheme();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  const [activeRoute, setActiveRoute] = useState('Chat');
  const [railCollapsed, setRailCollapsed] = useState(false);
  // One sheet for the whole app (BACKLOG 9.16), so the top bar and the rail open the same
  // instance and every screen can reach it without its own copy.
  const [reportOpen, setReportOpen] = useState(false);

  const isDesktopNav = useIsDesktopNav();
  const keyboardOpen = useKeyboardOpen();
  // Cooking mode is immersive and already opts out of the stack header for the same reason.
  const showChrome = activeRoute !== 'CookingMode';
  // Sub-screens (Profile, Preferences, Login) keep their own stack header with its back arrow,
  // so the top row would be a second, redundant way into a screen you are already on.
  const isTabRoute = NAV_ITEMS.some((item) => item.route === activeRoute);

  const navigate: NavigateFn = (route) => {
    // Tabs and rail entries are top-level, so a tap replaces the stack instead of pushing onto
    // it — otherwise Recipes → MealPlan → Recipes leaves a back trail (BACKLOG 0.14). Tapping
    // the active item is a no-op: reset() would remount the screen and drop its state.
    if (navigationRef.isReady() && navigationRef.getCurrentRoute()?.name !== route) {
      navigationRef.reset({ index: 0, routes: [{ name: route }] });
    }
  };

  return (
    <View style={[styles.root, { flexDirection: isDesktopNav && showChrome ? 'row' : 'column' }]}>
      {isDesktopNav && showChrome && (
        <Sidebar
          activeRoute={activeRoute}
          onNavigate={navigate}
          collapsed={railCollapsed}
          onToggleCollapse={() => setRailCollapsed((v) => !v)}
          onReport={() => setReportOpen(true)}
        />
      )}

      <View style={styles.content}>
        {!isDesktopNav && isTabRoute && !keyboardOpen && (
          // Pushed, not reset: Profile is a sub-screen on narrow and keeps its back arrow.
          <TopBar
            onPress={() => navigationRef.isReady() && navigationRef.navigate('Profile')}
            onReport={() => setReportOpen(true)}
            onHistory={() => navigationRef.isReady() && navigationRef.navigate('History')}
          />
        )}

        <NavigationContainer
          ref={navigationRef}
          onStateChange={() => {
            const name = navigationRef.getCurrentRoute()?.name;
            if (name) setActiveRoute(name);
          }}
        >
          <AppNavigator />
        </NavigationContainer>
      </View>

      {!isDesktopNav && showChrome && !keyboardOpen && (
        <TabBar activeRoute={activeRoute} onNavigate={navigate} />
      )}

      <FeedbackSheet visible={reportOpen} onClose={() => setReportOpen(false)} route={activeRoute} />
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: t.bg,
  },
  content: {
    flex: 1,
  },
  topBar: {
    flexDirection: 'row',
    justifyContent: 'flex-end',
    gap: space.xs,
    paddingHorizontal: space.md,
    // No SafeAreaProvider: the status-bar inset is a constant, as it is for the tab bar below.
    // Web sits under the browser chrome and needs none.
    paddingTop: Platform.OS === 'ios' ? 44 : space.sm, // status-bar height
    paddingBottom: space.xs,
    backgroundColor: t.bg,
  },
  topBtn: {
    width: 36,
    height: 36,
    alignItems: 'center',
    justifyContent: 'center',
  },
  tabBar: {
    flexDirection: 'row',
    backgroundColor: t.surface,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
    paddingTop: space.sm,
    // No SafeAreaProvider in this app, so the home-indicator inset is a constant, as it already
    // was for the FAB this replaced.
    paddingBottom: Platform.OS === 'ios' ? space.xl : space.sm,
  },
  tab: {
    flex: 1,
    alignItems: 'center',
    gap: space.xs,
  },
});

export default AppShell;
