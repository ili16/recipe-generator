// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See License.txt in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

func listAzureStorage() {
	url := "https://recipegeneratorili16.blob.core.windows.net/"

	credential, err := azidentity.NewDefaultAzureCredential(nil)
	handleError(err)

	client, err := azblob.NewClient(url, credential, nil)
	handleError(err)

	listBlobsFlat(client, "$web")
}

func handleError(err error) {
	if err != nil {
		log.Fatal(err.Error())
	}
}

func listBlobsFlat(client *azblob.Client, containerName string) {
	pager := client.NewListBlobsFlatPager(containerName, &azblob.ListBlobsFlatOptions{
		Include: azblob.ListBlobsInclude{Snapshots: true, Versions: true},
	})

	fmt.Println("List blobs flat:")
	for pager.More() {
		resp, err := pager.NextPage(context.TODO())
		handleError(err)

		for _, blob := range resp.Segment.BlobItems {
			fmt.Println(*blob.Name)
		}
	}
}

func blobstorageClient() (*azblob.Client, error) {
	url := "https://recipegeneratorili16.blob.core.windows.net/"

	credential, err := azidentity.NewDefaultAzureCredential(nil)
	handleError(err)

	client, err := azblob.NewClient(url, credential, nil)
	handleError(err)

	return client, nil
}

func addBlob(containername string, blob string, content string) error {
	client, err := blobstorageClient()
	if err != nil {
		log.Printf("Failed to create blob storage client: %v", err)
	}
	ctx := context.Background()

	_, err = client.UploadBuffer(ctx, containername, blob, []byte(content), nil)
	if err != nil {
		log.Printf("Failed to upload blob: %v", err)
	}

	return nil
}
