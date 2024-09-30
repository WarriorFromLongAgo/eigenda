package meterer

import (
	"context"

	commonaws "github.com/Layr-Labs/eigenda/common/aws"
	test_utils "github.com/Layr-Labs/eigenda/common/aws/dynamodb/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func CreateReservationTable(clientConfig commonaws.ClientConfig, tableName string) {
	ctx := context.Background()
	// tableDescription, err := test_utils.CreateTable(ctx, clientConfig, tableName, &dynamodb.CreateTableInput{
	test_utils.CreateTable(ctx, clientConfig, tableName, &dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("AccountID"),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("BinIndex"),
				AttributeType: types.ScalarAttributeTypeN,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("AccountID"),
				KeyType:       types.KeyTypeHash,
			},
			{
				AttributeName: aws.String("BinIndex"),
				KeyType:       types.KeyTypeRange,
			},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String("AccountIDIndex"),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("AccountID"),
						KeyType:       types.KeyTypeHash,
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll, // ProjectionTypeAll means all attributes are projected into the index
				},
				ProvisionedThroughput: &types.ProvisionedThroughput{
					ReadCapacityUnits:  aws.Int64(10),
					WriteCapacityUnits: aws.Int64(10),
				},
			},
		},
		TableName: aws.String(tableName),
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(10),
			WriteCapacityUnits: aws.Int64(10),
		},
	})
	// assert.NoError(t, err)
	// assert.NotNil(t, tableDescription)
}

func CreateGlobalReservationTable(clientConfig commonaws.ClientConfig, tableName string) {
	ctx := context.Background()
	// tableDescription, err := test_utils.CreateTable(ctx, clientConfig, tableName, &dynamodb.CreateTableInput{
	test_utils.CreateTable(ctx, clientConfig, tableName, &dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("BinIndex"),
				AttributeType: types.ScalarAttributeTypeN,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("BinIndex"),
				KeyType:       types.KeyTypeHash,
			},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String("BinIndexIndex"),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("BinIndex"),
						KeyType:       types.KeyTypeHash,
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll, // ProjectionTypeAll means all attributes are projected into the index
				},
				ProvisionedThroughput: &types.ProvisionedThroughput{
					ReadCapacityUnits:  aws.Int64(10),
					WriteCapacityUnits: aws.Int64(10),
				},
			},
		},
		TableName: aws.String(tableName),
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(10),
			WriteCapacityUnits: aws.Int64(10),
		},
	})
	// assert.NoError(t, err)
	// assert.NotNil(t, tableDescription)
}

func CreateOnDemandTable(clientConfig commonaws.ClientConfig, tableName string) {
	ctx := context.Background()
	// tableDescription, err := test_utils.CreateTable(ctx, clientConfig, tableName, &dynamodb.CreateTableInput{
	test_utils.CreateTable(ctx, clientConfig, tableName, &dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("AccountID"),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("CumulativePayments"),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("AccountID"),
				KeyType:       types.KeyTypeHash,
			},
			{
				AttributeName: aws.String("CumulativePayments"),
				KeyType:       types.KeyTypeRange,
			},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String("AccountIDIndex"),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("AccountID"),
						KeyType:       types.KeyTypeHash,
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll, // ProjectionTypeAll means all attributes are projected into the index
				},
				ProvisionedThroughput: &types.ProvisionedThroughput{
					ReadCapacityUnits:  aws.Int64(10),
					WriteCapacityUnits: aws.Int64(10),
				},
			},
		},
		TableName: aws.String(tableName),
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(10),
			WriteCapacityUnits: aws.Int64(10),
		},
	})
	// assert.NoError(t, err)
	// assert.NotNil(t, tableDescription)
}
