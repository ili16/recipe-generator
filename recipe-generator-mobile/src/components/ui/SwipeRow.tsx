import React, { ReactNode, useMemo } from 'react';
import { Animated, Pressable, StyleSheet, View } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { Theme, useTheme } from '../../context/ThemeContext';
import { radius, space } from '../../theme';
import { Text } from './index';
import { useSwipe } from '../../hooks/useSwipe';

type IoniconName = React.ComponentProps<typeof Ionicons>['name'];

export interface SwipeAction {
  icon: IoniconName;
  /** Shown behind the row as it moves, and used as the gesture's accessibility hint. */
  label: string;
  tone?: 'accent' | 'danger';
  onAction: () => void;
}

/** How far the row travels before a release fires. Roughly a thumb's width. */
const THRESHOLD = 80;

/**
 * A row you can swipe. One implementation, every list: the planner's meals and the pantry's
 * shelves (BACKLOG 12.5).
 *
 * **Full-swipe fires** rather than parking a drawer of buttons open. A drawer needs an
 * open-row to track, a tap to close it, and an answer for what happens when a second row
 * opens — for two actions that both already exist one tap away, that is machinery bought
 * for nothing.
 *
 * **The swipe is always an accelerator, never the only path.** Every action here is also
 * reachable by tap (a sheet, a button), because a gesture is invisible to a screen reader
 * and undiscoverable to anyone who has not been told about it.
 */
export const SwipeRow: React.FC<{
  left?: SwipeAction;
  right?: SwipeAction;
  onPress?: () => void;
  onLongPress?: () => void;
  accessibilityLabel?: string;
  style?: object;
  children?: ReactNode;
}> = ({ left, right, onPress, onLongPress, accessibilityLabel, style, children }) => {
  const { theme } = useTheme();
  const styles = useMemo(() => makeStyles(theme), [theme]);

  // Dragging *right* uncovers the action pinned to the left edge, and vice versa.
  const { panHandlers, offset } = useSwipe({
    axis: 'x',
    threshold: THRESHOLD,
    onRight: left ? left.onAction : undefined,
    onLeft: right ? right.onAction : undefined,
    enabled: !!left || !!right,
  });

  const backdrop = (action: SwipeAction, side: 'left' | 'right') => (
    <Animated.View
      pointerEvents="none"
      style={[
        styles.backdrop,
        side === 'left' ? styles.backdropLeft : styles.backdropRight,
        { backgroundColor: action.tone === 'danger' ? theme.danger : theme.accent },
        { opacity: offset.interpolate({
            inputRange: side === 'left' ? [0, THRESHOLD] : [-THRESHOLD, 0],
            outputRange: side === 'left' ? [0, 1] : [1, 0],
            extrapolate: 'clamp',
          }) },
      ]}
    >
      <Ionicons name={action.icon} size={18} color={action.tone === 'danger' ? theme.onDanger : theme.onAccent} />
      <Text variant="label" style={{ color: action.tone === 'danger' ? theme.onDanger : theme.onAccent }}>
        {action.label}
      </Text>
    </Animated.View>
  );

  const body = (
    <Animated.View style={[styles.body, { transform: [{ translateX: offset }] }]} {...panHandlers}>
      {children}
    </Animated.View>
  );

  return (
    <View style={[styles.container, style]}>
      {left && backdrop(left, 'left')}
      {right && backdrop(right, 'right')}
      {onPress || onLongPress ? (
        <Pressable
          onPress={onPress}
          onLongPress={onLongPress}
          accessibilityRole="button"
          accessibilityLabel={accessibilityLabel}
          // Named so a screen reader hears the same two verbs the gesture performs.
          accessibilityHint={[left?.label, right?.label].filter(Boolean).join(' · ') || undefined}
        >
          {body}
        </Pressable>
      ) : body}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: { overflow: 'hidden', borderRadius: radius.md, position: 'relative' },
  body: { backgroundColor: t.surface },
  backdrop: {
    ...StyleSheet.absoluteFillObject,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space.sm,
    paddingHorizontal: space.lg,
  },
  backdropLeft: { justifyContent: 'flex-start' },
  backdropRight: { justifyContent: 'flex-end' },
});

export default SwipeRow;
