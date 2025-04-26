// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See License.txt in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/google/uuid"
)

var (
	subscriptionID    string
	location          = "westeurope"
	resourceGroupName = "recipe-generator"
)

var (
	storageClientFactory *armstorage.ClientFactory
)

var (
	accountsClient *armstorage.AccountsClient
)

func bootstrapStorageAccount(storageAccountName string, userid string) error {
	subscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
	if len(subscriptionID) == 0 {
		log.Println("AZURE_SUBSCRIPTION_ID is not set")
		return fmt.Errorf("AZURE_SUBSCRIPTION_ID is not set")
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Printf("failed to obtain a credential: %v", err)
		return err
	}
	ctx := context.Background()

	storageClientFactory, err = armstorage.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		log.Printf("failed to create storage client factory: %v", err)
		return err
	}
	accountsClient = storageClientFactory.NewAccountsClient()

	availability, err := checkNameAvailability(ctx, storageAccountName)
	if err != nil {
		log.Printf("error checking name availability: %v", err)
		return err
	}
	if !*availability.NameAvailable {
		log.Printf("storage account name not available: %s", *availability.Message)
		return fmt.Errorf("storage account name not available: %s", *availability.Message)
	}

	storageAccount, err := createStorageAccount(ctx, storageAccountName)
	if err != nil {
		log.Printf("error creating storage account: %v", err)
		return err
	}
	log.Println("storage account:", *storageAccount.ID)

	err = assignBlobDataContributorRole(storageAccountName)
	if err != nil {
		log.Printf("error assigning role: %v", err)
		return err
	}

	_, err = storageAccountProperties(ctx, storageAccountName)
	if err != nil {
		log.Printf("error getting storage account properties: %v", err)
		return err
	}

	_, err = updateStorageAccount(ctx, storageAccountName, userid)
	if err != nil {
		log.Printf("error updating storage account: %v for user: %v", err, userid)
		return err
	}

	err = enableStaticWebsite(storageAccountName)
	if err != nil {
		log.Printf("error enabling static website: %v", err)
		return err
	}

	filesToCopy := []string{
		"index.html",
		"libs/markdown-it.min.js",
		"libs/modern-normalize.min.css",
		"libs/water.light.min.css",
	}

	const (
		maxRetries = 5
		retryDelay = 5 * time.Second
	)

	for _, file := range filesToCopy {
		var err error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			err = copyDefaultBlobs(storageAccountName, file)
			if err == nil {
				break
			}

			log.Printf("Attempt %d/%d: Error copying %s: %v", attempt, maxRetries, file, err)
			if attempt < maxRetries {
				log.Printf("Waiting %v before next retry...", retryDelay)
				time.Sleep(retryDelay)
			}
		}

		if err != nil {
			log.Printf("Failed to copy %s after %d attempts: %v", file, maxRetries, err)
			return err
		}
	}

	return nil
}

func storageAccountProperties(ctx context.Context, storageAccountName string) (*armstorage.Account, error) {

	storageAccountResponse, err := accountsClient.GetProperties(
		ctx,
		resourceGroupName,
		storageAccountName,
		nil,
	)
	if err != nil {
		return nil, err
	}
	return &storageAccountResponse.Account, nil
}

func checkNameAvailability(ctx context.Context, storageAccountName string) (*armstorage.CheckNameAvailabilityResult, error) {

	result, err := accountsClient.CheckNameAvailability(
		ctx,
		armstorage.AccountCheckNameAvailabilityParameters{
			Name: to.Ptr(storageAccountName),
			Type: to.Ptr("Microsoft.Storage/storageAccounts"),
		},
		nil)
	if err != nil {
		return nil, err
	}
	return &result.CheckNameAvailabilityResult, nil
}

func createStorageAccount(ctx context.Context, storageAccountName string) (*armstorage.Account, error) {

	pollerResp, err := accountsClient.BeginCreate(
		ctx,
		resourceGroupName,
		storageAccountName,
		armstorage.AccountCreateParameters{
			Kind: to.Ptr(armstorage.KindStorageV2),
			SKU: &armstorage.SKU{
				Name: to.Ptr(armstorage.SKUNameStandardLRS),
			},
			Location: to.Ptr(location),
			Properties: &armstorage.AccountPropertiesCreateParameters{
				AccessTier: to.Ptr(armstorage.AccessTierCool),
				Encryption: &armstorage.Encryption{
					Services: &armstorage.EncryptionServices{
						File: &armstorage.EncryptionService{
							KeyType: to.Ptr(armstorage.KeyTypeAccount),
							Enabled: to.Ptr(true),
						},
						Blob: &armstorage.EncryptionService{
							KeyType: to.Ptr(armstorage.KeyTypeAccount),
							Enabled: to.Ptr(true),
						},
						Queue: &armstorage.EncryptionService{
							KeyType: to.Ptr(armstorage.KeyTypeAccount),
							Enabled: to.Ptr(true),
						},
						Table: &armstorage.EncryptionService{
							KeyType: to.Ptr(armstorage.KeyTypeAccount),
							Enabled: to.Ptr(true),
						},
					},
					KeySource: to.Ptr(armstorage.KeySourceMicrosoftStorage),
				},
			},
		}, nil)
	if err != nil {
		return nil, err
	}
	resp, err := pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &resp.Account, nil
}

func listStorageAccount(ctx context.Context) ([]*armstorage.Account, error) {

	listAccounts := accountsClient.NewListPager(nil)

	list := make([]*armstorage.Account, 0)
	for listAccounts.More() {
		pageResponse, err := listAccounts.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		list = append(list, pageResponse.AccountListResult.Value...)
	}

	return list, nil
}

func listKeysStorageAccount(ctx context.Context, storageAccountName string) ([]*armstorage.AccountKey, error) {

	listKeys, err := accountsClient.ListKeys(ctx, resourceGroupName, storageAccountName, nil)
	if err != nil {
		return nil, err
	}

	return listKeys.AccountListKeysResult.Keys, nil
}

func regenerateKeyStorageAccount(ctx context.Context, storageAccountName string) ([]*armstorage.AccountKey, error) {

	regenerateKeyResp, err := accountsClient.RegenerateKey(
		ctx,
		resourceGroupName,
		storageAccountName,
		armstorage.AccountRegenerateKeyParameters{
			KeyName: to.Ptr("key1"),
		},
		nil)

	if err != nil {
		return nil, err
	}

	return regenerateKeyResp.AccountListKeysResult.Keys, nil
}

func updateStorageAccount(ctx context.Context, storageAccountName string, userid string) (*armstorage.Account, error) {

	updateResp, err := accountsClient.Update(
		ctx,
		resourceGroupName,
		storageAccountName,
		armstorage.AccountUpdateParameters{
			Tags: map[string]*string{
				"user-id": to.Ptr(userid),
			},
		},
		nil)
	if err != nil {
		return nil, fmt.Errorf("update storage account err:%s", err)
	}

	return &updateResp.Account, nil
}

func checkStorageAccountExists(ctx context.Context, resourceGroup, accountName string) bool {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Fatal(err)
	}

	subscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
	if len(subscriptionID) == 0 {
		log.Fatal("AZURE_SUBSCRIPTION_ID is not set")
	}

	storageClientFactory, err = armstorage.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		log.Fatal(err)
	}
	accountsClient = storageClientFactory.NewAccountsClient()

	_, err = accountsClient.GetProperties(
		ctx,
		resourceGroup,
		accountName,
		nil,
	)
	if err != nil {
		if strings.Contains(err.Error(), "ResourceNotFound") {
			return false
		}
		return false
	}
	return true
}

func assignBlobDataContributorRole(storageAccountName string) error {
	ctx := context.Background()
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	principalID := os.Getenv("AZURE_OBJECT_ID")

	if subscriptionID == "" || principalID == "" {
		return fmt.Errorf("missing AZURE_SUBSCRIPTION_ID or AZURE_OBJECT_ID")
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to get credentials: %v", err)
	}

	clientFactory, err := armauthorization.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		log.Printf("failed to create client factory: %v", err)
		return err
	}

	roleAssignmentsClient := clientFactory.NewRoleAssignmentsClient()

	roleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/ba92f5b4-2d11-453d-a403-e96b0029c9fe", subscriptionID)

	scope := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
		subscriptionID,
		"recipe-generator",
		storageAccountName,
	)

	roleAssignmentID := uuid.New().String()

	_, err = roleAssignmentsClient.Create(
		ctx,
		scope,
		roleAssignmentID,
		armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				RoleDefinitionID: to.Ptr(roleDefinitionID),
				PrincipalID:      to.Ptr(principalID),
			},
		},
		nil)

	if err != nil {
		return fmt.Errorf("failed to assign role: %v", err)
	}

	return nil
}
