import React, { useState, useMemo } from 'react';
import {
  View,
  Text,
  StyleSheet,
  TouchableOpacity,
  Alert,
  Platform,
} from 'react-native';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import authService, { AuthMode } from '../services/authService';
import Loading from '../components/Loading';
import { useTheme, Theme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { type } from '../theme';

type Props = NativeStackScreenProps<RootStackParamList, 'Login'>;

const LoginScreen: React.FC<Props> = ({ navigation }) => {
  const [loading, setLoading] = useState(false);
  const { theme } = useTheme();
  const { t, hydrateFromAccount } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  const handleLogin = async (mode: AuthMode) => {
    setLoading(true);
    try {
      const profile = await authService.login(mode);
      if (Platform.OS === 'web') {
        // Web login redirects the whole page to Keycloak; the app reloads
        // once the user comes back, so there's nothing left to do here.
        return;
      }
      if (profile) {
        // Native only — web reloads through App's own hydration. Their account may read in a
        // different language than this device does (BACKLOG 14.1).
        hydrateFromAccount();
        Alert.alert(t('login.welcome'), t('login.loggedInAs', { name: profile.name }));
        navigation.goBack();
      } else {
        Alert.alert(t('login.failed'), t('login.failedBody'));
      }
    } catch (error) {
      console.error('Login error:', error);
      Alert.alert(t('common.error'), t('login.errorBody'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <View style={styles.container}>
      <View style={styles.content}>
        <Text style={styles.title}>{t('nav.login')}</Text>
        <Text style={styles.subtitle}>
          {t('login.subtitle')}
        </Text>

        <TouchableOpacity
          style={[styles.authButton, styles.primaryButton]}
          onPress={() => handleLogin('login')}
          disabled={loading}
        >
          <Text style={styles.primaryButtonText}>{t('login.logIn')}</Text>
        </TouchableOpacity>

        <TouchableOpacity
          style={[styles.authButton, styles.googleButton]}
          onPress={() => handleLogin('signup')}
          disabled={loading}
        >
          <Text style={styles.authButtonText}>{t('login.signUp')}</Text>
        </TouchableOpacity>

        <TouchableOpacity
          style={styles.skipButton}
          onPress={() => navigation.goBack()}
        >
          <Text style={styles.skipButtonText}>{t('login.skip')}</Text>
        </TouchableOpacity>
      </View>
      
      <Loading visible={loading} message={t('auth.signingIn')} />
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: t.bg,
  },
  content: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    padding: 20,
  },
  title: {
    ...type.display, fontSize: 32,
    lineHeight: 40,
    color: t.text,
    marginBottom: 10,
    textAlign: 'center',
  },
  subtitle: {
    ...type.body,
    color: t.muted,
    marginBottom: 40,
    textAlign: 'center',
    paddingHorizontal: 20,
  },
  authButton: {
    width: '100%',
    maxWidth: 300,
    paddingVertical: 16,
    borderRadius: 12,
    alignItems: 'center',
    marginBottom: 15,
    borderWidth: 1.5,
  },
  googleButton: {
    backgroundColor: 'transparent',
    borderColor: t.accent,
  },
  authButtonText: {
    color: t.text,
    ...type.label, fontSize: 16,
  },
  primaryButton: {
    backgroundColor: t.accent,
    borderColor: t.accent,
  },
  primaryButtonText: {
    color: t.onAccent,
    ...type.label, fontSize: 16,
  },
  skipButton: {
    marginTop: 20,
    padding: 10,
  },
  skipButtonText: {
    color: t.subtext,
    ...type.body, fontSize: 16,
  },
});

export default LoginScreen;
