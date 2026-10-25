import React, { useState, useMemo } from 'react';
import { View, StyleSheet } from 'react-native';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import Loading from '../components/Loading';
import { useTheme, Theme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { useEscapeBack } from '../hooks/useEscapeBack';
import { useIsAuthenticated } from '../hooks/useIsAuthenticated';
import { SignInRequired } from '../components/ui';
import WeekView from './mealplan/WeekView';
import TodayBanner from './mealplan/TodayBanner';
import { MealPlanProvider } from '../context/MealPlanContext';

type Props = NativeStackScreenProps<RootStackParamList, 'MealPlan'>;

// Thin shell: auth gate plus the week the planner is centered on. Week is the only view —
// the Day timeline and Month grid were deleted (BACKLOG 4.1); neither showed what a day
// was planned for, and only Week has the AI suggest/chat flow.
const MealPlanScreen: React.FC<Props> = ({ navigation }) => {
  const isAuthenticated = useIsAuthenticated();
  const [selectedDate, setSelectedDate] = useState(() => new Date());
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  useEscapeBack();

  if (isAuthenticated === null) {
    return <Loading visible message={t('common.loading')} />;
  }

  if (!isAuthenticated) {
    return (
      <SignInRequired
        message={t('plan.signInToPlan')}
        onSignIn={() => navigation.navigate('Login')}
      />
    );
  }

  return (
    <MealPlanProvider>
      <View style={styles.container}>
        <TodayBanner navigation={navigation} />
        <WeekView selectedDate={selectedDate} onChangeDate={setSelectedDate} navigation={navigation} />
      </View>
    </MealPlanProvider>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: { flex: 1, backgroundColor: t.bg },
});

export default MealPlanScreen;
