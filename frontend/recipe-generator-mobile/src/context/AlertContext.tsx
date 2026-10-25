import React, {
  createContext,
  useContext,
  useRef,
  useState,
  useMemo,
  ReactNode,
} from 'react';
import {
  Modal,
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  Animated,
  Platform,
} from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { useTheme, Theme } from './ThemeContext';
import { useLanguage } from './LanguageContext';
import { type } from '../theme';

/** Omit it for a neutral alert; the caller always knows which it is. */
export type AlertSeverity = 'error' | 'success';

const ICONS: Record<AlertSeverity, React.ComponentProps<typeof Ionicons>['name']> = {
  error: 'warning-outline',
  success: 'checkmark-circle-outline',
};

interface AlertConfig {
  title: string;
  message?: string;
  severity?: AlertSeverity;
  confirmLabel?: string;
  destructive?: boolean;
  isConfirm: boolean;
  resolve: (result: boolean) => void;
}

interface AlertContextValue {
  showAlert: (title: string, message?: string, severity?: AlertSeverity) => void;
  confirmAction: (
    title: string,
    message: string,
    options?: { confirmLabel?: string; destructive?: boolean }
  ) => Promise<boolean>;
}

const AlertContext = createContext<AlertContextValue>({
  showAlert: () => {},
  confirmAction: async () => false,
});

export function AlertProvider({ children }: { children: ReactNode }) {
  const [config, setConfig] = useState<AlertConfig | null>(null);
  const scaleAnim = useRef(new Animated.Value(0.9)).current;
  const opacityAnim = useRef(new Animated.Value(0)).current;
  const { theme: t } = useTheme();
  // `t` is already the theme here, so the translator keeps its own name.
  const { t: translate } = useLanguage();
  const styles = useMemo(() => makeStyles(t), [t]);

  const open = (cfg: AlertConfig) => {
    setConfig(cfg);
    scaleAnim.setValue(0.9);
    opacityAnim.setValue(0);
    Animated.parallel([
      Animated.spring(scaleAnim, { toValue: 1, useNativeDriver: true, damping: 20, stiffness: 300 }),
      Animated.timing(opacityAnim, { toValue: 1, duration: 160, useNativeDriver: true }),
    ]).start();
  };

  const close = (result: boolean) => {
    config?.resolve(result);
    setConfig(null);
  };

  const showAlert = (title: string, message?: string, severity?: AlertSeverity) =>
    new Promise<boolean>(resolve => open({ title, message, severity, isConfirm: false, resolve }));

  const confirmAction = (
    title: string,
    message: string,
    options?: { confirmLabel?: string; destructive?: boolean }
  ): Promise<boolean> =>
    new Promise(resolve =>
      open({
        title, message,
        confirmLabel: options?.confirmLabel ?? 'OK',
        destructive: options?.destructive,
        isConfirm: true,
        resolve,
      })
    );

  const severity = config?.severity;

  return (
    <AlertContext.Provider value={{ showAlert, confirmAction }}>
      {children}
      <Modal
        visible={!!config}
        transparent
        animationType="none"
        onRequestClose={() => close(false)}
        statusBarTranslucent
      >
        <Animated.View style={[styles.overlay, { opacity: opacityAnim }]}>
          <TouchableOpacity style={StyleSheet.absoluteFill} activeOpacity={1} onPress={() => !config?.isConfirm && close(false)} />
          <Animated.View style={[styles.card, { transform: [{ scale: scaleAnim }] }]}>
            {severity && (
              <View style={styles.iconWrap}>
                <Ionicons
                  name={ICONS[severity]}
                  size={26}
                  color={severity === 'error' ? t.danger : t.success}
                />
              </View>
            )}
            <Text style={styles.title}>{config?.title}</Text>
            {config?.message ? <Text style={styles.message}>{config.message}</Text> : null}
            <View style={[styles.buttonRow, config?.isConfirm && styles.buttonRowDual]}>
              {config?.isConfirm && (
                <TouchableOpacity style={styles.btnCancel} onPress={() => close(false)}>
                  <Text style={styles.btnCancelText}>{translate('common.cancel')}</Text>
                </TouchableOpacity>
              )}
              <TouchableOpacity
                style={[styles.btnOk, config?.destructive && styles.btnDestructive]}
                onPress={() => close(true)}
              >
                <Text style={[styles.btnOkText, config?.destructive && styles.btnDestructiveText]}>
                  {config?.isConfirm ? (config.confirmLabel ?? 'OK') : 'OK'}
                </Text>
              </TouchableOpacity>
            </View>
          </Animated.View>
        </Animated.View>
      </Modal>
    </AlertContext.Provider>
  );
}

export const useAlert = () => useContext(AlertContext);

const makeStyles = (t: Theme) =>
  StyleSheet.create({
    overlay: {
      flex: 1,
      backgroundColor: t.overlay,
      justifyContent: 'center',
      alignItems: 'center',
      paddingHorizontal: 32,
    },
    card: {
      width: '100%',
      maxWidth: 340,
      backgroundColor: t.surfaceRaised,
      borderRadius: 20,
      borderWidth: 1,
      borderColor: t.border,
      paddingHorizontal: 24,
      paddingTop: 28,
      paddingBottom: 20,
      alignItems: 'center',
      // shadow for iOS
      shadowColor: t.shadow,
      shadowOffset: { width: 0, height: 8 },
      shadowOpacity: 0.28,
      shadowRadius: 18,
      // elevation for Android
      elevation: 12,
    },
    iconWrap: {
      width: 52,
      height: 52,
      borderRadius: 26,
      backgroundColor: t.surface,
      justifyContent: 'center',
      alignItems: 'center',
      marginBottom: 16,
    },
    title: {
      ...type.title, fontSize: 17,
      lineHeight: 24,
      color: t.text,
      textAlign: 'center',
      marginBottom: 6,
    },
    message: {
      ...type.body, fontSize: 13,
      color: t.muted,
      textAlign: 'center',
      lineHeight: 19,
      marginBottom: 4,
    },
    buttonRow: {
      marginTop: 22,
      width: '100%',
    },
    buttonRowDual: {
      flexDirection: 'row',
      gap: 10,
    },
    btnOk: {
      flex: 1,
      backgroundColor: t.accent,
      borderRadius: 10,
      paddingVertical: 11,
      alignItems: 'center',
    },
    btnOkText: {
      color: t.onAccent,
      ...type.label,
    },
    btnCancel: {
      flex: 1,
      backgroundColor: t.surface,
      borderRadius: 10,
      paddingVertical: 11,
      alignItems: 'center',
      borderWidth: 1,
      borderColor: t.border,
    },
    btnCancelText: {
      color: t.subtext,
      ...type.label,
    },
    btnDestructive: {
      backgroundColor: t.danger,
    },
    btnDestructiveText: {
      color: t.onDanger,
    },
  });
