// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See License.txt in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
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

func blobstorageClient(storageAccountName string) (*azblob.Client, error) {
	url := fmt.Sprintf("https://%s.blob.core.windows.net/", storageAccountName)

	credential, err := azidentity.NewDefaultAzureCredential(nil)
	handleError(err)

	client, err := azblob.NewClient(url, credential, nil)
	handleError(err)

	return client, nil
}

func addBlob(storageAccountName string, blob string, content string) error {
	client, err := blobstorageClient(storageAccountName)
	if err != nil {
		log.Printf("Failed to create blob storage client: %v", err)
	}
	ctx := context.Background()

	_, err = client.UploadBuffer(ctx, "$web", blob, []byte(content), nil)
	if err != nil {
		log.Printf("Failed to upload blob: %v", err)
	}

	return nil
}

func enableStaticWebsite(accountName string) error {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net/", accountName)
	serviceClient, err := service.NewClient(serviceURL, cred, nil)
	if err != nil {
		log.Printf("Failed to create blob storage service client: %v", err)
		return err
	}

	_, err = serviceClient.SetProperties(context.TODO(), &service.SetPropertiesOptions{
		StaticWebsite: &service.StaticWebsite{
			Enabled: to.Ptr(true),
		},
	})
	if err != nil {
		log.Printf("Failed to set static website properties: %v", err)
		return err
	}

	return nil
}
