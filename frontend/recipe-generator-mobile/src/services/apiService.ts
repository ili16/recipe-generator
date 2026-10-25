import axios, { AxiosInstance } from 'axios';
import { Platform } from 'react-native';
import { fetch as expoFetch } from 'expo/fetch';
import { API_BASE_URL, API_ENDPOINTS } from '../constants';
import { Recipe, RecipeDocument, RecipeResponse, PatchRecipePayload, GenerationOrigin, EditTurn, RecipeVersion, UserPreferences, MealPlanWeek, MealPlanItem, MealSlot, MealPlanSuggestion, GroceryList, GroceryTick, SharedPlan, ChatStreamEvent, ChatAttachment, Collection, PantryItem, FeedbackPayload, Household, CookedResult, ConversationSummary, ConversationThread, Usage } from '../types';
import authService from './authService';

// RN's global fetch has no readable body on native; expo/fetch does, so SSE streams there too.
const streamFetch = (url: string, init: RequestInit) =>
  Platform.OS === 'web' ? fetch(url, init) : (expoFetch(url, init as never) as unknown as Promise<Response>);
import { currentLocale } from '../i18n';
import { isOffline } from '../utils/offlineRecipes';
import { offlineRecipe, rememberForOffline } from '../utils/offlineRecipeStore';

// The language sent on every /chat turn. It is the only language signal the backend has
// left since POST /generate (and its `language` form field) was deleted — the agent uses
// it when the user's own message is too short to detect a language from, so that
// "cacio e pepe" comes back in the user's language rather than Italian (BACKLOG 3.9).
// Read per turn, not once: it follows the in-app language switch (Profile), which starts
// at the device locale but does not have to stay there.

// The one error type every apiService method rejects with. `status` is the HTTP status
// (undefined = the request never reached the server), so a caller can tell "the server
// said no" from "the server is down" — which the old swallow-and-return-empty methods
// made impossible. No method swallows its own errors; swallowing is the call site's choice.
export class ApiError extends Error {
  constructor(message: string, readonly status?: number, readonly data?: unknown) {
    super(message);
    this.name = 'ApiError';
  }
}

class ApiService {
  private client: AxiosInstance;

  constructor() {
    this.client = axios.create({
      baseURL: API_BASE_URL,
      headers: {
        'Content-Type': 'application/json',
      },
    });

    // Add request interceptor to include auth token (only if authenticated)
    this.client.interceptors.request.use(
      async (config) => {
        const token = await authService.getAccessToken();
        if (token) {
          config.headers.Authorization = `Bearer ${token}`;
        }
        // If no token, requests still proceed (for anonymous access)
        return config;
      },
      (error) => Promise.reject(error)
    );

    this.client.interceptors.response.use(
      (response) => response,
      async (error) => {
        const originalRequest = error.config as any;
        const status = error?.response?.status;

        if (status === 401 && originalRequest && !originalRequest._retry) {
          originalRequest._retry = true;

          const refreshedToken = await authService.refreshAccessToken();
          if (refreshedToken) {
            originalRequest.headers = originalRequest.headers ?? {};
            originalRequest.headers.Authorization = `Bearer ${refreshedToken}`;
            return this.client(originalRequest);
          }
        }

        return Promise.reject(
          new ApiError(error?.message ?? 'request failed', status, error?.response?.data)
        );
      }
    );
  }

  // The one SSE reader. Mirrors the axios interceptors the raw-fetch streams would
  // otherwise miss: bearer token in, one 401 refresh-and-replay. Always cancels the
  // reader so an abandoned stream stops server-side too. `signal` aborts the request.
  private async streamSSE(
    path: string,
    init: (token: string | null) => RequestInit,
    onEvent: (type: string, payload: unknown) => void,
    defaultEvent: string,
    signal?: AbortSignal,
  ): Promise<void> {
    // A raw fetch rejects with a bare TypeError when there is no network, where axios
    // rejects with an ApiError carrying no status. Normalising here is what lets one
    // `isOffline` check cover both (BACKLOG 12.4).
    const send = async (token: string | null) => {
      try {
        return await streamFetch(`${API_BASE_URL}/${path}`, { ...init(token), signal } as RequestInit);
      } catch (err) {
        if (err instanceof Error && err.name === 'AbortError') throw err;
        throw new ApiError(err instanceof Error ? err.message : 'request failed');
      }
    };

    let response = await send(await authService.getAccessToken());
    if (response.status === 401) {
      const refreshed = await authService.refreshAccessToken();
      if (refreshed) response = await send(refreshed);
    }
    if (!response.ok || !response.body) {
      // The refusal a caller acts on is in the body, not the status: billing.Guard turns a
      // user away with 429 {"error":"budget_exceeded"} *before* the stream opens, so
      // dropping the body here is what made hitting the cap look like a generic failure
      // (BACKLOG.md 10.5).
      const code = await response.json().then(
        (b: { error?: string; code?: string }) => b?.error || b?.code,
        () => undefined,
      );
      throw new ApiError(code || `stream failed: ${response.status}`, response.status);
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const events = buffer.split('\n\n');
        buffer = events.pop() ?? '';
        for (const raw of events) {
          if (!raw.trim()) continue;
          let type = defaultEvent;
          const data: string[] = [];
          for (const line of raw.split('\n')) {
            if (line.startsWith('event:')) type = line.slice(6).trim();
            else if (line.startsWith('data:')) data.push(line.slice(5).trim());
          }
          if (data.length === 0) continue;
          let payload: unknown;
          try {
            payload = JSON.parse(data.join('\n'));
          } catch {
            continue; // a malformed frame must not kill the stream
          }
          // The agent loop reports failures as {code}; older streams send a bare string.
          if (type === 'error') {
            const code = typeof payload === 'string' ? payload : (payload as { code?: string })?.code;
            throw new ApiError(code || 'stream error');
          }
          onEvent(type, payload);
        }
      }
    } finally {
      reader.cancel().catch(() => { /* already closed */ });
    }
  }

  // The agent loop (POST /chat): one user turn in, a stream of prose tokens, tool events
  // and artifact cards out. The thread lives server-side — the only state the client
  // keeps is the conversation id, which this resolves from the closing `done` event.
  // Native streams too, through expo/fetch (RN's own fetch has no readable body).
  async streamChat(
    message: string,
    conversationId: string | null,
    onEvent: (event: ChatStreamEvent) => void,
    signal?: AbortSignal,
    attachments?: ChatAttachment[],
    /**
     * Answers an `approval_request` from the previous turn on this thread instead of
     * sending a message (BACKLOG.md 10.2) — the decision *is* the turn, and the server
     * resumes the parked tool call from it.
     */
    approval?: { id: string; approve: boolean },
  ): Promise<string | null> {
    let conversation = conversationId;
    const body = { conversation_id: conversationId ?? '', message, attachments, approval, language: currentLocale() };

    await this.streamSSE(
      API_ENDPOINTS.CHAT,
      (token) => ({
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify(body),
      }),
      (type, payload) => {
        if (type === 'done') conversation = (payload as { conversation_id?: string }).conversation_id ?? conversation;
        onEvent({ type, payload } as ChatStreamEvent);
      },
      'token',
      signal,
    );
    return conversation;
  }

  // One /chat turn collapsed to its result, for the surfaces that want an answer rather
  // than a conversation to render: the assistant's prose and the last recipe document it
  // produced. Pass the returned `conversationId` back in to make the next call a follow-up
  // in the same thread — which is the whole reason cooking mode stopped posting to the old
  // stateless /refine-recipe and /cooking-chat (BACKLOG 3.13).
  async chatTurn(
    message: string,
    conversationId: string | null = null,
  ): Promise<{ conversationId: string | null; text: string; document?: RecipeDocument }> {
    let text = '';
    let document: RecipeDocument | undefined;
    const conversation = await this.streamChat(message, conversationId, (event) => {
      if (event.type === 'token') text += event.payload.text;
      else if (event.type === 'artifact' && event.payload.kind === 'recipe') {
        document = event.payload.data.document;
      }
    });
    return { conversationId: conversation, text: text.trim(), document };
  }

  // Transcribe audio to text using OpenAI Whisper on the backend
  async transcribeAudio(audioUri: string): Promise<string> {
    const formData = new FormData();
    const filename = audioUri.split('/').pop() || 'audio.m4a';
    const ext = filename.split('.').pop()?.toLowerCase() ?? 'm4a';
    const mimeMap: Record<string, string> = { m4a: 'audio/m4a', mp3: 'audio/mpeg', wav: 'audio/wav', webm: 'audio/webm', ogg: 'audio/ogg' };
    const type = mimeMap[ext] ?? 'audio/m4a';
    formData.append('audio', { uri: audioUri, name: filename, type } as any);
    const response = await this.client.post<{ text: string }>(
      API_ENDPOINTS.TRANSCRIBE_AUDIO,
      formData,
      { headers: { 'Content-Type': 'multipart/form-data' } },
    );
    return response.data.text ?? '';
  }

  async transcribeBlob(blob: Blob, filename = 'recording.webm'): Promise<string> {
    const formData = new FormData();
    formData.append('audio', blob, filename);
    const response = await this.client.post<{ text: string }>(
      API_ENDPOINTS.TRANSCRIBE_AUDIO,
      formData,
      { headers: { 'Content-Type': 'multipart/form-data' } },
    );
    return response.data.text ?? '';
  }

  // Persist manual edits to a saved recipe (PATCH /update-recipe)
  async patchRecipe(payload: PatchRecipePayload): Promise<Recipe> {
    const response = await this.client.patch<Recipe>(
      API_ENDPOINTS.UPDATE_RECIPE,
      payload
    );
    return response.data;
  }

  // Get all recipes
  async getRecipes(): Promise<Recipe[]> {
    const response = await this.client.get<Recipe[]>(API_ENDPOINTS.GET_RECIPES);
    return response.data;
  }

  // Delete recipe (soft — it lands in the trash for 30 days, BACKLOG 8.2)
  async deleteRecipe(recipeId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.DELETE_RECIPE}/${recipeId}`);
  }

  async getTrash(): Promise<Recipe[]> {
    const response = await this.client.get<Recipe[]>(`${API_ENDPOINTS.GET_RECIPE}/trash`);
    return response.data;
  }

  async restoreRecipe(recipeId: number): Promise<void> {
    await this.client.post(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/restore`);
  }

  /* ── Collections (BACKLOG 8.1) ───────────────────────────────────────────── */

  async getCollections(): Promise<Collection[]> {
    const response = await this.client.get<Collection[]>(API_ENDPOINTS.COLLECTIONS);
    return response.data;
  }

  // Creating a name that already exists returns that collection rather than a duplicate.
  async createCollection(name: string): Promise<Collection> {
    const response = await this.client.post<Collection>(API_ENDPOINTS.COLLECTIONS, { name });
    return response.data;
  }

  async deleteCollection(collectionId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.COLLECTIONS}/${collectionId}`);
  }

  async setRecipeCollection(collectionId: number, recipeId: number, member: boolean): Promise<void> {
    const path = `${API_ENDPOINTS.COLLECTIONS}/${collectionId}/recipes/${recipeId}`;
    if (member) await this.client.put(path);
    else await this.client.delete(path);
  }

  /* ── Feedback (BACKLOG 9.16) ─────────────────────────────────────────────── */

  // The one write that works logged out: the request interceptor attaches a token when
  // there is one and proceeds without when there isn't, and the backend treats "no user"
  // as anonymous rather than unauthorised. A tester who never signs in still hits bugs.
  async submitFeedback(payload: FeedbackPayload): Promise<void> {
    await this.client.post(API_ENDPOINTS.FEEDBACK, payload);
  }

  /* ── Pantry (BACKLOG 7.1) ────────────────────────────────────────────────── */

  async getPantry(): Promise<PantryItem[]> {
    const response = await this.client.get<PantryItem[]>(API_ENDPOINTS.PANTRY);
    return response.data;
  }

  // Upsert, not create: the server keys on the normalised name and unit, so saving
  // "flour" twice updates the amount rather than adding a second line. The saved item
  // comes back with its id, which is how a ticked grocery line remembers what to remove
  // again when it is unticked (BACKLOG 7.2).
  async savePantryItem(item: Omit<PantryItem, 'id'>): Promise<PantryItem> {
    const response = await this.client.post<PantryItem>(API_ENDPOINTS.PANTRY, item);
    return response.data;
  }

  /** "Still enough?" for one line — BACKLOG 16.6. Either answer confirms the line. */
  async setPantryStock(itemId: number, runningLow: boolean): Promise<PantryItem> {
    const response = await this.client.patch<PantryItem>(
      `${API_ENDPOINTS.PANTRY}/${itemId}/stock`, { running_low: runningLow });
    return response.data;
  }

  async deletePantryItem(itemId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.PANTRY}/${itemId}`);
  }

  /* ── Households (BACKLOG 15.1) ───────────────────────────────────────────── */

  // null when the caller is in no household — the server answers 204, which is a normal
  // state and not an error, and the screen renders the create/join form for it.
  async getHousehold(): Promise<Household | null> {
    const response = await this.client.get<Household | ''>(API_ENDPOINTS.HOUSEHOLD);
    return response.status === 204 || !response.data ? null : (response.data as Household);
  }

  async createHousehold(name: string): Promise<Household> {
    const response = await this.client.post<Household>(API_ENDPOINTS.HOUSEHOLD, { name });
    return response.data;
  }

  // A wrong code and a rotated one both come back 404 — deliberately indistinguishable.
  async joinHousehold(joinCode: string): Promise<Household> {
    const response = await this.client.post<Household>(`${API_ENDPOINTS.HOUSEHOLD}/join`, { join_code: joinCode });
    return response.data;
  }

  async leaveHousehold(): Promise<void> {
    await this.client.post(`${API_ENDPOINTS.HOUSEHOLD}/leave`);
  }

  // Ends it for everyone; any member may. Recipes go back to their owners and planned days
  // after today are dropped, so the caller has to have confirmed this first.
  async disbandHousehold(): Promise<void> {
    await this.client.delete(API_ENDPOINTS.HOUSEHOLD);
  }

  // The only way to revoke access after somebody leaves.
  async rotateHouseholdCode(): Promise<string> {
    const response = await this.client.post<{ join_code: string }>(`${API_ENDPOINTS.HOUSEHOLD}/code`);
    return response.data.join_code;
  }

  /* ── Read-only share link (BACKLOG 8.4) ──────────────────────────────────── */

  // Turns sharing on and returns the token. Idempotent: an already-shared recipe
  // returns the same token, so a second tap never invalidates a link already sent.
  async shareRecipe(recipeId: number): Promise<string> {
    const response = await this.client.post<{ share_token: string }>(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/share`);
    return response.data.share_token;
  }

  async unshareRecipe(recipeId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/share`);
  }

  // Unauthenticated on purpose — the token is the credential.
  async getSharedRecipe(token: string): Promise<Recipe> {
    const response = await this.client.get<Recipe>(`${API_ENDPOINTS.SHARED}/${token}`);
    return response.data;
  }

  // The one read that returns a recipe's structured document, and so the one place worth
  // an offline copy: cooking mode and the library card both come through here
  // (BACKLOG 12.4). Every success tops the cache up; a request that never reached the
  // server falls back to it, so a hob with no wifi still has steps. A 4xx/5xx does not —
  // a deleted recipe must not come back from a cache.
  async getRecipeById(recipeId: number): Promise<Recipe> {
    try {
      const response = await this.client.get<Recipe>(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}`);
      void rememberForOffline([response.data]);
      return response.data;
    } catch (err) {
      const cached = isOffline(err) ? await offlineRecipe(recipeId) : null;
      if (cached) return cached;
      throw err;
    }
  }

  // What the cook uses instead on one ingredient line (BACKLOG 17.4c). An empty
  // replacement clears the swap — one call both ways, so the caller toggles rather than
  // choosing a verb. sortOrder is the line's 1-based position in the recipe.
  async setIngredientSwap(recipeId: number, sortOrder: number, replacement: string): Promise<void> {
    await this.client.put(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/swaps/${sortOrder}`, { replacement });
  }

  async getGraph(): Promise<GraphEdge[]> {
    const response = await this.client.get<GraphEdge[]>('graph');
    return response.data ?? [];
  }

  // Recipe↔recipe edges for the map's Overlap mode: each recipe's few closest neighbours
  // by shared food, scored server-side so the scoring lives in one place (BACKLOG 18.3).
  async getRecipeOverlap(): Promise<RecipeLink[]> {
    const response = await this.client.get<RecipeLink[] | null>('graph/overlap');
    return response.data ?? [];
  }

  async getGraphNeighbours(type: string, node: string, depth = 1): Promise<GraphEdge[]> {
    const response = await this.client.get<GraphEdge[] | null>('graph/neighbours', { params: { type, node, depth } });
    return response.data ?? [];
  }

  async getRecipeHistory(recipeId: number): Promise<RecipeVersion[]> {
    const response = await this.client.get<RecipeVersion[]>(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/history`);
    return response.data;
  }

  // Save recipe (requires authentication)
  async saveRecipe(
    recipeName: string,
    recipeContent: string,
    tags?: string[],
    structured?: RecipeDocument,
    origin?: GenerationOrigin,
    history?: EditTurn[],
    variantOfRecipeId?: number
  ): Promise<Recipe> {
    const response = await this.client.post<Recipe>(API_ENDPOINTS.SAVE_RECIPE, {
      recipename: recipeName,
      recipe: recipeContent,
      ...(tags && tags.length > 0 ? { tags } : {}),
      ...(structured ? { structured } : {}),
      ...(origin ? { origin } : {}),
      ...(history ? { history } : {}),
      ...(variantOfRecipeId ? { variant_of_recipe_id: variantOfRecipeId } : {}),
    });
    return response.data;
  }

  // Saves a recipe the agent drafted in chat, without asking the model to do it — the
  // thread's "Save to my recipes" button. Idempotent server-side per draft_ref, so a
  // second tap (or a later "save it") returns the recipe already saved.
  async saveChatDraft(conversationId: string, draftRef: string, tags?: string[]): Promise<Recipe> {
    const response = await this.client.post<Recipe>(API_ENDPOINTS.SAVE_CHAT_DRAFT, {
      conversation_id: conversationId,
      draft_ref: draftRef,
      ...(tags && tags.length > 0 ? { tags } : {}),
    });
    return response.data;
  }

  // The thread list and one reopened thread (BACKLOG.md 10.4). Signed-in only: an
  // anonymous thread lives in server memory and has no row to list.
  async listConversations(): Promise<ConversationSummary[]> {
    const response = await this.client.get<ConversationSummary[]>(API_ENDPOINTS.CONVERSATIONS);
    return response.data;
  }

  async getConversation(conversationId: string): Promise<ConversationThread> {
    const response = await this.client.get<ConversationThread>(`${API_ENDPOINTS.CONVERSATIONS}/${conversationId}`);
    return response.data;
  }

  // Renaming and deleting a thread (BACKLOG.md 10.7). The server trims and caps the title
  // at 80 runes and echoes back what it stored, so the list shows the stored name.
  async renameConversation(conversationId: string, title: string): Promise<string> {
    const response = await this.client.patch<{ title: string }>(`${API_ENDPOINTS.CONVERSATIONS}/${conversationId}`, { title });
    return response.data.title;
  }

  async deleteConversation(conversationId: string): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.CONVERSATIONS}/${conversationId}`);
  }

  // This month's spend against the cap (BACKLOG.md 10.5). Not budget-guarded itself — a
  // user at the cap is exactly who needs to read it.
  async getUsage(): Promise<Usage> {
    const response = await this.client.get<Usage>(API_ENDPOINTS.USAGE);
    return response.data;
  }

  // Last known preferences, kept so a screen can render the right week start on its first
  // frame instead of showing the Monday default and visibly jumping once the fetch lands.
  // Cleared on logout (authService.clearSession) — another account's week start is wrong,
  // not just stale.
  cachedPreferences: UserPreferences | null = null;

  async getPreferences(): Promise<UserPreferences> {
    const response = await this.client.get<UserPreferences>(API_ENDPOINTS.PREFERENCES);
    this.cachedPreferences = response.data;
    return response.data;
  }

  async updatePreferences(p: Partial<UserPreferences>): Promise<UserPreferences> {
    const response = await this.client.patch<UserPreferences>(API_ENDPOINTS.PREFERENCES, p);
    this.cachedPreferences = response.data;
    return response.data;
  }

  async voteRecipe(recipeId: number, vote: 1 | -1): Promise<void> {
    await this.client.put(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/vote`, { vote });
  }

  async unvoteRecipe(recipeId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/vote`);
  }

  // Mark a recipe cooked now (BACKLOG 6.3) — keeps it out of meal-plan suggestions for
  // a couple of weeks. Idempotent: re-marking just moves the timestamp forward. An
  // optional 1–5 rating (BACKLOG 6.7) rides the same call, so the automatic mark at the
  // end of cooking mode and the stars tapped a moment later need no second endpoint;
  // omitting it leaves any previous rating alone.
  async markCooked(recipeId: number, rating?: number): Promise<void> {
    await this.client.put(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/cooked`, rating ? { rating } : {});
  }

  async unmarkCooked(recipeId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.GET_RECIPE}/${recipeId}/cooked`);
  }

  // Get every meal-plan item in [startsOn, endsOn]. endsOn defaults server-side to
  // startsOn+6d (a week) when omitted.
  async getMealPlanWeek(startsOn: string, endsOn?: string): Promise<MealPlanWeek> {
    const response = await this.client.get<MealPlanWeek>(API_ENDPOINTS.MEAL_PLAN, {
      params: { starts_on: startsOn, ...(endsOn ? { ends_on: endsOn } : {}) },
    });
    return response.data;
  }

  // The week's shopping list, derived from the plan server-side. endsOn defaults to
  // startsOn+6d, exactly as getMealPlanWeek does.
  async getGroceryList(startsOn: string, endsOn?: string): Promise<GroceryList> {
    const response = await this.client.get<GroceryList>(API_ENDPOINTS.GROCERY_LIST, {
      params: { starts_on: startsOn, ...(endsOn ? { ends_on: endsOn } : {}) },
    });
    return response.data;
  }

  /* ── Ticked-off grocery lines (BACKLOG 15.9) ─────────────────────────────── */

  // Ticks live on the server and not on the device because a household shops together:
  // two people splitting one list have to see each other's ticks, or one of them buys the
  // milk twice. For a solo cook this is the same state their device used to hold.
  async getGroceryTicks(weekStartISO: string): Promise<GroceryTick[]> {
    const response = await this.client.get<{ ticks: GroceryTick[] }>(API_ENDPOINTS.GROCERY_TICKS, {
      params: { week_start: weekStartISO },
    });
    return response.data.ticks ?? [];
  }

  // The pantry write (BACKLOG 7.2) is the server's now, not the client's: two devices each
  // writing their own pantry row is exactly the bug 15.9 exists to fix. Idempotent — a line
  // somebody already ticked comes back with their tick, not a second one.
  async tickGroceryLine(weekStartISO: string, lineKey: string, item: Omit<PantryItem, 'id'>): Promise<GroceryTick> {
    const response = await this.client.post<GroceryTick>(API_ENDPOINTS.GROCERY_TICKS,
      { line_key: lineKey, item },
      { params: { week_start: weekStartISO } });
    return response.data;
  }

  async untickGroceryLine(weekStartISO: string, lineKey: string): Promise<void> {
    await this.client.delete(API_ENDPOINTS.GROCERY_TICKS, { params: { week_start: weekStartISO, line_key: lineKey } });
  }

  // The week's read-only link (BACKLOG 13.2). Idempotent: an already-shared week
  // returns the token already sent, so a second tap never breaks a link.
  async sharePlan(startsOn: string, endsOn?: string): Promise<string> {
    const response = await this.client.post<{ share_token: string }>(API_ENDPOINTS.MEAL_PLAN_SHARE, null, {
      params: { starts_on: startsOn, ...(endsOn ? { ends_on: endsOn } : {}) },
    });
    return response.data.share_token;
  }

  async unsharePlan(startsOn: string): Promise<void> {
    await this.client.delete(API_ENDPOINTS.MEAL_PLAN_SHARE, { params: { starts_on: startsOn } });
  }

  // Unauthenticated on purpose — the token is the credential.
  async getSharedPlan(token: string): Promise<SharedPlan> {
    const response = await this.client.get<SharedPlan>(`${API_ENDPOINTS.SHARED_PLAN}/${token}`);
    return response.data;
  }

  // Assign a saved recipe to one meal slot of a day (upsert — replaces whatever was in
  // that slot, leaving the day's other meals alone; BACKLOG 6.4). Because it upserts,
  // re-posting the same recipe with a different `servings` is also how a planned day's
  // servings get changed (BACKLOG 6.2). Omitting mealSlot means dinner.
  async addMealPlanItem(recipeId: number, plannedOn: string, servings?: number, mealSlot?: MealSlot): Promise<MealPlanItem> {
    const response = await this.client.post<MealPlanItem>(API_ENDPOINTS.MEAL_PLAN_ITEMS, {
      recipe_id: recipeId,
      planned_on: plannedOn,
      ...(servings ? { servings } : {}),
      ...(mealSlot ? { meal_slot: mealSlot } : {}),
    });
    return response.data;
  }

  // Make one cooking day cover a set of later days (BACKLOG 9.13). `days` is the
  // complete set of covered days, so passing a shorter list — or none — is the undo.
  // One atomic write server-side; the response is the cook plus its leftovers.
  async setBatchCook(itemId: number, days: string[], servings?: number): Promise<MealPlanItem[]> {
    const response = await this.client.post<MealPlanItem[]>(
      `${API_ENDPOINTS.MEAL_PLAN_ITEMS}/${itemId}/covers`,
      { days, ...(servings ? { servings } : {}) },
    );
    return response.data;
  }

  // Mark one planned meal cooked and eaten: it leaves the week's grocery list and its
  // ingredients come out of the pantry, server-side and in one transaction. The result
  // says what the pantry deduction changed and what it refused to guess at — the caller
  // must show both, because the alternative is silently editing the user's fridge.
  // A 404 means it was already marked; re-tapping never deducts twice.
  async markMealCooked(itemId: number, rating?: number): Promise<CookedResult> {
    const response = await this.client.post<CookedResult>(
      `${API_ENDPOINTS.MEAL_PLAN_ITEMS}/${itemId}/cooked`,
      rating ? { rating } : {},
    );
    return response.data;
  }

  // Undo a mis-tap. The meal goes back on the week and back onto the grocery list; the
  // pantry is deliberately NOT restored (there is no record of what was deducted), which
  // the UI tells the user rather than leaving them to find out.
  async unmarkMealCooked(itemId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.MEAL_PLAN_ITEMS}/${itemId}/cooked`);
  }

  async deleteMealPlanItem(itemId: number): Promise<void> {
    await this.client.delete(`${API_ENDPOINTS.MEAL_PLAN_ITEMS}/${itemId}`);
  }

  // Propose a new, unsaved recipe variant of an existing saved recipe
  async generateVariant(recipeId: number, hint?: string): Promise<RecipeResponse & { variant_of_recipe_id: number }> {
    const response = await this.client.post<RecipeResponse & { variant_of_recipe_id: number }>(
      `${API_ENDPOINTS.MEAL_PLAN_VARIANTS}/${recipeId}`,
      hint ? { hint } : {}
    );
    return response.data;
  }
}

export interface GraphEdge {
  src_type: string;
  src: string;
  rel: string;
  dst_type: string;
  dst: string;
  weight: number;
}

export interface RecipeLink {
  a: number;
  b: number;
  score: number;
  shared: string[];
}

export default new ApiService();
