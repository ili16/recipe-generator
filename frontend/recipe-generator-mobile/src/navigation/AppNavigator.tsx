import React from 'react';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { type } from '../theme';
import { useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { useIsDesktopNav } from '../components/ui';

// Screens - we'll create these next
import ChatScreen from '../screens/ChatScreen';
import RecipesScreen from '../screens/RecipesScreen';
import ProfileScreen from '../screens/ProfileScreen';
import PreferencesScreen from '../screens/PreferencesScreen';
import LoginScreen from '../screens/LoginScreen';
import CookingModeScreen from '../screens/CookingModeScreen';
import MealPlanScreen from '../screens/MealPlanScreen';
import GroceryListScreen from '../screens/GroceryListScreen';
import PantryScreen from '../screens/PantryScreen';
import HouseholdScreen from '../screens/HouseholdScreen';
import ThreadListScreen from '../screens/chat/ThreadList';
import MapScreen from '../screens/MapScreen';
import { Recipe } from '../types';

export type RootStackParamList = {
  Login: undefined;
  // `threadId` reopens an older conversation (BACKLOG.md 10.7); `prompt` is sent as a
  // user turn on arrival (the planner hands a day or a week over to the thread). The
  // screen clears each param so neither re-fires.
  Chat: { prompt?: string; threadId?: string } | undefined;
  Recipes: undefined;
  Profile: undefined;
  Preferences: undefined;
  Household: undefined;
  History: undefined;
  CookingMode: { recipe: Recipe };
  MealPlan: undefined;
  GroceryList: undefined;
  Pantry: undefined;
  Map: undefined;
};

const Stack = createNativeStackNavigator<RootStackParamList>();

// Tab / rail destinations are top-level: the shell is what moves between them, so the stack's
// back button is noise (and lies about where "back" goes). Sub-screens reached from inside a
// screen — Preferences, Login, CookingMode — keep theirs.
const topLevelWide = { headerBackVisible: false } as const;

// On narrow there is no header at all: the tab bar already names the screen, and a 56px bar
// repeating it costs a third of the composer's height on a phone. `AppShell`'s `TopBar` carries
// the one thing the header still held — the way into Profile.
const topLevelNarrow = { headerShown: false } as const;

const AppNavigator: React.FC = () => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const isDesktopNav = useIsDesktopNav();

  const topLevel = isDesktopNav ? topLevelWide : topLevelNarrow;

  return (
    <Stack.Navigator
      initialRouteName="Chat"
      screenOptions={{
        headerStyle: { backgroundColor: theme.bg },
        headerTintColor: theme.accent,
        // The native header takes family/size/weight only — no lineHeight.
        headerTitleStyle: { fontFamily: type.title.fontFamily, fontSize: 17, color: theme.text },
        headerShadowVisible: false,
      }}
    >
      <Stack.Screen
        name="Chat"
        component={ChatScreen}
        options={{ title: t('nav.appTitle'), ...topLevel }}
      />
      <Stack.Screen
        name="Login"
        component={LoginScreen}
        options={{ title: t('nav.login') }}
      />
      <Stack.Screen
        name="Recipes"
        component={RecipesScreen}
        options={{ title: t('nav.recipes'), ...topLevel }}
      />
      <Stack.Screen
        name="Profile"
        component={ProfileScreen}
        // Top-level on wide (a rail entry, reset to). On narrow it is *pushed* by the header
        // button above, so it keeps its back arrow — it is not a tab to return from.
        options={{ title: t('nav.profile'), headerBackVisible: !isDesktopNav }}
      />
      <Stack.Screen
        name="Preferences"
        component={PreferencesScreen}
        options={{ title: t('nav.preferences') }}
      />
      <Stack.Screen
        name="History"
        component={ThreadListScreen}
        options={{ title: t('chat.history.title') }}
      />
      <Stack.Screen name="Map" component={MapScreen} options={{ title: t('nav.map') }} />
      <Stack.Screen
        name="Household"
        component={HouseholdScreen}
        options={{ title: t('nav.household') }}
      />
      <Stack.Screen
        name="CookingMode"
        component={CookingModeScreen}
        options={{ headerShown: false }}
      />
      <Stack.Screen
        name="MealPlan"
        component={MealPlanScreen}
        options={{ title: t('nav.mealplan'), ...topLevel }}
      />
      <Stack.Screen
        name="GroceryList"
        component={GroceryListScreen}
        options={{ title: t('nav.grocery'), ...topLevel }}
      />
      <Stack.Screen
        name="Pantry"
        component={PantryScreen}
        options={{ title: t('nav.pantry'), ...topLevel }}
      />
    </Stack.Navigator>
  );
};

export default AppNavigator;
