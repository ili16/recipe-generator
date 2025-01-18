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

	"github.com/google/go-github/v66/github"
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
		"### Schritt 1\n" +
		"### Schritt 2\n" +
		"### Schritt N" +
		"Alle Zutaten müssen in metrischen Einheiten angegeben werden."

	englishSystemMessage = "You are an agent that changes the format recipes" +
		"The recipe needs to be in markdown format: " +
		"# <Recipe Name>\n" + "## Ingredients\n" + "- **<UNIT>** Ingredient \n" + "## Preparation" + "### Instructionset 1\n" + "### Instructionset 2" +
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
	RecipeName string `json:"recipename"`
	IsGerman   bool   `json:"isGerman"`
}

func main() {

	mux := http.NewServeMux()

	if !validateEnvVars() {
		log.Fatal("Missing environment variables")
		return
	}

	mux.HandleFunc("/health", HandleHealth)

	mux.HandleFunc("/add-recipe", HandleAddRecipe)

	mux.HandleFunc("/api/v1/generate/by-name", HandleGenerateByName)

	mux.HandleFunc("/api/v1/generate/by-link", HandleGenerateByLink)

	mux.HandleFunc("/api/v1/generate/by-image", HandleGenerateByImage)

	mux.HandleFunc("/api/v1/transform", HandleTransformRecipe)

	mux.HandleFunc("/test/get-content", func(w http.ResponseWriter, r *http.Request) {
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
			http.Error(w, "Missing url", http.StatusBadRequest)
			return
		}

		websitecontent, err := GetWebsite(req.URL)
		if err != nil {
			http.Error(w, "Error fetching website content", http.StatusInternalServerError)
			return
		}
		log.Println("Website content:", websitecontent)

		log.Println("Length of website content:", len(websitecontent))

		_, _ = fmt.Fprintf(w, "New recipe created successfully: %s", websitecontent)
	})

	log.Println("Server is running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", logRequests(addCORSHeaders(mux))))
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: Method=%s, URL=%s, Headers=%v, RemoteAddr=%s",
			r.Method, r.URL.String(), r.Header, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func addCORSHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedOrigins := map[string]bool{
			"https://recipe-generator.ili16.de": true,
			"http://192.168.10.163:1000":        true,
		}

		origin := r.Header.Get("Origin")
		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func validateEnvVars() bool {
	_, found := os.LookupEnv("OPENAI_KEY")
	if !found {
		log.Println("OPENAI_KEY not found")
		return false
	}

	_, found = os.LookupEnv("GITHUB_PAT")
	if !found {
		log.Println("GITHUB_PAT not found")
		return false
	}
	return true
}

func GithubClient() *github.Client {
	githubPAT, found := os.LookupEnv("GITHUB_PAT")
	if !found {
		log.Println("GITHUB_PAT not found")
		return nil
	}

	client := github.NewClient(nil).WithAuthToken(githubPAT)
	return client
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

	err = AddRecipe(req.Recipename, req.Recipe)
	if err != nil {
		http.Error(w, "Error adding recipe", http.StatusInternalServerError)
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

	_, _ = fmt.Fprint(w, recipe)
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

	recipe, err := GenerateRecipeByLink(req.URL, req.IsGerman)
	if err != nil {
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		return
	}

	_, _ = fmt.Fprintf(w, recipe)
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
		recipeRequest.RecipeName = recipeName
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

	var Recipename string

	if recipeRequest.RecipeName != "" {
		Recipename = recipeRequest.RecipeName
	} else {
		Recipename, err = openAIgenerateRecipeName(recipe, recipeRequest.IsGerman)
		if err != nil {
			http.Error(w, "Error generating recipe", http.StatusInternalServerError)
			log.Println("Error generating recipe name:", err)
			return
		}
	}

	err = AddRecipe(Recipename, recipe)
	if err != nil {
		http.Error(w, "Error adding recipe", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(map[string]string{"recipe": recipe})
	if err != nil {
		log.Println("Failed to send response:", err)
	}

	_, err = fmt.Fprintf(w, recipe)

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

	var Recipename string

	if req.Recipename != "" {
		Recipename = req.Recipename
	} else {
		Recipename, err = openAIgenerateRecipeName(transformedRecipe, req.IsGerman)
		if err != nil {
			http.Error(w, "Error generating recipe", http.StatusInternalServerError)
			log.Println("Error generating recipe name:", err)
			return
		}
	}

	err = AddRecipe(Recipename, transformedRecipe)
	if err != nil {
		http.Error(w, "Error adding recipe", http.StatusInternalServerError)
		return
	}

	_, err = fmt.Fprintf(w, transformedRecipe)
}

func GenerateRecipeByLink(URL string, isGerman bool) (string, error) {

	websitecontent, err := GetWebsite(URL)
	if err != nil {
		fmt.Println("Error fetching website content:", err)
		return "", err
	}

	recipe, err := openAIgenerateRecipeLink(websitecontent, isGerman)
	if err != nil {
		fmt.Println("Error generating recipe:", err)
		return "", err
	}

	recipename, err := openAIgenerateRecipeName(recipe, isGerman)
	if err != nil {
		fmt.Println("Error generating recipe name:", err)
		return "", err
	}

	err = AddRecipe(recipename, recipe)
	if err != nil {
		fmt.Println("Error adding recipe:", err)
		return "", err
	}

	return recipename, nil
}

func GenerateRecipeByName(RecipeName string, Details string, isGerman bool) (string, error) {
	recipe, err := openAIgenerateRecipe(RecipeName, Details, isGerman)
	if err != nil {
		fmt.Println("Error generating recipe:", err)
		return "", err
	}

	err = AddRecipe(RecipeName, recipe)
	if err != nil {
		fmt.Println("Error adding recipe:", err)
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

func CreateRef(RecipeName string) (*string, error) {
	client := GithubClient()
	baseRef, _, err := client.Git.GetRef(context.Background(), "ili16", "ili16.github.io", "refs/heads/main")
	if err != nil {
		fmt.Println("Error fetching base reference:", err)
		return nil, err
	}

	newRef := &github.Reference{
		Ref:    github.String("refs/heads/" + RecipeName),
		Object: &github.GitObject{SHA: baseRef.Object.SHA},
	}

	_, _, err = client.Git.CreateRef(context.Background(), "ili16", "ili16.github.io", newRef)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	return newRef.Ref, err
}

func AddRecipe(RecipeName string, Content string) error {
	debugMode := os.Getenv("DEBUG_MODE")
	if debugMode == "true" {
		return nil
	}

	client := GithubClient()

	RecipeReplaced := strings.ReplaceAll(RecipeName, " ", "-")

	newRef, err := CreateRef(RecipeReplaced)
	if err != nil {
		fmt.Println("Error creating reference:", err)
		return err
	}

	_, _, err = client.Repositories.CreateFile(
		context.Background(),
		"ili16",
		"ili16.github.io",
		"recipes/"+RecipeReplaced+".md",
		&github.RepositoryContentFileOptions{
			Message: github.String("Add " + RecipeName + " recipe"),
			Content: []byte(Content),
			Branch:  newRef,
		})
	if err != nil {
		fmt.Println(err)
		return err
	}

	err = appendRecipeListFile(RecipeName, newRef)
	if err != nil {
		fmt.Println("Error appending recipe to list:", err)
		return err
	}
	fmt.Println("Recipe added successfully!")

	return nil
}

func getRecipeListFile() []string {
	client := GithubClient()
	recipes, _, _, err := client.Repositories.GetContents(context.Background(), "ili16", "ili16.github.io", "recipes.md", nil)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	recipe, err := recipes.GetContent()
	if err != nil {
		fmt.Println(err)
		return nil
	}

	lines := strings.Split(recipe, "\n")

	return lines
}

func appendRecipeListFile(RecipeName string, newRef *string) error {
	client := GithubClient()
	recipeList := getRecipeListFile()

	RecipeReplaced := strings.ReplaceAll(RecipeName, " ", "-")
	newRecipe := fmt.Sprintf("- [%s](?recipe=%s)", RecipeName, RecipeReplaced)

	recipeList = append(recipeList, newRecipe)

	_, _, err := client.Repositories.UpdateFile(
		context.Background(),
		"ili16",
		"ili16.github.io",
		"recipes.md",
		&github.RepositoryContentFileOptions{
			Message: github.String("Update recipe list with " + RecipeName),
			Content: []byte(strings.Join(recipeList, "\n")),
			SHA:     github.String(getFileSHA("recipes.md")),
			Branch:  newRef,
		})
	if err != nil {
		fmt.Println("Error updating recipe list:", err)
		return err
	}

	return nil
}

func getFileSHA(filepath string) string {
	client := GithubClient()
	file, _, _, err := client.Repositories.GetContents(context.Background(), "ili16", "ili16.github.io", filepath, nil)
	if err != nil {
		fmt.Println("Error fetching file SHA:", err)
		return ""
	}
	return file.GetSHA()
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
