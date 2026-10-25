import AsyncStorage from '@react-native-async-storage/async-storage';
import axios from 'axios';
import * as AuthSession from 'expo-auth-session';
import { Platform } from 'react-native';
import { KEYCLOAK_CONFIG, STORAGE_KEYS, mealPlanCacheKeys } from '../constants';
import { UserProfile } from '../types';

// 'login' sends the user to Keycloak's login page; 'signup' sends them straight
// to its registration page instead. Google shows up as an alternative on both
// (configured as an IdP on the realm), so there's no separate Google button.
export type AuthMode = 'login' | 'signup';

const KEYCLOAK_ISSUER = `${KEYCLOAK_CONFIG.url}/realms/${KEYCLOAK_CONFIG.realm}`;
const USERINFO_ENDPOINT = `${KEYCLOAK_ISSUER}/protocol/openid-connect/userinfo`;
const AUTHORIZATION_ENDPOINT = `${KEYCLOAK_ISSUER}/protocol/openid-connect/auth`;
const REGISTRATION_ENDPOINT = `${KEYCLOAK_ISSUER}/protocol/openid-connect/registrations`;
const TOKEN_ENDPOINT = `${KEYCLOAK_ISSUER}/protocol/openid-connect/token`;
const REVOCATION_ENDPOINT = `${KEYCLOAK_ISSUER}/protocol/openid-connect/revoke`;

const DISCOVERY: AuthSession.DiscoveryDocument = {
  authorizationEndpoint: AUTHORIZATION_ENDPOINT,
  tokenEndpoint: TOKEN_ENDPOINT,
  revocationEndpoint: REVOCATION_ENDPOINT,
};

// Web only: mobile Safari blocks the popup expo-auth-session opens for
// promptAsync() because it's opened after an `await`, not synchronously from
// the click. A full-page redirect never gets blocked, so on web we drive the
// OAuth code flow manually and stash the PKCE verifier across the reload.
const WEB_AUTH_STATE_KEY = 'recipe_generator_web_auth_state';

type WebAuthState = {
  state: string;
  codeVerifier?: string;
  redirectUri: string;
};

class AuthService {
  private accessToken: string | null = null;
  private refreshToken: string | null = null;
  private tokenExpiresAt: number | null = null;
  private userProfile: UserProfile | null = null;
  private refreshPromise: Promise<string | null> | null = null;

  private buildRedirectUri(): string {
    const scheme = KEYCLOAK_CONFIG.redirectUri.split('://')[0];
    return AuthSession.makeRedirectUri({
      scheme,
      path: 'oauth/callback',
    });
  }

  async login(mode: AuthMode = 'login'): Promise<UserProfile | null> {
    try {
      const redirectUri = this.buildRedirectUri();
      const discovery: AuthSession.DiscoveryDocument = {
        ...DISCOVERY,
        authorizationEndpoint: mode === 'signup' ? REGISTRATION_ENDPOINT : AUTHORIZATION_ENDPOINT,
      };
      const request = new AuthSession.AuthRequest({
        clientId: KEYCLOAK_CONFIG.clientId,
        responseType: AuthSession.ResponseType.Code,
        redirectUri,
        // No offline_access: Keycloak never creates the offline session such a token claims,
        // and introspection rejects it as user_session_not_found. Plain refresh tokens
        // (bound to the SSO session) are issued and used by performRefresh.
        scopes: ['openid', 'profile', 'email'],
        usePKCE: true,
      });

      if (Platform.OS === 'web') {
        const authUrl = await request.makeAuthUrlAsync(discovery);
        const state: WebAuthState = {
          state: request.state,
          codeVerifier: request.codeVerifier,
          redirectUri,
        };
        window.sessionStorage.setItem(WEB_AUTH_STATE_KEY, JSON.stringify(state));
        window.location.assign(authUrl);
        // Navigation away from the page happens above; nothing left to return.
        return null;
      }

      const result = await request.promptAsync(discovery);
      if (result.type !== 'success' || !result.params.code) {
        return null;
      }

      const tokenResponse = await AuthSession.exchangeCodeAsync(
        {
          clientId: KEYCLOAK_CONFIG.clientId,
          code: result.params.code,
          redirectUri,
          extraParams: {
            code_verifier: request.codeVerifier ?? '',
          },
        },
        DISCOVERY
      );

      return await this.finalizeLogin(tokenResponse);
    } catch (error) {
      console.error('Login error:', error);
      return null;
    }
  }

  /**
   * Web only: call once on app start. Finishes the redirect-based login started
   * by `login()` if the page just came back from Keycloak with `?code=&state=`.
   */
  async completeWebLoginIfNeeded(): Promise<UserProfile | null> {
    if (Platform.OS !== 'web') {
      return null;
    }

    const url = new URL(window.location.href);
    const code = url.searchParams.get('code');
    const returnedState = url.searchParams.get('state');
    if (!code || !returnedState) {
      return null;
    }

    // Strip the OAuth params and go back to the app's root regardless of
    // outcome, so a refresh doesn't try to replay the same code and the user
    // isn't left sitting on the bare /oauth/callback URL.
    url.searchParams.delete('code');
    url.searchParams.delete('state');
    url.searchParams.delete('session_state');
    url.searchParams.delete('iss');
    url.pathname = '/';
    window.history.replaceState({}, '', url.toString());

    const rawState = window.sessionStorage.getItem(WEB_AUTH_STATE_KEY);
    window.sessionStorage.removeItem(WEB_AUTH_STATE_KEY);
    if (!rawState) {
      return null;
    }

    try {
      const savedState: WebAuthState = JSON.parse(rawState);
      if (savedState.state !== returnedState) {
        console.error('Login error: OAuth state mismatch.');
        return null;
      }

      const tokenResponse = await AuthSession.exchangeCodeAsync(
        {
          clientId: KEYCLOAK_CONFIG.clientId,
          code,
          redirectUri: savedState.redirectUri,
          extraParams: {
            code_verifier: savedState.codeVerifier ?? '',
          },
        },
        DISCOVERY
      );

      return await this.finalizeLogin(tokenResponse);
    } catch (error) {
      console.error('Login error:', error);
      return null;
    }
  }

  private async finalizeLogin(tokenResponse: AuthSession.TokenResponse): Promise<UserProfile> {
    const accessToken = tokenResponse.accessToken;
    if (!accessToken) {
      throw new Error('Missing access token in authorization response.');
    }

    const profile = await this.fetchUserProfile(accessToken);

    this.accessToken = accessToken;
    this.refreshToken = tokenResponse.refreshToken ?? null;
    this.tokenExpiresAt = this.resolveTokenExpiry(tokenResponse, accessToken);
    this.userProfile = profile;

    await AsyncStorage.setItem(STORAGE_KEYS.USER_TOKEN, accessToken);
    if (tokenResponse.refreshToken) {
      await AsyncStorage.setItem(STORAGE_KEYS.USER_REFRESH_TOKEN, tokenResponse.refreshToken);
    } else {
      await AsyncStorage.removeItem(STORAGE_KEYS.USER_REFRESH_TOKEN);
    }
    if (this.tokenExpiresAt) {
      await AsyncStorage.setItem(STORAGE_KEYS.USER_TOKEN_EXPIRES_AT, String(this.tokenExpiresAt));
    } else {
      await AsyncStorage.removeItem(STORAGE_KEYS.USER_TOKEN_EXPIRES_AT);
    }
    await AsyncStorage.setItem(STORAGE_KEYS.USER_PROFILE, JSON.stringify(profile));

    return profile;
  }

  private async fetchUserProfile(accessToken: string): Promise<UserProfile> {
    try {
      const response = await axios.get(USERINFO_ENDPOINT, {
        headers: {
          Authorization: `Bearer ${accessToken}`,
        },
      });

      const data = response.data as {
        name?: string;
        email?: string;
        preferred_username?: string;
        given_name?: string;
      };

      return {
        name: data.name ?? data.given_name ?? data.preferred_username ?? 'Google User',
        email: data.email,
        username: data.preferred_username,
      };
    } catch (error) {
      console.error('User profile fetch error:', error);
      return {
        name: 'Google User',
      };
    }
  }

  async logout(): Promise<void> {
    const accessToken = this.accessToken ?? await AsyncStorage.getItem(STORAGE_KEYS.USER_TOKEN);
    const refreshToken = this.refreshToken ?? await AsyncStorage.getItem(STORAGE_KEYS.USER_REFRESH_TOKEN);
    const userId = accessToken ? this.decodeUserId(accessToken) : null;

    await this.clearSession();
    if (userId) {
      const keys = mealPlanCacheKeys(userId);
      await AsyncStorage.multiRemove([keys.items, keys.recipes]).catch(() => {});
    }

    try {
      if (accessToken) {
        await this.revokeWithTimeout(accessToken);
      }
      if (refreshToken) {
        await this.revokeWithTimeout(refreshToken);
      }
    } catch (error) {
      console.error('Logout revoke warning:', error);
    }
  }

  async getAccessToken(): Promise<string | null> {
    if (!this.accessToken) {
      await this.hydrateTokensFromStorage();
    }

    if (!this.accessToken) {
      return null;
    }

    if (this.isTokenExpiringSoon()) {
      const refreshedToken = await this.refreshAccessToken();
      if (refreshedToken) {
        return refreshedToken;
      }
    }

    return this.accessToken;
  }

  async refreshAccessToken(): Promise<string | null> {
    if (this.refreshPromise) {
      return this.refreshPromise;
    }

    this.refreshPromise = this.performRefresh();
    try {
      return await this.refreshPromise;
    } finally {
      this.refreshPromise = null;
    }
  }

  private async performRefresh(): Promise<string | null> {
    if (!this.refreshToken) {
      const storedRefreshToken = await AsyncStorage.getItem(STORAGE_KEYS.USER_REFRESH_TOKEN);
      this.refreshToken = storedRefreshToken;
    }

    if (!this.refreshToken) {
      return null;
    }

    try {
      const tokenResponse = await AuthSession.refreshAsync(
        {
          clientId: KEYCLOAK_CONFIG.clientId,
          refreshToken: this.refreshToken,
        },
        DISCOVERY
      );

      const nextAccessToken = tokenResponse.accessToken;
      if (!nextAccessToken) {
        return null;
      }

      const nextRefreshToken = tokenResponse.refreshToken ?? this.refreshToken;
      const nextExpiresAt = this.resolveTokenExpiry(tokenResponse, nextAccessToken);

      this.accessToken = nextAccessToken;
      this.refreshToken = nextRefreshToken;
      this.tokenExpiresAt = nextExpiresAt;

      await AsyncStorage.setItem(STORAGE_KEYS.USER_TOKEN, nextAccessToken);
      if (nextRefreshToken) {
        await AsyncStorage.setItem(STORAGE_KEYS.USER_REFRESH_TOKEN, nextRefreshToken);
      }
      if (nextExpiresAt) {
        await AsyncStorage.setItem(STORAGE_KEYS.USER_TOKEN_EXPIRES_AT, String(nextExpiresAt));
      } else {
        await AsyncStorage.removeItem(STORAGE_KEYS.USER_TOKEN_EXPIRES_AT);
      }

      return nextAccessToken;
    } catch (error) {
      console.error('Silent token refresh failed:', error);
      await this.clearSession();
      return null;
    }
  }

  async isAuthenticated(): Promise<boolean> {
    const token = await this.getAccessToken();
    return token !== null && token.length > 0;
  }

  async getUserProfile(): Promise<UserProfile | null> {
    if (this.userProfile) {
      return this.userProfile;
    }

    try {
      const profileStr = await AsyncStorage.getItem(STORAGE_KEYS.USER_PROFILE);
      if (profileStr) {
        this.userProfile = JSON.parse(profileStr);
        return this.userProfile;
      }
    } catch (error) {
      console.error('Error getting user profile:', error);
    }
    return null;
  }

  private resolveTokenExpiry(
    tokenResponse: AuthSession.TokenResponse,
    accessToken: string
  ): number | null {
    if (typeof tokenResponse.issuedAt === 'number' && typeof tokenResponse.expiresIn === 'number') {
      return (tokenResponse.issuedAt + tokenResponse.expiresIn) * 1000;
    }

    return this.readJwtExpiry(accessToken);
  }

  private decodeJwtPayload(token: string): { exp?: number; sub?: string } | null {
    try {
      const [, payload] = token.split('.');
      if (!payload) {
        return null;
      }

      const base64 = payload.replace(/-/g, '+').replace(/_/g, '/');
      const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=');
      return JSON.parse(atob(padded));
    } catch {
      return null;
    }
  }

  private readJwtExpiry(token: string): number | null {
    const parsed = this.decodeJwtPayload(token);
    return typeof parsed?.exp === 'number' ? parsed.exp * 1000 : null;
  }

  private decodeUserId(token: string): string | null {
    const parsed = this.decodeJwtPayload(token);
    return typeof parsed?.sub === 'string' ? parsed.sub : null;
  }

  // The OIDC subject claim — same id the backend keys `users.oauth_id` by — used to scope
  // per-account local caches (see MealPlanContext) so a shared device never shows one
  // account's cached data to another.
  async getUserId(): Promise<string | null> {
    const token = await this.getAccessToken();
    return token ? this.decodeUserId(token) : null;
  }

  private isTokenExpiringSoon(): boolean {
    if (!this.accessToken) {
      return true;
    }

    if (!this.tokenExpiresAt) {
      this.tokenExpiresAt = this.readJwtExpiry(this.accessToken);
    }

    if (!this.tokenExpiresAt) {
      return false;
    }

    const oneMinuteFromNow = Date.now() + 60_000;
    return oneMinuteFromNow >= this.tokenExpiresAt;
  }

  private async hydrateTokensFromStorage(): Promise<void> {
    const [storedToken, storedRefreshToken, storedExpiresAt] = await AsyncStorage.multiGet([
      STORAGE_KEYS.USER_TOKEN,
      STORAGE_KEYS.USER_REFRESH_TOKEN,
      STORAGE_KEYS.USER_TOKEN_EXPIRES_AT,
    ]);

    this.accessToken = storedToken[1] ?? null;
    this.refreshToken = storedRefreshToken[1] ?? null;

    const expiresAtMs = storedExpiresAt[1] ? Number(storedExpiresAt[1]) : NaN;
    this.tokenExpiresAt = Number.isFinite(expiresAtMs) ? expiresAtMs : null;

    if (!this.tokenExpiresAt && this.accessToken) {
      this.tokenExpiresAt = this.readJwtExpiry(this.accessToken);
    }
  }

  private async clearSession(): Promise<void> {
    this.accessToken = null;
    this.refreshToken = null;
    this.tokenExpiresAt = null;
    this.userProfile = null;

    await AsyncStorage.multiRemove([
      STORAGE_KEYS.USER_TOKEN,
      STORAGE_KEYS.USER_REFRESH_TOKEN,
      STORAGE_KEYS.USER_TOKEN_EXPIRES_AT,
      STORAGE_KEYS.USER_PROFILE,
      STORAGE_KEYS.RECIPES,
    ]);
  }

  private async revokeWithTimeout(token: string): Promise<void> {
    await Promise.race([
      AuthSession.revokeAsync(
        {
          token,
          clientId: KEYCLOAK_CONFIG.clientId,
        },
        DISCOVERY
      ),
      new Promise<void>((_, reject) => {
        setTimeout(() => reject(new Error('Token revoke timeout')), 2500);
      }),
    ]);
  }
}

export default new AuthService();