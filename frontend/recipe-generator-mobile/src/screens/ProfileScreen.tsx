import React, { useState, useEffect, useMemo } from 'react';
import {
  View,
  Text,
  StyleSheet,
  TouchableOpacity,
} from 'react-native';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import apiService from '../services/apiService';
import authService from '../services/authService';
import { UserProfile, Usage } from '../types';
import Loading from '../components/Loading';
import { useTheme, Theme, ThemeMode } from '../context/ThemeContext';
import { type } from '../theme';
import { useEscapeBack } from '../hooks/useEscapeBack';
import { Chip } from '../components/ui';
import { useAlert } from '../context/AlertContext';
import { useLanguage, LanguageMode } from '../context/LanguageContext';
import { formatUSD, resetsOn } from '../utils/budget';
import { currentLocale } from '../i18n';

type Props = NativeStackScreenProps<RootStackParamList, 'Profile'>;



const ProfileScreen: React.FC<Props> = ({ navigation }) => {
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  const [usage, setUsage] = useState<Usage | null>(null);
  const { theme, mode, setMode } = useTheme();
  const { t, mode: langMode, setMode: setLangMode } = useLanguage();
  const { showAlert } = useAlert();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  useEscapeBack();

  const appearance = (
    <View style={styles.field}>
      <Text style={styles.label}>{t('profile.appearance')}</Text>
      <View style={styles.modeRow}>
        {(['system', 'light', 'dark'] as ThemeMode[]).map((m) => (
          <Chip
            key={m}
            label={t(`profile.theme.${m}`)}
            selected={mode === m}
            onPress={() => setMode(m)}
          />
        ))}
      </View>
    </View>
  );

  // The two languages name themselves — someone who has landed in the wrong one still has to
  // recognise their way out.
  const language = (
    <View style={styles.field}>
      <Text style={styles.label}>{t('profile.language')}</Text>
      <View style={styles.modeRow}>
        {(['system', 'en', 'de'] as LanguageMode[]).map((m) => (
          <Chip
            key={m}
            label={m === 'system' ? t('profile.theme.system') : m === 'en' ? 'English' : 'Deutsch'}
            selected={langMode === m}
            onPress={() => setLangMode(m)}
          />
        ))}
      </View>
    </View>
  );

  useEffect(() => {
    loadProfile();
  }, []);

  const loadProfile = async () => {
    setLoading(true);
    try {
      const authenticated = await authService.isAuthenticated();
      setIsAuthenticated(authenticated);
      if (authenticated) {
        const userProfile = await authService.getUserProfile();
        setProfile(userProfile);
        // There is a monthly cap that can refuse a turn, and until now the only way to
        // learn it existed was to hit it (BACKLOG.md 10.5). Not awaited into the failure
        // path: a profile that loads without the number is better than one that errors.
        apiService.getUsage().then(setUsage, () => setUsage(null));
      }
    } catch (error) {
      console.error('Error loading profile:', error);
      showAlert(t('common.error'), t('profile.loadFailed'), 'error');
    } finally {
      setLoading(false);
    }
  };

  const handleLogout = async () => {
    setLoading(true);
    try {
      await authService.logout();
      apiService.cachedPreferences = null;
      setIsAuthenticated(false);
      setProfile(null);
      setUsage(null);
      navigation.navigate('Chat');
    } catch (error) {
      console.error('Logout error:', error);
      showAlert(t('common.error'), t('profile.logoutFailed'), 'error');
    } finally {
      setLoading(false);
    }
  };

  if (loading) {
    return <Loading visible={true} message={t('common.loading')} />;
  }

  if (!isAuthenticated) {
    return (
      <View style={styles.centered}>
        <View style={styles.card}>
          <Text style={styles.cardTitle}>{t('profile.notSignedIn')}</Text>
          <Text style={styles.muted}>{t('profile.signInBlurb')}</Text>
          {appearance}
          {language}
          <TouchableOpacity style={styles.primaryButton} onPress={() => navigation.navigate('Login')}>
            <Text style={styles.primaryButtonText}>{t('nav.login')}</Text>
          </TouchableOpacity>
          <TouchableOpacity style={styles.ghostButton} onPress={() => navigation.navigate('Chat')}>
            <Text style={styles.ghostButtonText}>{t('profile.continueAnonymously')}</Text>
          </TouchableOpacity>
        </View>
      </View>
    );
  }

  return (
    <View style={styles.centered}>
      <View style={styles.card}>
        <Text style={styles.cardTitle}>{t('nav.profile')}</Text>

        <View style={styles.field}>
          <Text style={styles.label}>{t('profile.name')}</Text>
          <Text style={styles.value}>{profile?.name || '—'}</Text>
        </View>

        {profile?.email ? (
          <View style={styles.field}>
            <Text style={styles.label}>{t('profile.email')}</Text>
            <Text style={styles.value}>{profile.email}</Text>
          </View>
        ) : null}

        {usage && (
          <View style={styles.field}>
            <Text style={styles.label}>{t('profile.budget')}</Text>
            <Text style={styles.value}>
              {t('profile.budgetSpent', { spent: formatUSD(usage.spent_usd, currentLocale()), cap: formatUSD(usage.cap_usd, currentLocale()) })}
            </Text>
            <Text style={styles.muted}>{t('profile.budgetResets', { date: resetsOn(usage.period_start, currentLocale()) })}</Text>
          </View>
        )}

        {appearance}
        {language}

        <TouchableOpacity style={styles.ghostButton} onPress={() => navigation.navigate('Preferences')}>
          <Text style={styles.ghostButtonText}>{t('nav.preferences')}</Text>
        </TouchableOpacity>

        <TouchableOpacity style={styles.ghostButton} onPress={() => navigation.navigate('Household')}>
          <Text style={styles.ghostButtonText}>{t('nav.household')}</Text>
        </TouchableOpacity>

        <TouchableOpacity style={styles.primaryButton} onPress={handleLogout}>
          <Text style={styles.primaryButtonText}>{t('profile.logout')}</Text>
        </TouchableOpacity>
      </View>
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  centered: {
    flex: 1,
    backgroundColor: t.bg,
    justifyContent: 'center',
    alignItems: 'center',
    padding: 24,
  },
  card: {
    width: '100%',
    maxWidth: 380,
    backgroundColor: t.surface,
    borderRadius: 16,
    padding: 28,
    borderWidth: 1.5,
    borderColor: t.accent,
    gap: 16,
  },
  cardTitle: {
    ...type.title,
    color: t.text,
    marginBottom: 4,
  },
  muted: {
    ...type.body, fontSize: 14,
    color: t.muted,
    lineHeight: 20,
  },
  field: {
    gap: 4,
  },
  label: {
    ...type.label, fontSize: 12,
    lineHeight: 16,
    color: t.muted,
    textTransform: 'uppercase',
    letterSpacing: 0.6,
  },
  value: {
    ...type.body, fontSize: 16,
    color: t.text,
  },
  modeRow: {
    flexDirection: 'row',
    gap: 8,
  },
  primaryButton: {
    backgroundColor: t.accent,
    padding: 14,
    borderRadius: 10,
    alignItems: 'center',
    marginTop: 4,
  },
  primaryButtonText: {
    color: t.onAccent,
    ...type.label, fontSize: 15,
  },
  ghostButton: {
    padding: 10,
    alignItems: 'center',
  },
  ghostButtonText: {
    color: t.subtext,
    ...type.body, fontSize: 14,
  },
});

export default ProfileScreen;
