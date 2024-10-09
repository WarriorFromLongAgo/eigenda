package meterer_test

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Layr-Labs/eigenda/common"
	commonaws "github.com/Layr-Labs/eigenda/common/aws"
	commondynamodb "github.com/Layr-Labs/eigenda/common/aws/dynamodb"
	"github.com/Layr-Labs/eigenda/core"
	"github.com/Layr-Labs/eigenda/core/auth"
	"github.com/Layr-Labs/eigenda/core/meterer"
	"github.com/Layr-Labs/eigenda/core/mock"
	"github.com/Layr-Labs/eigenda/inabox/deploy"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ory/dockertest/v3"
	"github.com/stretchr/testify/assert"

	"github.com/Layr-Labs/eigensdk-go/logging"
)

var (
	dockertestPool     *dockertest.Pool
	dockertestResource *dockertest.Resource
	dynamoClient       *commondynamodb.Client
	clientConfig       commonaws.ClientConfig
	privateKey1        *ecdsa.PrivateKey
	privateKey2        *ecdsa.PrivateKey
	signer             *auth.EIP712Signer
	mt                 *meterer.Meterer

	deployLocalStack  bool
	localStackPort    = "4566"
	paymentChainState = &mock.MockOnchainPaymentState{}
)

func TestMain(m *testing.M) {
	setup(m)
	code := m.Run()
	teardown()
	os.Exit(code)
}

// // Mock data initialization method
// func InitializeMockPayments(pcs *meterer.OnchainPaymentState, privateKey1 *ecdsa.PrivateKey, privateKey2 *ecdsa.PrivateKey) {
// 	// Initialize mock active reservations
// 	now := uint64(time.Now().Unix())
// 	pcs.ActiveReservations.Reservations = map[string]*meterer.ActiveReservation{
// 		crypto.PubkeyToAddress(privateKey1.PublicKey).Hex(): {BytesPerSec: 100, StartTimestamp: now + 1200, EndTimestamp: now + 1800, QuorumSplit: []byte{50, 50}, QuorumNumbers: []uint8{0, 1}},
// 		crypto.PubkeyToAddress(privateKey2.PublicKey).Hex(): {BytesPerSec: 200, StartTimestamp: now - 120, EndTimestamp: now + 180, QuorumSplit: []byte{30, 70}, QuorumNumbers: []uint8{0, 1}},
// 	}
// 	pcs.OnDemandPayments.Payments = map[string]*meterer.OnDemandPayment{
// 		crypto.PubkeyToAddress(privateKey1.PublicKey).Hex(): {CumulativePayment: 1500},
// 		crypto.PubkeyToAddress(privateKey2.PublicKey).Hex(): {CumulativePayment: 1000},
// 	}
// }

func setup(_ *testing.M) {

	deployLocalStack = !(os.Getenv("DEPLOY_LOCALSTACK") == "false")
	if !deployLocalStack {
		localStackPort = os.Getenv("LOCALSTACK_PORT")
	}

	if deployLocalStack {
		var err error
		dockertestPool, dockertestResource, err = deploy.StartDockertestWithLocalstackContainer(localStackPort)
		if err != nil {
			teardown()
			panic("failed to start localstack container")
		}
	}

	loggerConfig := common.DefaultLoggerConfig()
	logger, err := common.NewLogger(loggerConfig)
	if err != nil {
		teardown()
		panic("failed to create logger")
	}

	clientConfig = commonaws.ClientConfig{
		Region:          "us-east-1",
		AccessKey:       "localstack",
		SecretAccessKey: "localstack",
		EndpointURL:     fmt.Sprintf("http://0.0.0.0:%s", localStackPort),
	}

	dynamoClient, err = commondynamodb.NewClient(clientConfig, logger)
	if err != nil {
		teardown()
		panic("failed to create dynamodb client")
	}

	chainID := big.NewInt(17000)
	verifyingContract := gethcommon.HexToAddress("0x1234000000000000000000000000000000000000")
	signer = auth.NewEIP712Signer(chainID, verifyingContract)

	privateKey1, err = crypto.GenerateKey()
	if err != nil {
		teardown()
		panic("failed to generate private key")
	}
	privateKey2, err = crypto.GenerateKey()
	if err != nil {
		teardown()
		panic("failed to generate private key")
	}

	logger = logging.NewNoopLogger()
	config := meterer.Config{
		PricePerChargeable:     1,
		MinChargeableSize:      1,
		GlobalSymbolsPerSecond: 1000,
		ReservationWindow:      1,
		ChainReadTimeout:       3 * time.Second,
	}

	err = meterer.CreateReservationTable(clientConfig, "reservations")
	if err != nil {
		teardown()
		panic("failed to create reservation table")
	}
	err = meterer.CreateOnDemandTable(clientConfig, "ondemand")
	if err != nil {
		teardown()
		panic("failed to create ondemand table")
	}
	err = meterer.CreateGlobalReservationTable(clientConfig, "global")
	if err != nil {
		teardown()
		panic("failed to create global reservation table")
	}

	store, err := meterer.NewOffchainStore(
		clientConfig,
		"reservations",
		"ondemand",
		"global",
		logger,
	)
	if err != nil {
		teardown()
		panic("failed to create offchain store")
	}

	// add some default sensible configs
	mt, err = meterer.NewMeterer(
		config,
		paymentChainState,
		store,
		logging.NewNoopLogger(),
		// metrics.NewNoopMetrics(),
	)

	// InitializeMockPayments(paymentChainState, privateKey1, privateKey2)

	if err != nil {
		teardown()
		panic("failed to create meterer")
	}
}

func teardown() {
	if deployLocalStack {
		deploy.PurgeDockertestResources(dockertestPool, dockertestResource)
	}
}

func TestMetererReservations(t *testing.T) {
	ctx := context.Background()
	meterer.CreateReservationTable(clientConfig, "reservations")
	binIndex := meterer.GetBinIndex(uint64(time.Now().Unix()), mt.ReservationWindow)
	quoromNumbers := []uint8{0, 1}

	// test invalid signature
	invalidHeader := &core.PaymentMetadata{
		AccountID:         crypto.PubkeyToAddress(privateKey1.PublicKey).Hex(),
		BinIndex:          uint32(time.Now().Unix()) / mt.Config.ReservationWindow,
		CumulativePayment: 0,
		DataLength:        2000,
		QuorumNumbers:     []uint8{0},
		Signature:         []byte{78, 212, 55, 45, 156, 217, 21, 240, 47, 141, 18, 213, 226, 196, 4, 51, 245, 110, 20, 106, 244, 142, 142, 49, 213, 21, 34, 151, 118, 254, 46, 89, 48, 84, 250, 46, 179, 228, 46, 51, 106, 164, 122, 11, 26, 101, 10, 10, 243, 2, 30, 46, 95, 125, 189, 237, 236, 91, 130, 224, 240, 151, 106, 204, 1},
	}
	err := mt.MeterRequest(ctx, *invalidHeader)
	assert.ErrorContains(t, err, "invalid signature: recovered address")

	// test invalid quorom ID
	header, err := auth.ConstructPaymentMetadata(signer, 1, 0, 1000, []uint8{0, 1, 2}, privateKey1)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "quorum number mismatch")

	// test non-existent account
	unregisteredUser, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	header, err = auth.ConstructPaymentMetadata(signer, 1, 0, 1000, []uint8{0, 1, 2}, unregisteredUser)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "failed to get active reservation by account: reservation not found")

	// test invalid bin index
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, 0, 2000, quoromNumbers, privateKey1)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "invalid bin index for reservation")

	// test bin usage metering
	accountID := crypto.PubkeyToAddress(privateKey2.PublicKey).Hex()
	for i := 0; i < 9; i++ {
		dataLength := 20
		header, err = auth.ConstructPaymentMetadata(signer, binIndex, 0, uint32(dataLength), quoromNumbers, privateKey2)
		assert.NoError(t, err)
		err = mt.MeterRequest(ctx, *header)
		assert.NoError(t, err)
		item, err := dynamoClient.GetItem(ctx, "reservations", commondynamodb.Key{
			"AccountID": &types.AttributeValueMemberS{Value: accountID},
			"BinIndex":  &types.AttributeValueMemberN{Value: strconv.Itoa(int(binIndex))},
		})
		assert.NoError(t, err)
		assert.Equal(t, accountID, item["AccountID"].(*types.AttributeValueMemberS).Value)
		assert.Equal(t, strconv.Itoa(int(binIndex)), item["BinIndex"].(*types.AttributeValueMemberN).Value)
		assert.Equal(t, strconv.Itoa(int((i+1)*dataLength)), item["BinUsage"].(*types.AttributeValueMemberN).Value)

	}
	// frist over flow is allowed
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, 0, 25, quoromNumbers, privateKey2)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.NoError(t, err)
	overflowedBinIndex := binIndex + 2
	item, err := dynamoClient.GetItem(ctx, "reservations", commondynamodb.Key{
		"AccountID": &types.AttributeValueMemberS{Value: accountID},
		"BinIndex":  &types.AttributeValueMemberN{Value: strconv.Itoa(int(overflowedBinIndex))},
	})
	assert.NoError(t, err)
	assert.Equal(t, accountID, item["AccountID"].(*types.AttributeValueMemberS).Value)
	assert.Equal(t, strconv.Itoa(int(overflowedBinIndex)), item["BinIndex"].(*types.AttributeValueMemberN).Value)
	assert.Equal(t, strconv.Itoa(int(5)), item["BinUsage"].(*types.AttributeValueMemberN).Value)

	// second over flow
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, 0, 1, quoromNumbers, privateKey2)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "bin has already been filled")

	// overwhelming bin overflow for empty bins (assuming all previous requests happened within 1 reservation window)
	header, err = auth.ConstructPaymentMetadata(signer, binIndex-1, 0, 1000, quoromNumbers, privateKey2)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "overflow usage exceeds bin limit")
}

func TestMetererOnDemand(t *testing.T) {
	ctx := context.Background()
	meterer.CreateOnDemandTable(clientConfig, "ondemand")
	meterer.CreateGlobalReservationTable(clientConfig, "global")
	quorumNumbers := []uint8{0, 1}
	binIndex := uint32(0) // this field doesn't matter for on-demand payments wrt global rate limit

	// test invalid signature
	invalidHeader := &core.PaymentMetadata{
		AccountID:         crypto.PubkeyToAddress(privateKey1.PublicKey).Hex(),
		BinIndex:          binIndex,
		CumulativePayment: 1,
		DataLength:        2000,
		QuorumNumbers:     quorumNumbers,
		Signature:         []byte{78, 212, 55, 45, 156, 217, 21, 240, 47, 141, 18, 213, 226, 196, 4, 51, 245, 110, 20, 106, 244, 142, 142, 49, 213, 21, 34, 151, 118, 254, 46, 89, 48, 84, 250, 46, 179, 228, 46, 51, 106, 164, 122, 11, 26, 101, 10, 10, 243, 2, 30, 46, 95, 125, 189, 237, 236, 91, 130, 224, 240, 151, 106, 204, 1},
	}
	err := mt.MeterRequest(ctx, *invalidHeader)
	assert.ErrorContains(t, err, "invalid signature: recovered address")

	// test unregistered account
	unregisteredUser, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	header, err := auth.ConstructPaymentMetadata(signer, 1, 1, 1000, quorumNumbers, unregisteredUser)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "failed to get on-demand payment by account: payment not found")

	// test invalid quorom ID
	header, err = auth.ConstructPaymentMetadata(signer, 1, 1, 1000, []uint8{0, 1, 2}, privateKey1)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "invalid quorum for On-Demand Request")

	// test insufficient cumulative payment
	header, err = auth.ConstructPaymentMetadata(signer, 0, 1, 2000, quorumNumbers, privateKey1)

	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "insufficient cumulative payment increment")
	// No rollback after meter request
	result, err := dynamoClient.QueryIndex(ctx, "ondemand", "AccountIDIndex", "AccountID = :account", commondynamodb.ExpressionValues{
		":account": &types.AttributeValueMemberS{
			Value: crypto.PubkeyToAddress(privateKey1.PublicKey).Hex(),
		}})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))

	// test duplicated cumulative payments
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, uint64(100), 100, quorumNumbers, privateKey2)
	err = mt.MeterRequest(ctx, *header)
	assert.NoError(t, err)
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, uint64(100), 100, quorumNumbers, privateKey2)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "exact payment already exists")

	// test valid payments
	for i := 1; i < 9; i++ {
		header, err = auth.ConstructPaymentMetadata(signer, binIndex, uint64(100*(i+1)), 100, quorumNumbers, privateKey2)
		err = mt.MeterRequest(ctx, *header)
		assert.NoError(t, err)
	}

	// test cumulative payment on-chain constraint
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, 1001, 1, quorumNumbers, privateKey2)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "invalid on-demand payment: request claims a cumulative payment greater than the on-chain deposit")

	// test insufficient increment in cumulative payment
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, 901, 2, quorumNumbers, privateKey2)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "invalid on-demand payment: insufficient cumulative payment increment")

	// test cannot insert cumulative payment in out of order
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, 50, 50, quorumNumbers, privateKey2)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "invalid on-demand payment: breaking cumulative payment invariants")

	numPrevRecords := 12
	result, err = dynamoClient.QueryIndex(ctx, "ondemand", "AccountIDIndex", "AccountID = :account", commondynamodb.ExpressionValues{
		":account": &types.AttributeValueMemberS{
			Value: crypto.PubkeyToAddress(privateKey2.PublicKey).Hex(),
		}})
	assert.NoError(t, err)
	assert.Equal(t, numPrevRecords, len(result))
	// test failed global rate limit
	header, err = auth.ConstructPaymentMetadata(signer, binIndex, 1002, 1001, quorumNumbers, privateKey1)
	assert.NoError(t, err)
	err = mt.MeterRequest(ctx, *header)
	assert.ErrorContains(t, err, "failed global rate limiting")
	// Correct rollback
	result, err = dynamoClient.QueryIndex(ctx, "ondemand", "AccountIDIndex", "AccountID = :account", commondynamodb.ExpressionValues{
		":account": &types.AttributeValueMemberS{
			Value: crypto.PubkeyToAddress(privateKey2.PublicKey).Hex(),
		}})
	assert.NoError(t, err)
	assert.Equal(t, numPrevRecords, len(result))
}

func TestMeterer_paymentCharged(t *testing.T) {
	tests := []struct {
		name               string
		dataLength         uint32
		pricePerChargeable uint32
		minChargeableSize  uint32
		expected           uint64
	}{
		{
			name:               "Data length equal to min chargeable size",
			dataLength:         1024,
			pricePerChargeable: 100,
			minChargeableSize:  1024,
			expected:           100,
		},
		{
			name:               "Data length less than min chargeable size",
			dataLength:         512,
			pricePerChargeable: 100,
			minChargeableSize:  1024,
			expected:           100,
		},
		{
			name:               "Data length greater than min chargeable size",
			dataLength:         2048,
			pricePerChargeable: 100,
			minChargeableSize:  1024,
			expected:           200,
		},
		{
			name:               "Large data length",
			dataLength:         1 << 20, // 1 MB
			pricePerChargeable: 100,
			minChargeableSize:  1024,
			expected:           102400,
		},
		{
			name:               "Price not evenly divisible by min chargeable size",
			dataLength:         1536,
			pricePerChargeable: 150,
			minChargeableSize:  1024,
			expected:           225,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &meterer.Meterer{
				Config: meterer.Config{
					PricePerChargeable: tt.pricePerChargeable,
					MinChargeableSize:  tt.minChargeableSize,
				},
			}
			result := m.PaymentCharged(tt.dataLength)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMeterer_symbolsCharged(t *testing.T) {
	tests := []struct {
		name              string
		dataLength        uint32
		minChargeableSize uint32
		expected          uint32
	}{
		{
			name:              "Data length equal to min chargeable size",
			dataLength:        1024,
			minChargeableSize: 1024,
			expected:          1024,
		},
		{
			name:              "Data length less than min chargeable size",
			dataLength:        512,
			minChargeableSize: 1024,
			expected:          1024,
		},
		{
			name:              "Data length greater than min chargeable size",
			dataLength:        2048,
			minChargeableSize: 1024,
			expected:          2048,
		},
		{
			name:              "Large data length",
			dataLength:        1 << 20, // 1 MB
			minChargeableSize: 1024,
			expected:          1 << 20,
		},
		{
			name:              "Very small data length",
			dataLength:        16,
			minChargeableSize: 1024,
			expected:          1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &meterer.Meterer{
				Config: meterer.Config{
					MinChargeableSize: tt.minChargeableSize,
				},
			}
			result := m.SymbolsCharged(tt.dataLength)
			assert.Equal(t, tt.expected, result)
		})
	}
}
