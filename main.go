package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v66/github"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type RecipeRequest struct {
	Recipename string `json:"recipename"`
	Recipe     string `json:"recipe"`
}

type RecipeGenerateRequest struct {
	Recipename string `json:"recipename"`
	Details    string `json:"details"`
}

type RecipeLinkRequest struct {
	URL string `json:"url"`
}

func main() {
	http.HandleFunc("/add-recipe", func(w http.ResponseWriter, r *http.Request) {
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
	})

	http.HandleFunc("/api/v1/generate/by-name", func(w http.ResponseWriter, r *http.Request) {
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

		err = GenerateRecipeByName(req.Recipename, req.Details)
		if err != nil {
			http.Error(w, "Error generating recipe", http.StatusInternalServerError)
			return
		}

		_, _ = fmt.Fprint(w, "Recipe generated successfully!")
	})

	http.HandleFunc("/api/v1/generate/by-link", func(w http.ResponseWriter, r *http.Request) {

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

		recipe, err := GenerateRecipeByLink(req.URL)
		if err != nil {
			http.Error(w, "Error generating recipe", http.StatusInternalServerError)
			return
		}

		_, _ = fmt.Fprintf(w, "New recipe created successfully: %s", recipe)
	})

	http.HandleFunc("/test/get-content", func(w http.ResponseWriter, r *http.Request) {
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
	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}

func GenerateRecipeByLink(URL string) (string, error) {

	websitecontent, err := GetWebsite(URL)
	if err != nil {
		fmt.Println("Error fetching website content:", err)
		return "", err
	}

	recipe, err := openAIgenerateRecipeLink(websitecontent)
	if err != nil {
		fmt.Println("Error generating recipe:", err)
		return "", err
	}

	recipename, err := openAIgenerateRecipeName(recipe)
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

func GenerateRecipeByName(RecipeName string, Details string) error {
	recipe, err := openAIgenerateRecipe(RecipeName, Details)
	if err != nil {
		fmt.Println("Error generating recipe:", err)
		return err
	}

	err = AddRecipe(RecipeName, recipe)
	if err != nil {
		fmt.Println("Error adding recipe:", err)
		return err
	}

	log.Println("Recipe generated successfully!")

	return nil
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

func openAIgenerateRecipe(Recipename string, Details string) (string, error) {
	client := openAIclient()

	systemmessage := openai.SystemMessage(
		"You are a top chef inspired by the world's best recipes." +
			" You are creating a new recipe for a dish called " + Recipename + "." +
			"The recipe needs to be in markdown format: " +
			"# <Recipe Name>\n" + "## Ingredients\n" + "- **<UNIT>** Ingredient \n" + "## Instructionset 1\n" + "## Instructionset 2" +
			"All ingredients need to be in metric units.")
	var usermessage openai.ChatCompletionMessageParamUnion
	if Details != "" {
		usermessage = openai.UserMessage("Generate a recipe for " + Recipename + " with the following details: " + Details)
	} else {
		usermessage = openai.UserMessage("Generate a recipe for " + Recipename)
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

func openAIgenerateRecipeName(Recipe string) (string, error) {
	client := openAIclient()

	completion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You only respond with the recipe name. 2 words max."),
			openai.UserMessage("Generate a recipe name for: " + Recipe),
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		return "", err
	}

	return completion.Choices[0].Message.Content, nil
}

func openAIgenerateRecipeLink(Recipe string) (string, error) {
	client := openAIclient()

	systemmessage := openai.SystemMessage(
		" You are an agent that changes the format recipes" +
			"The recipe needs to be in markdown format: " +
			"# <Recipe Name>\n" + "## Ingredients\n" + "- **<UNIT>** Ingredient \n" + "## Instructionset 1\n" + "## Instructionset 2" +
			"All ingredients need to be in metric units.")

	var usermessage openai.ChatCompletionMessageParamUnion
	usermessage = openai.UserMessage("Change to markdown format: " + Recipe)

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