/**
 * Font assets — BACKLOG 5.6. Separate file only because these are `require()`d binaries and the
 * per-weight subpath imports keep the other six weights of each family out of the bundle.
 *
 * The keys are the family names the `type` scale in `./index.ts` refers to. Do not add a face
 * without a `type` variant that uses it.
 */
import { Epilogue_600SemiBold } from '@expo-google-fonts/epilogue/600SemiBold';
import { Epilogue_700Bold } from '@expo-google-fonts/epilogue/700Bold';
import { PlusJakartaSans_400Regular } from '@expo-google-fonts/plus-jakarta-sans/400Regular';
import { PlusJakartaSans_600SemiBold } from '@expo-google-fonts/plus-jakarta-sans/600SemiBold';

export const fontAssets = {
  Epilogue_600SemiBold,
  Epilogue_700Bold,
  PlusJakartaSans_400Regular,
  PlusJakartaSans_600SemiBold,
};
