import React, { useMemo } from 'react';
import { View, ActivityIndicator, StyleSheet, Modal, Text } from 'react-native';
import { Theme, useTheme } from '../context/ThemeContext';
import { type } from '../theme';

interface LoadingProps {
  visible: boolean;
  message?: string;
}

const Loading: React.FC<LoadingProps> = ({ visible, message }) => {
  const { theme } = useTheme();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  return (
    <Modal transparent visible={visible} animationType="fade">
      <View style={styles.container}>
        <View style={styles.content}>
          <ActivityIndicator size="large" color={theme.onScrim} />
          {message && <Text style={styles.message}>{message}</Text>}
        </View>
      </View>
    </Modal>
  );
};

const makeStyles = (t: Theme) =>
  StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: t.scrim,
      justifyContent: 'center',
      alignItems: 'center',
    },
    content: {
      padding: 30,
      borderRadius: 16,
      alignItems: 'center',
      minWidth: 150,
    },
    message: {
      marginTop: 15,
      ...type.body,
      color: t.onScrim,
    },
  });

export default Loading;
