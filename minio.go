package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func s3Client() (*minio.Client, error) {
	endpoint := os.Getenv("S3_ENDPOINT")
	accessKeyID := os.Getenv("S3_ACCESS")
	secretAccessKey := os.Getenv("S3_SECRET")

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
	})
	if err != nil {
		return nil, err
	}

	return minioClient, nil
}

func templateRecipesS3Object(userid int, bucketname string) error {
	ctx := context.Background()
	s3client, err := s3Client()
	if err != nil {
		log.Println("Failed to create s3 client, error")
		return err
	}

	var recipesTemplate = "# Rezepte\n\n## 🍝 Hauptgerichte"
	var recipes []Recipe
	recipes, err = GetRecipes(userid)
	if err != nil {
		log.Println("Failed to get recipes")
		return err
	}

	for _, recipe := range recipes {
		recipesTemplate = recipesTemplate + "\n - [" + recipe.Recipename + "](/?recipe=" + strings.ReplaceAll(recipe.Recipename, " ", "-") + ")"
	}

	object := strings.NewReader(recipesTemplate)
	_, err = s3client.PutObject(ctx, bucketname, "recipes.md", object, int64(object.Len()), minio.PutObjectOptions{})
	if err != nil {
		log.Println("Failed to update recipe.md")
		return err
	}

	return nil
}

func uploadRecipeS3Object(recipename string, recipe string, bucketname string) error {
	ctx := context.Background()
	s3client, err := s3Client()
	if err != nil {
		log.Println("Failed to create s3 client, error")
		return err
	}

	recipename = strings.ReplaceAll(recipename, " ", "-")
	recipePath := "recipes/" + recipename + ".md"
	recipeReader := strings.NewReader(recipe)
	_, err = s3client.PutObject(ctx, bucketname, recipePath, recipeReader, int64(recipeReader.Len()), minio.PutObjectOptions{})
	if err != nil {
		log.Println("Failed to update recipe")
		return err
	}

	log.Printf("Recipe %s uploaded to bucket %s", recipename, bucketname)
	return nil
}

func createStaticWebsite(oauthID string) error {

	ctx := context.Background()
	s3client, err := s3Client()
	if err != nil {
		log.Println("Failed to create s3 client")
		return err
	}

	err = s3client.MakeBucket(ctx, oauthID, minio.MakeBucketOptions{})
	if err != nil {
		exists, err := s3client.BucketExists(context.Background(), oauthID)
		if err == nil && exists {
			log.Printf("We already own %s\n", oauthID)
		} else {
			log.Println("Failed to create bucket")
		}
	} else {
		log.Printf("Successfully created %s\n", oauthID)
	}

	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [{
			"Action": ["s3:GetObject"],
			"Effect": "Allow",
			"Principal": {"AWS": ["*"]},
			"Resource": ["arn:aws:s3:::%s/*"],
			"Sid": ""
		}]
	}`, oauthID)

	err = s3client.SetBucketPolicy(ctx, oauthID, policy)
	if err != nil {
		log.Printf("Failed to set bucket policy, bucket %s err:  %s\n", oauthID, err)
	}

	err = bootstrapStaticWebsite(oauthID)

	return nil
}

func bootstrapStaticWebsite(bucketName string) error {
	ctx := context.Background()
	s3client, err := s3Client()
	if err != nil {
		log.Println("Failed to create s3 client")
		return err
	}

	for _, object := range []string{"", "libs"} {
		objectCh := s3client.ListObjects(ctx, "template", minio.ListObjectsOptions{
			Prefix:    object,
			Recursive: true,
		})

		for object := range objectCh {
			if object.Err != nil {
				log.Println("Failed to list objects in bucket:", object.Err)
				return object.Err
			}

			src := minio.CopySrcOptions{Bucket: "template", Object: object.Key}
			dst := minio.CopyDestOptions{Bucket: bucketName, Object: object.Key}

			if strings.HasSuffix(object.Key, "/") {
				break
			}
			_, err := s3client.CopyObject(ctx, dst, src)
			if err != nil {
				log.Println("Failed to copy object:", object.Key, "to bucket:", bucketName, "error:", err)
				return err
			}
		}
	}

	return nil
}
