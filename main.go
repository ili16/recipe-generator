package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	goopenai "github.com/sashabaranov/go-openai"
)

const (
	germanSystemMessage = "Du bist ein Agent, der das Format von Rezepten ändert." +
		"Das Rezept muss im Markdown-Format sein: " +
		"# <Rezeptname>\n" +
		"## Zutaten\n" +
		"- **<MENGE>** Zutat \n" +
		"## Zubereitung \n" +
		"### <Anweisung> z.B. Teig anrühren/Vorbereitung\n" +
		"- <Schritte>\n" +
		"### <Anweisung> z.B. Backen/Braten\n" +
		"- <Scritte>\n" +
		"Alle Zutaten müssen in metrischen Einheiten angegeben werden."

	englishSystemMessage = "You are an agent that changes the format recipes" +
		"The recipe needs to be in markdown format: " +
		"# <Recipe Name>\n" + "## Ingredients\n" + "- **<UNIT>** Ingredient \n" + "## Preparation" +
		"### Instructionset 1\n" +
		"- Steps\n" +
		"### Instructionset 2" +
		"- Steps\n" +
		"All ingredients need to be in metric units."

	judgeSystemMessage = "You are a judge AI agent that decides whether input is related to a recipe or not."
)

var pool *pgxpool.Pool

type RecipeRequest struct {
	Recipename     string `json:"recipename"`
	Recipe         string `json:"recipe"`
	IsGerman       bool   `json:"isGerman"`
	RecipeCategory string `json:"recipecategory,omitempty"`
}

type RecipeGenerateRequest struct {
	RecipeDescription string `json:"recipedescription"`
	IsGerman          bool   `json:"isGerman"`
}

type RecipeLinkRequest struct {
	URL      string `json:"url"`
	IsGerman bool   `json:"isGerman"`
}

type RecipeImageRequest struct {
	Recipename string `json:"recipename"`
	IsGerman   bool   `json:"isGerman"`
}

type RecipeChangeRequest struct {
	Recipe       string `json:"recipe"`
	ChangePrompt string `json:"changePrompt"`
}

type Recipe struct {
	Recipename string `json:"recipename"`
	Recipe     string `json:"recipe"`
	ID         int    `json:"id"`
	Transcript string `json:"transcript,omitempty"`
	Category   string `json:"category,omitempty"`
}

func main() {
	mux := http.NewServeMux()

	if !validateEnvVars() {
		log.Fatal("Missing environment variables")
		return
	}

	initDBPool()

	mux.HandleFunc("/health", HandleHealth)

	mux.HandleFunc("/api/v1/add-recipe", HandleAddRecipe)

	mux.HandleFunc("/api/v1/generate/by-description", HandlerJudgeMiddleware(HandleGenerateByDescription))

	mux.HandleFunc("/api/v1/generate/by-link", HandleGenerateByLink)

	mux.HandleFunc("/api/v1/generate/by-image", HandleGenerateByImage)

	mux.HandleFunc("/api/v1/login", HandleLogin)

	mux.HandleFunc("GET /api/v1/get-recipes", HandleGetRecipes)

	mux.HandleFunc("POST /api/v1/generate/by-voice", HandleGenerateRecipeByVoice)

	mux.HandleFunc("DELETE /api/v1/delete-recipe", HandleDeleteRecipe)

	mux.HandleFunc("POST /api/v1/update-recipe", HandleReprompt)

	mux.HandleFunc("PATCH /api/v1/update-recipe", HandleUpdateRecipe)

	log.Println("Server is running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", logRequests(mux)))
}

func initDBPool() {
	var err error
	pool, err = pgxpool.New(context.Background(), os.Getenv("DB_URL"))
	if err != nil {
		log.Fatalf("Unable to initialize DB pool connection: %v\n", err)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: Method=%s, URL=%s, Headers=%v, RemoteAddr=%s",
			r.Method, r.URL.String(), r.Header, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func validateEnvVars() bool {
	_, found := os.LookupEnv("OPENAI_KEY")
	if !found {
		log.Println("OPENAI_KEY environment variable  not found")
		return false
	}

	_, found = os.LookupEnv("DB_URL")
	if !found {
		log.Println("DB_URL environment variable missing")
		return false
	}

	return true
}

func HandleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintf(w, `{"status": "Healthy"}`)
	if err != nil {
		log.Println("Error writing response:", err)
	}
}

func HandleGetRecipes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	if os.Getenv("LOCAL_DEV") == "true" {
		mockAzureAuth(r)
	}

	oauthID := r.Header.Get("X-MS-CLIENT-PRINCIPAL-ID")

	userID, _, err := GetUserInformation(oauthID)
	if err != nil {
		http.Error(w, "Error getting user ID", http.StatusInternalServerError)
		return
	}

	recipes, err := GetRecipes(userID)
	if err != nil {
		http.Error(w, "Error getting recipes", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(recipes)
	if err != nil {
		http.Error(w, "Error encoding JSON response", http.StatusInternalServerError)
	}
}

func HandleAddRecipe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req RecipeRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.Recipename == "" || req.Recipe == "" {
		http.Error(w, "Missing recipename or recipe", http.StatusBadRequest)
		return
	}

	if req.RecipeCategory == "" {
		req.RecipeCategory = goopenAIgenerateRecipeCategory(req.Recipe)
	}

	if os.Getenv("LOCAL_DEV") == "true" {
		mockAzureAuth(r)
	}

	oauthID := r.Header.Get("X-MS-CLIENT-PRINCIPAL-ID")

	userID, storageaccount, err := GetUserInformation(oauthID)
	if err != nil {
		http.Error(w, "Error getting user ID", http.StatusInternalServerError)
		return
	}
	err = AddRecipeToDB(userID, req.Recipename, req.Recipe, req.RecipeCategory)
	if err != nil {
		http.Error(w, "Error adding recipe", http.StatusInternalServerError)
		return
	}

	recipename := strings.ReplaceAll(req.Recipename, " ", "-")
	recipePath := "recipes/" + recipename + ".md"

	err = addBlob(storageaccount, recipePath, req.Recipe)
	if err != nil {
		http.Error(w, "Failed to update recipe", http.StatusInternalServerError)
		return
	}
	err = templateRecipesBlob(storageaccount, userID)
	if err != nil {
		log.Printf("Error updating recipe template: %v\n", err)
		http.Error(w, "Failed to template recipes", http.StatusInternalServerError)
		return
	}

	_, _ = fmt.Fprint(w, "Recipe added successfully!")
}

func HandleDeleteRecipe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	if os.Getenv("LOCAL_DEV") == "true" {
		mockAzureAuth(r)
	}

	oauthID := r.Header.Get("X-MS-CLIENT-PRINCIPAL-ID")

	userID, storageAccountName, err := GetUserInformation(oauthID)
	if err != nil {
		log.Println("Error getting user ID:", err)
		http.Error(w, "Error getting user ID", http.StatusInternalServerError)
		return
	}

	var req map[string]int
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Println("Error decoding JSON:", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	recipeID, ok := req["recipeID"]
	if !ok || recipeID == 0 {
		log.Println("Missing or invalid recipeID")
		http.Error(w, "Missing or invalid recipeID", http.StatusBadRequest)
		return
	}

	err = RemoveRecipeFromDB(userID, recipeID)
	if err != nil {
		log.Printf("Error removing recipe: %v\n", err)
		http.Error(w, "Error removing recipe", http.StatusInternalServerError)
		return
	}

	err = templateRecipesBlob(storageAccountName, userID)
	if err != nil {
		log.Printf("Error updating recipe template: %v\n", err)
		http.Error(w, "Error updating recipe template", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func HandleUpdateRecipe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	if os.Getenv("LOCAL_DEV") == "true" {
		mockAzureAuth(r)
	}

	oauthID := r.Header.Get("X-MS-CLIENT-PRINCIPAL-ID")
	userID, storageAccountName, err := GetUserInformation(oauthID)
	if err != nil {
		log.Println("Error getting user ID:", err)
		http.Error(w, "Error getting user ID", http.StatusInternalServerError)
		return
	}

	var updateReq struct {
		ID             int    `json:"id"`
		Recipename     string `json:"recipename"`
		Recipe         string `json:"recipe"`
		RecipeCategory string `json:"recipecategory"`
	}

	if err := json.NewDecoder(r.Body).Decode(&updateReq); err != nil {
		log.Println("Error decoding request body:", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if updateReq.ID == 0 || updateReq.Recipename == "" || updateReq.Recipe == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	query := `
        UPDATE recipes 
        SET title = $1, content = $2, category = $3
        WHERE id = $4 AND user_id = $5
        RETURNING id`

	var recipeID int
	err = pool.QueryRow(context.Background(), query,
		updateReq.Recipename,
		updateReq.Recipe,
		updateReq.RecipeCategory,
		updateReq.ID,
		userID,
	).Scan(&recipeID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Recipe not found or unauthorized", http.StatusNotFound)
			return
		}
		log.Printf("Error updating recipe: %v\n", err)
		http.Error(w, "Error updating recipe", http.StatusInternalServerError)
		return
	}

	recipename := strings.ReplaceAll(updateReq.Recipename, " ", "-")
	recipePath := "recipes/" + recipename + ".md"

	if err := addBlob(storageAccountName, recipePath, updateReq.Recipe); err != nil {
		log.Printf("Error updating recipe in blob storage: %v\n", err)
		http.Error(w, "Failed to update recipe in storage", http.StatusInternalServerError)
		return
	}

	if err := templateRecipesBlob(storageAccountName, userID); err != nil {
		log.Printf("Error updating recipe template: %v\n", err)
		http.Error(w, "Failed to update recipe template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(map[string]string{
		"message": "Recipe updated successfully",
	})
	if err != nil {
		return
	}
}

func HandleGenerateByDescription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	var req RecipeGenerateRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.RecipeDescription == "" {
		log.Printf("missing recipe description")
		http.Error(w, "Missing recipename", http.StatusBadRequest)
		return
	}

	recipe, err := GenerateRecipeByName(req.RecipeDescription, req.IsGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		return
	}

	recipename, err := openAIgenerateRecipeName(recipe, req.IsGerman)
	if err != nil {
		log.Printf("Error generating recipe name: %v\n", err)
		http.Error(w, "Error generating recipe name", http.StatusInternalServerError)
		return
	}

	resp := Recipe{
		Recipename: recipename,
		Recipe:     recipe,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		http.Error(w, "Error encoding JSON response", http.StatusInternalServerError)
	}
}

func HandleGenerateByLink(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	var req RecipeLinkRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		http.Error(w, "Missing link", http.StatusBadRequest)
		return
	}

	recipename, recipe, err := GenerateRecipeByLink(req.URL, req.IsGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		return
	}

	resp := Recipe{
		Recipename: recipename,
		Recipe:     recipe,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		http.Error(w, "Error encoding JSON response", http.StatusInternalServerError)
	}
}

func HandleGenerateByImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form data (10 MB max)
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	// Retrieve the image file from the form
	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "Failed to get the image file", http.StatusBadRequest)
		return
	}
	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {

		}
	}(file)

	var recipeRequest RecipeImageRequest
	if recipeName := r.FormValue("recipename"); recipeName != "" {
		recipeRequest.Recipename = recipeName
	}
	if isGerman := r.FormValue("isGerman"); isGerman != "" {
		if isGerman == "true" {
			recipeRequest.IsGerman = true
		} else if isGerman == "false" {
			recipeRequest.IsGerman = false
		} else {
			http.Error(w, "isGerman must be 'true' or 'false'", http.StatusBadRequest)
			return
		}
	} else {
		http.Error(w, "isGerman cannot be empty", http.StatusBadRequest)
		return
	}

	base64Data, err := EncodeImageToBase64(file)
	if err != nil {
		http.Error(w, "Failed to encode image to base64", http.StatusInternalServerError)
		return
	}

	recipe, err := GenerateRecipeByImage(base64Data, recipeRequest.IsGerman)
	if err != nil {
		http.Error(w, "Failed to generate recipe", http.StatusInternalServerError)
		return
	}

	var recipename string

	if recipeRequest.Recipename != "" {
		recipename = recipeRequest.Recipename
	} else {
		recipename, err = openAIgenerateRecipeName(recipe, recipeRequest.IsGerman)
		if err != nil {
			http.Error(w, "Error generating recipe", http.StatusInternalServerError)
			log.Println("Error generating recipe name:", err)
			return
		}
	}

	resp := Recipe{
		Recipename: recipename,
		Recipe:     recipe,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		http.Error(w, "Error encoding JSON response", http.StatusInternalServerError)
	}
}

func HandleGenerateRecipeByVoice(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "Failed to get the audio file", http.StatusBadRequest)
		return
	}

	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {

		}
	}(file)

	var isGerman bool
	if risGerman := r.FormValue("isGerman"); risGerman != "" {
		if risGerman == "true" {
			isGerman = true
		} else if risGerman == "false" {
			isGerman = false
		} else {
			http.Error(w, "isGerman must be 'true' or 'false'", http.StatusBadRequest)
			return
		}
	} else {
		http.Error(w, "isGerman cannot be empty", http.StatusBadRequest)
		return
	}

	transcript, err := goopenAIgenerateTranscript(file)
	if err != nil {
		http.Error(w, "Failed to generate recipe", http.StatusInternalServerError)
		log.Println("Error transcribing recipe:", err)
		return
	}
	log.Printf("Transcript: %s\n", transcript)

	if !isRecipeRelated(transcript) {
		log.Printf("Input rejected by LLM judge")
		http.Error(w, "Input rejected by LLM judge", http.StatusBadRequest)
		return
	}

	recipe, err := openAIgenerateRecipe(transcript, isGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		log.Println("Error generating recipe via voice:", err)
		return
	}

	recipename, err := openAIgenerateRecipeName(recipe, isGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		log.Println("Error generating recipe name:", err)
		return
	}

	resp := Recipe{
		Recipename: recipename,
		Recipe:     recipe,
		Transcript: transcript,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		http.Error(w, "Error encoding JSON response", http.StatusInternalServerError)
	}

}

func HandleReprompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	var req RecipeChangeRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	updatedRecipe, err := goopenaiUpdateRecipe(req.Recipe, req.ChangePrompt)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		return
	}

	resp := Recipe{
		Recipe: updatedRecipe,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		log.Printf("Error encoding JSON response: %v\n\n", err)
		http.Error(w, "Error encoding JSON response", http.StatusInternalServerError)
	}
}

func GenerateRecipeByLink(URL string, isGerman bool) (string, string, error) {
	websitecontent, err := GetWebsite(URL)
	if err != nil {
		fmt.Println("Error fetching website content:", err)
		return "", "", err
	}

	recipe, err := openAIgenerateRecipeLink(websitecontent, isGerman)
	if err != nil {
		fmt.Println("Error generating recipe:", err)
		return "", "", err
	}

	recipename, err := openAIgenerateRecipeName(recipe, isGerman)
	if err != nil {
		fmt.Println("Error generating recipe name:", err)
		return "", "", err
	}

	return recipename, recipe, nil
}

func GenerateRecipeByName(RecipeName string, isGerman bool) (string, error) {
	recipe, err := openAIgenerateRecipe(RecipeName, isGerman)
	if err != nil {
		fmt.Println("Error generating recipe:", err)
		return "", err
	}

	log.Println("Route /api/v1/generate/by-name: Recipe generated successfully!")

	return recipe, nil
}

func GenerateRecipeByImage(Image string, isGerman bool) (string, error) {
	recipe, err := goopenAIgenerateRecipeImage(Image, isGerman)
	if err != nil {
		fmt.Println("Error generating recipe:", err)
		return "", err
	}

	return recipe, nil
}

func AddRecipeToDB(userID int, RecipeName string, Recipe string, RecipeCategory string) error {
	_, err := pool.Exec(context.Background(), "insert into recipes(user_id, title, content, category) values($1, $2, $3, $4)", userID, RecipeName, Recipe, RecipeCategory)
	if err != nil {
		log.Printf("Inserting Recipe failed: %v\n\n", err)
		return err
	}

	log.Printf("added recipe %s to database", RecipeName)
	return nil
}

func RemoveRecipeFromDB(userID int, recipeID int) error {
	_, err := pool.Exec(context.Background(), "delete from recipes where user_id = $1 and id = $2", userID, recipeID)
	if err != nil {
		log.Printf("Deleting recipe failed: %v\n\n", err)
		return err
	}

	log.Printf("deleted recipe with id %v from database", recipeID)
	return nil
}

func openAIclient() *openai.Client {
	OpenAIKey, found := os.LookupEnv("OPENAI_KEY")
	if !found {
		log.Println("OPENAI_KEY not found")
		return nil
	}

	client := openai.NewClient(
		option.WithAPIKey(OpenAIKey),
	)

	return client
}

func openAIgenerateRecipe(recipeDescription string, isGerman bool) (string, error) {
	client := openAIclient()

	var systemmessage openai.ChatCompletionMessageParamUnion
	var usermessage openai.ChatCompletionMessageParamUnion

	if isGerman {
		systemmessage = openai.SystemMessage(germanSystemMessage)
		usermessage = openai.UserMessage("Erstelle ein Rezept für folgende Beschreibung: " + recipeDescription)
	} else {
		systemmessage = openai.SystemMessage(englishSystemMessage)
		usermessage = openai.UserMessage("Generate a recipe for the following description: " + recipeDescription)
	}

	completion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			systemmessage,
			usermessage,
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		return "", err
	}

	return completion.Choices[0].Message.Content, nil
}

func openAIgenerateRecipeName(Recipe string, isGerman bool) (string, error) {
	client := openAIclient()

	var systemmessage openai.ChatCompletionMessageParamUnion
	var usermessage openai.ChatCompletionMessageParamUnion

	if isGerman {
		systemmessage = openai.SystemMessage("Du bist ein Agent, der nur mit dem Rezeptnamen antwortet. Maximal 2 Wörter.")
		usermessage = openai.UserMessage("Generiere einen Rezeptnamen für: " + Recipe)
	} else {
		systemmessage = openai.SystemMessage("You only respond with the recipe name. 2 words max.")
		usermessage = openai.UserMessage("Generate a recipe name for: " + Recipe)
	}

	recipename, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			systemmessage,
			usermessage,
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		return "", err
	}

	return recipename.Choices[0].Message.Content, nil
}

func openAIgenerateRecipeLink(Recipe string, isGerman bool) (string, error) {
	client := openAIclient()

	var systemmessage openai.ChatCompletionMessageParamUnion

	if isGerman {
		systemmessage = openai.SystemMessage(germanSystemMessage)
	} else {
		systemmessage = openai.SystemMessage(englishSystemMessage)
	}

	var usermessage openai.ChatCompletionMessageParamUnion
	if isGerman {
		usermessage = openai.UserMessage("Ändere das Rezept in Markdown-Format: " + Recipe)
	} else {
		usermessage = openai.UserMessage("Change to markdown format: " + Recipe)
	}

	completion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			systemmessage,
			usermessage,
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		return "", err
	}

	return completion.Choices[0].Message.Content, nil
}

func goopenAIgenerateRecipeImage(RecipeBase64 string, isGerman bool) (string, error) {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	var SystemMessage string
	if isGerman {
		SystemMessage = germanSystemMessage
	} else {
		SystemMessage = englishSystemMessage
	}

	response, err := client.CreateChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model: goopenai.GPT4oMini,
		Messages: []goopenai.ChatCompletionMessage{
			{
				Role: goopenai.ChatMessageRoleUser,
				MultiContent: []goopenai.ChatMessagePart{
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: SystemMessage,
					},
					{
						Type: goopenai.ChatMessagePartTypeImageURL,
						ImageURL: &goopenai.ChatMessageImageURL{
							URL: "data:image/jpeg;base64," + RecipeBase64,
						},
					},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}

	return response.Choices[0].Message.Content, nil
}

func goopenAIgenerateTranscript(voicemessage multipart.File) (string, error) {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	req := goopenai.AudioRequest{
		Model:    goopenai.Whisper1,
		Reader:   voicemessage,
		FilePath: "voicemessage.mp3", // fake name necessary for the request
	}

	response, err := client.CreateTranscription(context.Background(), req)
	if err != nil {
		return "", err
	}

	return response.Text, nil
}

func goopenAIgenerateRecipeCategory(Recipe string) string {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	response, err := client.CreateChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model: goopenai.GPT4oMini,
		Messages: []goopenai.ChatCompletionMessage{
			{
				Role: goopenai.ChatMessageRoleUser,
				MultiContent: []goopenai.ChatMessagePart{
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: "What is the category of this recipe? Currently only Hauptgericht, Vorspeise, Brot, Dessert are supported. Answer with a single word nothing else",
					},
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: Recipe,
					},
				},
			},
		},
	})
	if err != nil {
		log.Println("Error generating recipe category:", err)
		return ""
	}

	categories := []string{"Hauptgericht", "Vorspeise", "Brot", "Dessert"}
	category := response.Choices[0].Message.Content
	for _, c := range categories {
		if strings.Contains(category, c) {
			return c
		}
	}
	log.Println("Recipe category not found, defaulting to Sonstiges")
	return "Sonstiges"
}

func goopenAIChatCompletion(ctx context.Context, systemPrompt, userPrompt string, model string) (string, error) {
	client := openAIclient()

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt),
		openai.UserMessage(userPrompt),
	}

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: openai.F(messages),
		Model:    openai.F(model),
	})

	if err != nil {
		return "", fmt.Errorf("chat completion error: %w", err)
	}

	if len(completion.Choices) == 0 {
		return "", errors.New("no completion choices returned")
	}

	return completion.Choices[0].Message.Content, nil
}

func goopenaiUpdateRecipe(Recipe string, Prompt string) (string, error) {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	response, err := client.CreateChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model: goopenai.GPT4oMini,
		Messages: []goopenai.ChatCompletionMessage{
			{
				Role: goopenai.ChatMessageRoleUser,
				MultiContent: []goopenai.ChatMessagePart{
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: "The user requested that you change the following recipe: " +
							Recipe + " according to the following prompt: " + Prompt +
							" keep the recipe in the same format and language" +
							" only respond with the recipe and nothing else",
					},
				},
			},
		},
	})
	if err != nil {
		log.Printf("Error updating recipe: %v\n", err)
		return "Error while updating Recipe", err
	}
	return response.Choices[0].Message.Content, nil
}

func HandlerJudgeMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !HandlerIsRecipeRelated(r) {
			log.Printf("Input rejected by LLM judge")
			http.Error(w, "Input rejected by LLM judge", http.StatusBadRequest)
			return
		}
		next(w, r)
	}
}

func HandlerIsRecipeRelated(r *http.Request) bool {
	client := openAIclient()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v\n", err)
		return false
	}

	log.Printf("Raw body: %s\n", string(bodyBytes))

	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var req RecipeGenerateRequest
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		log.Printf("Error decoding request: %v\n", err)
		return false
	}

	var systemmessage openai.ChatCompletionMessageParamUnion
	systemmessage = openai.SystemMessage(judgeSystemMessage)

	var usermessage openai.ChatCompletionMessageParamUnion
	usermessage = openai.UserMessage("Is this input related to a recipe? Only answer with 'yes' or 'no'" + req.RecipeDescription)

	completion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			systemmessage,
			usermessage,
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		log.Println("Error judging input:", err)
		return false
	}

	log.Printf("Completion response: %s\n", completion.Choices[0].Message.Content)
	return strings.Contains(completion.Choices[0].Message.Content, "yes")
}

func isRecipeRelated(recipe string) bool {
	result, err := goopenAIChatCompletion(
		context.TODO(),
		judgeSystemMessage,
		"Is this input related to a recipe? Only answer with 'yes' or 'no'"+recipe,
		openai.ChatModelGPT4oMini,
	)

	if err != nil {
		log.Println("Error judging input:", err)
		return false
	}

	return strings.Contains(strings.ToLower(result), "yes")
}

func fixRecipe(recipe string) (string, error) {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	response, err := client.CreateChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model: goopenai.GPT4oMini,
		Messages: []goopenai.ChatCompletionMessage{
			{
				Role: goopenai.ChatMessageRoleUser,
				MultiContent: []goopenai.ChatMessagePart{
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: "Fix this recipe regarding formatting" +
							"All ingredients need to be in metric units." +
							"If in german do not be formal e.g. dont use Sie" +
							"remove formatting errors e.g. ```markdown```",
					},
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: recipe,
					},
				},
			},
		},
	})
	if err != nil {
		return "Error while fixing Recipe", err
	}
	return response.Choices[0].Message.Content, nil
}

func EncodeImageToBase64(imageData io.Reader) (string, error) {
	data, err := io.ReadAll(imageData)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	base64Data := base64.StdEncoding.EncodeToString(data)
	return base64Data, nil
}

func GetWebsite(link string) (string, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return "", err
	}

	// Set headers to mimic a browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(res.Body)

	content, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func mockAzureAuth(r *http.Request) {
	// r.Header.Set("X-MS-CLIENT-PRINCIPAL-ID", "8e888379-a76f-4aba-9860-183b913c0719")
	r.Header.Set("X-MS-CLIENT-PRINCIPAL-ID", "8e888379-a76f-4aba-5678-183b913c0719")
	r.Header.Set("X-MS-CLIENT-PRINCIPAL-NAME", "ilijakovac1@googlemail.com")
	r.Header.Set("X-MS-CLIENT-PRINCIPAL-IDP", "aad")
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("LOCAL_DEV") == "true" {
		mockAzureAuth(r)
	}
	oauthID := r.Header.Get("X-MS-CLIENT-PRINCIPAL-ID")
	userName := r.Header.Get("X-MS-CLIENT-PRINCIPAL-NAME")
	provider := r.Header.Get("X-MS-CLIENT-PRINCIPAL-IDP")
	if oauthID == "" || userName == "" || provider == "" {
		http.Error(w, "Unauthorized: Missing authentication headers", http.StatusUnauthorized)
		return
	}

	var storageAccountName string
	err := pool.QueryRow(context.Background(), "SELECT subdomain FROM users WHERE oauth_id = $1", oauthID).Scan(&storageAccountName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// User doesn't exist, create new user
			storageAccountName, err = randomString()
			if err != nil {
				log.Printf("Failed to generate random string: %v\n", err)
				http.Error(w, "Failed to generate random string", http.StatusInternalServerError)
				return
			}

			_, err = pool.Exec(context.Background(), "INSERT INTO users (oauth_id, name, oauth_provider, subdomain) VALUES ($1, $2, $3, $4)", oauthID, userName, provider, storageAccountName)
			if err != nil {
				http.Error(w, "Database Error Failed to create user", http.StatusInternalServerError)
				log.Printf("Failed to create user %s with error: %v\n", userName, err)
				return
			}

			err = bootstrapStorageAccount(storageAccountName, oauthID)
			if err != nil {
				http.Error(w, "Failed to bootstrap static website", http.StatusInternalServerError)
				log.Printf("Failed to bootstrap static website for user %s with error: %v\n", userName, err)
				return
			}

			userID, _, _ := GetUserInformation(oauthID)
			err = templateRecipesBlob(storageAccountName, userID)
			if err != nil {
				log.Printf("Failed to template recipes for user %s with error: %v\n", userName, err)
				http.Error(w, "Failed to template recipes", http.StatusInternalServerError)
				return
			}
		} else {
			log.Printf("Database error: %v\n", err)
			http.Error(w, "Database Error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("X-USER-NAME", userName)
	w.Header().Set("X-USER-ID", oauthID)
	w.Header().Set("X-USER-PROVIDER", provider)
	w.Header().Set("X-USER-STORAGEACCOUNT", storageAccountName)
	w.WriteHeader(http.StatusOK)
}

func randomString() (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %v", err)
	}

	result := make([]byte, 8)
	for i := range b {
		result[i] = charset[int(b[i])%len(charset)]
	}

	return string(result), nil
}

func GetUserInformation(oauthid string) (int, string, error) {
	var userID int
	var subdomain string
	err := pool.QueryRow(context.Background(), "SELECT id, subdomain FROM users WHERE oauth_id = $1", oauthid).Scan(&userID, &subdomain)
	if err != nil {
		return 0, "", err
	}
	return userID, subdomain, nil
}

func GetRecipes(userid int) ([]Recipe, error) {
	rows, err := pool.Query(context.Background(), "SELECT id, title, content, category FROM recipes WHERE user_id = $1", userid)
	if err != nil {
		log.Printf("Failed to query recipes: %v", err)
		return nil, err
	}
	defer rows.Close()

	var recipes []Recipe
	for rows.Next() {
		var recipe Recipe
		err := rows.Scan(&recipe.ID, &recipe.Recipename, &recipe.Recipe, &recipe.Category)
		if err != nil {
			log.Printf("Failed to scan recipe: %v", err)
			return nil, err
		}
		recipes = append(recipes, recipe)
	}
	return recipes, nil
}

func templateRecipesBlob(storageAccountName string, userid int) error {
	var title = "# Rezepte\n\n"
	var recipes []Recipe
	recipes, err := GetRecipes(userid)
	if err != nil {
		log.Printf("Failed to get recipes from database, error: %s", err)
		return err
	}

	var recipesTemplateMain string
	var recipesTemplateBread string
	var recipesTemplateStarter string
	var recipesTemplateDessert string
	var recipesTemplateMisc string

	for _, recipe := range recipes {
		linkFormat := "- [" + recipe.Recipename + "](/?recipe=" + strings.ReplaceAll(recipe.Recipename, " ", "-") + ")\n"
		switch recipe.Category {
		case "Hauptgericht":
			recipesTemplateMain += linkFormat
		case "Brot":
			recipesTemplateBread += linkFormat
		case "Vorspeise":
			recipesTemplateStarter += linkFormat
		case "Dessert":
			recipesTemplateDessert += linkFormat
		default:
			recipesTemplateMisc += linkFormat
		}
	}

	combinedTemplate := title + "🍝 Hauptgerichte\n" + recipesTemplateMain +
		"\n🥗 Vorspeisen\n" + recipesTemplateStarter +
		"\n🧁 Desserts\n" + recipesTemplateDessert +
		"\n🍞 Brot\n" + recipesTemplateBread +
		"\n🍴 Sonstiges\n" + recipesTemplateMisc

	err = addBlob(storageAccountName, "recipes.md", combinedTemplate)
	if err != nil {
		log.Printf("Failed to add recipes to $web container of storage account  %s, error: %s", storageAccountName, err)
		return err
	}

	return nil
}
