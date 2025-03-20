package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
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
)

type RecipeRequest struct {
	Recipename string `json:"recipename"`
	Recipe     string `json:"recipe"`
	IsGerman   bool   `json:"isGerman"`
}

type RecipeGenerateRequest struct {
	Recipename string `json:"recipename"`
	Details    string `json:"details"`
	IsGerman   bool   `json:"isGerman"`
}

type RecipeLinkRequest struct {
	URL      string `json:"url"`
	IsGerman bool   `json:"isGerman"`
}

type RecipeImageRequest struct {
	Recipename string `json:"recipename"`
	IsGerman   bool   `json:"isGerman"`
}

type RecipeResponse struct {
	Recipename string `json:"recipename"`
	Recipe     string `json:"recipe"`
	Transcript string `json:"transcript,omitempty"`
}

func main() {
	mux := http.NewServeMux()

	if !validateEnvVars() {
		log.Fatal("Missing environment variables")
		return
	}

	mux.HandleFunc("/health", HandleHealth)

	mux.HandleFunc("/api/v1/add-recipe", HandleAddRecipe)

	mux.HandleFunc("/api/v1/generate/by-name", HandleGenerateByName)

	mux.HandleFunc("/api/v1/generate/by-link", HandleGenerateByLink)

	mux.HandleFunc("/api/v1/generate/by-image", HandleGenerateByImage)

	mux.HandleFunc("/api/v1/transform", HandleTransformRecipe)

	mux.HandleFunc("/api/v1/login", HandleLogin)

	mux.HandleFunc("GET /api/v1/get-recipes", HandleGetRecipes)

	mux.HandleFunc("POST /api/v1/generate/by-voice", HandleGenerateRecipeByVoice)

	log.Println("Server is running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", logRequests(mux)))
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

	if os.Getenv("LOCAL_DEV") == "true" {
		mockAzureAuth(r)
	}

	oauthID := r.Header.Get("X-MS-CLIENT-PRINCIPAL-ID")

	userID, err := GetUserID(oauthID)
	if err != nil {
		http.Error(w, "Error getting user ID", http.StatusInternalServerError)
		return
	}
	err = AddRecipeToDB(userID, req.Recipename, req.Recipe)
	if err != nil {
		http.Error(w, "Error adding recipe", http.StatusInternalServerError)
		return
	}

	recipename := strings.ReplaceAll(req.Recipename, " ", "-")
	recipePath := "recipes/" + recipename + ".md"

	err = addBlob("$web", recipePath, req.Recipe)
	if err != nil {
		http.Error(w, "Failed to update recipe", http.StatusInternalServerError)
		return
	}
	err = templateRecipesBlob("$web", userID)
	if err != nil {
		http.Error(w, "Failed to template recipes", http.StatusInternalServerError)
		return
	}

	_, _ = fmt.Fprint(w, "Recipe added successfully!")
}
func HandleGenerateByName(w http.ResponseWriter, r *http.Request) {
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

	if req.Recipename == "" {
		http.Error(w, "Missing recipename", http.StatusBadRequest)
		return
	}

	recipe, err := GenerateRecipeByName(req.Recipename, req.Details, req.IsGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		return
	}

	resp := RecipeResponse{
		Recipename: req.Recipename,
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

	resp := RecipeResponse{
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

	resp := RecipeResponse{
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

func HandleTransformRecipe(w http.ResponseWriter, r *http.Request) {
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

	transformedRecipe, err := TransformRecipe(req.Recipe, req.IsGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		return
	}

	var recipename string

	if req.Recipename != "" {
		recipename = req.Recipename
	} else {
		recipename, err = openAIgenerateRecipeName(transformedRecipe, req.IsGerman)
		if err != nil {
			http.Error(w, "Error generating recipe", http.StatusInternalServerError)
			log.Println("Error generating recipe name:", err)
			return
		}
	}

	resp := RecipeResponse{
		Recipename: recipename,
		Recipe:     transformedRecipe,
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
		log.Println("Error generating recipe:", err)
		return
	}

	recipe, err := openAIgenerateRecipe(transcript, "", isGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		log.Println("Error generating recipe:", err)
		return
	}

	recipename, err := openAIgenerateRecipeName(recipe, isGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		log.Println("Error generating recipe name:", err)
		return
	}

	resp := RecipeResponse{
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

func GenerateRecipeByName(RecipeName string, Details string, isGerman bool) (string, error) {
	recipe, err := openAIgenerateRecipe(RecipeName, Details, isGerman)
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

func TransformRecipe(Recipe string, isGerman bool) (string, error) {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	var SystemMessage string

	if isGerman {
		SystemMessage = "Du bist ein Agent, der das Format von Rezepten ändert." +
			"Das Rezept muss im Markdown-Format sein: " +
			"# <Rezeptname>\n" +
			"## Zutaten\n" +
			"- **<MENGE>** Zutat \n" +
			"## Anweisung 1\n" +
			"## Anweisung 2" +
			"Alle Zutaten müssen in metrischen Einheiten angegeben werden."
	} else {
		SystemMessage = "You are an agent that changes the format recipes" +
			"The recipe needs to be in markdown format: " +
			"# <Recipe Name>\n" + "## Ingredients\n" + "- **<UNIT>** Ingredient \n" + "## Instructionset 1\n" + "## Instructionset 2" +
			"All ingredients need to be in metric units."
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
						Type: goopenai.ChatMessagePartTypeText,
						Text: Recipe,
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

func AddRecipeToDB(userID int, RecipeName string, Recipe string) error {
	conn, err := pgx.Connect(context.Background(), os.Getenv("DB_URL"))
	if err != nil {
		_, err := fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		if err != nil {
			return err
		}
		os.Exit(1)
	}
	defer func(conn *pgx.Conn, ctx context.Context) {
		err := conn.Close(ctx)
		if err != nil {

		}
	}(conn, context.Background())

	_, err = conn.Exec(context.Background(), "insert into recipes(user_id, title, content) values($1, $2, $3)", userID, RecipeName, Recipe)
	if err != nil {
		log.Printf("Inserting Recipe failed: %v\n\n", err)
		return err
	}

	log.Printf("added recipe %s to database", RecipeName)
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

func openAIgenerateRecipe(Recipename string, Details string, isGerman bool) (string, error) {
	client := openAIclient()

	var systemmessage openai.ChatCompletionMessageParamUnion
	var usermessageString string

	if isGerman {
		systemmessage = openai.SystemMessage(germanSystemMessage)
		usermessageString = "Erstelle ein Rezept für " + Recipename
	} else {
		systemmessage = openai.SystemMessage(englishSystemMessage)
		usermessageString = "Generate a recipe for " + Recipename
	}

	var usermessage openai.ChatCompletionMessageParamUnion
	if Details != "" {
		usermessage = openai.UserMessage(usermessageString + " details: " + Details)
	} else {
		usermessage = openai.UserMessage(usermessageString + Recipename)
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
		FilePath: "voicemessage.m4a",
	}

	response, err := client.CreateTranscription(context.Background(), req)
	if err != nil {
		return "", err
	}

	return response.Text, nil
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
							"If in german do not be formally e.g. dont use Sie" +
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
	// Read all the data from the image
	data, err := io.ReadAll(imageData)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Encode the data to a base64 string
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
	r.Header.Set("X-MS-CLIENT-PRINCIPAL-ID", "8e888379-a76f-4aba-9860-183b913c0719")
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
	conn, err := pgx.Connect(context.Background(), os.Getenv("DB_URL"))
	if err != nil {
		http.Error(w, "Failed to connect to database", http.StatusInternalServerError)
		return
	}

	var exists bool
	err = conn.QueryRow(context.Background(), "select exists(SELECT 1 FROM users WHERE oauth_id = $1)", oauthID).Scan(&exists)

	if !exists {
		_, err = conn.Exec(context.Background(), "INSERT INTO users (oauth_id, name, oauth_provider) VALUES ($1, $2, $3)", oauthID, userName, provider)
		if err != nil {
			http.Error(w, "Database Error Failed to create user", http.StatusInternalServerError)
			log.Println("Login: Database error Failed to create user")
			return
		}

		// go func() {
		// 	err := createStaticWebsite(oauthID)
		// 	if err != nil {
		// 		log.Println("Login: Failed to create static website")
		// 	}
		// }()
	} else {
		log.Println("Login: User already exists")
	}

	w.Header().Set("X-USER-NAME", userName)
	w.Header().Set("X-USER-ID", oauthID)
	w.Header().Set("X-USER-PROVIDER", provider)

	w.WriteHeader(http.StatusOK)
}

func GetUserID(oauthid string) (int, error) {
	conn, err := pgx.Connect(context.Background(), os.Getenv("DB_URL"))
	if err != nil {
		return 0, err
	}
	userID := 0
	err = conn.QueryRow(context.Background(), "SELECT id FROM users WHERE oauth_id = $1", oauthid).Scan(&userID)
	if err != nil {
		return 0, err
	}
	return userID, nil
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

	userID, err := GetUserID(oauthID)
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

func GetRecipes(userid int) ([]RecipeResponse, error) {
	conn, err := pgx.Connect(context.Background(), os.Getenv("DB_URL"))
	if err != nil {
		return nil, err
	}

	rows, err := conn.Query(context.Background(), "SELECT title, content FROM recipes WHERE user_id = $1", userid)
	if err != nil {
		return nil, err
	}

	var recipes []RecipeResponse
	for rows.Next() {
		var recipe RecipeResponse
		err := rows.Scan(&recipe.Recipename, &recipe.Recipe)
		if err != nil {
			return nil, err
		}
		recipes = append(recipes, recipe)
	}
	return recipes, nil
}

func templateRecipesBlob(containername string, userid int) error {
	var recipesTemplate = "# Rezepte\n\n## 🍝 Hauptgerichte"
	var recipes []RecipeResponse
	recipes, err := GetRecipes(userid)
	if err != nil {
		log.Println("Failed to get recipes")
		return err
	}

	for _, recipe := range recipes {
		recipesTemplate = recipesTemplate + "\n - [" + recipe.Recipename + "](/?recipe=" + strings.ReplaceAll(recipe.Recipename, " ", "-") + ")"
	}

	err = addBlob("$web", "recipes.md", recipesTemplate)
	if err != nil {
		log.Printf("Failed to add recipes to bucket %s, error: %s", containername, err)
		return err
	}

	return nil
}
