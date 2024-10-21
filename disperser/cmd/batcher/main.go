package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Layr-Labs/eigenda/common"
	coreindexer "github.com/Layr-Labs/eigenda/core/indexer"
	"github.com/Layr-Labs/eigenda/core/thegraph"

	"github.com/Layr-Labs/eigenda/common/aws/dynamodb"
	"github.com/Layr-Labs/eigenda/common/aws/s3"
	"github.com/Layr-Labs/eigenda/common/geth"
	"github.com/Layr-Labs/eigenda/core"
	coreeth "github.com/Layr-Labs/eigenda/core/eth"
	"github.com/Layr-Labs/eigenda/disperser/batcher"
	dispatcher "github.com/Layr-Labs/eigenda/disperser/batcher/grpc"
	"github.com/Layr-Labs/eigenda/disperser/cmd/batcher/flags"
	"github.com/Layr-Labs/eigenda/disperser/common/blobstore"
	"github.com/Layr-Labs/eigenda/disperser/encoder"
	"github.com/Layr-Labs/eigensdk-go/aws/kms"
	walletsdk "github.com/Layr-Labs/eigensdk-go/chainio/clients/wallet"
	"github.com/Layr-Labs/eigensdk-go/signerv2"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/urfave/cli"
)

var (
	// version is the version of the binary.
	version   string
	gitCommit string
	gitDate   string
	// Note: Changing these paths will require updating the k8s deployment
	readinessProbePath      string        = "/tmp/ready"
	healthProbePath         string        = "/tmp/health"
	maxStallDuration        time.Duration = 240 * time.Second
	handleBatchLivenessChan               = make(chan time.Time, 1)
)

func main() {
	app := cli.NewApp()
	app.Flags = flags.Flags
	app.Version = fmt.Sprintf("%s-%s-%s", version, gitCommit, gitDate)
	app.Name = "batcher"
	app.Usage = "EigenDA Batcher"
	app.Description = "Service for creating a batch from queued blobs, distributing coded chunks to nodes, and confirming onchain"

	app.Action = RunBatcher
	err := app.Run(os.Args)
	if err != nil {
		log.Fatalf("application failed: %v", err)
	}

	if _, err := os.Create(healthProbePath); err != nil {
		log.Printf("Failed to create healthProbe file: %v", err)
	}

	// Start HeartBeat Monitor
	go heartbeatMonitor(healthProbePath, maxStallDuration)

	select {}
}

// RunBatcher 服务的主要作用是处理和管理 EigenDA 系统中的数据批处理过程。具体来说，它的功能包括：
// 创建批次：从队列中收集待处理的 blobs（数据块），并将它们组合成批次。
// 编码：使用编码器客户端对批次中的数据进行编码处理。
// 分发：将编码后的数据块分发给网络中的节点。
// 链上确认：在区块链上确认批次的处理状态。
// 状态管理：管理批次和 blob 的处理状态，包括恢复之前的状态。
// 指标收集：收集和报告与批处理相关的各种指标。
// 7. 交易管理：处理与批次确认相关的区块链交易。
// 链状态同步：与区块链保持同步，获取最新的链上状态。
func RunBatcher(ctx *cli.Context) error {
	// 1. 清理环境
	// 删除就绪文件，为新的服务启动做准备
	// Clean up readiness file
	if err := os.Remove(readinessProbePath); err != nil {
		log.Printf("Failed to clean up readiness file: %v at path %v \n", err, readinessProbePath)
	}
	// 2. 加载配置
	// 从命令行上下文加载配置
	config, err := NewConfig(ctx)
	if err != nil {
		return err
	}
	// 3. 初始化日志记录器
	logger, err := common.NewLogger(config.LoggerConfig)
	if err != nil {
		return err
	}
	// 4. 设置 AWS 服务
	// 初始化 S3 客户端
	bucketName := config.BlobstoreConfig.BucketName
	s3Client, err := s3.NewClient(context.Background(), config.AwsClientConfig, logger)
	if err != nil {
		return err
	}
	logger.Info("Initialized S3 client", "bucket", bucketName)
	// 初始化 DynamoDB 客户端
	dynamoClient, err := dynamodb.NewClient(config.AwsClientConfig, logger)
	if err != nil {
		return err
	}
	// 5. 设置指标收集
	metrics := batcher.NewMetrics(config.MetricsConfig.HTTPPort, logger)
	// 6. 初始化分发器
	dispatcher := dispatcher.NewDispatcher(&dispatcher.Config{
		Timeout:                   config.TimeoutConfig.AttestationTimeout,
		EnableGnarkBundleEncoding: config.EnableGnarkBundleEncoding,
	}, logger, metrics.DispatcherMetrics)
	asgn := &core.StdAssignmentCoordinator{}
	// 7. 设置以太坊客户端和钱包
	// 根据配置初始化以太坊客户端和钱包
	var wallet walletsdk.Wallet
	var client *geth.MultiHomingClient
	if !config.KMSKeyConfig.Disable {
		if config.KMSKeyConfig.KeyID == "" || config.KMSKeyConfig.Region == "" {
			return errors.New("KMS key ID and region must be specified unless KMS wallet is disabled")
		}
		kmsClient, err := kms.NewKMSClient(context.Background(), config.KMSKeyConfig.Region)
		if err != nil {
			return fmt.Errorf("failed to create KMS client: %w", err)
		}
		pubKey, err := kms.GetECDSAPublicKey(context.Background(), kmsClient, config.KMSKeyConfig.KeyID)
		if err != nil {
			return fmt.Errorf("failed to get public key from KMS: %w", err)
		}
		addr := crypto.PubkeyToAddress(*pubKey)
		client, err = geth.NewMultiHomingClient(config.EthClientConfig, addr, logger)
		if err != nil {
			logger.Error("Cannot create chain.Client", "err", err)
			return err
		}
		chainID, err := client.ChainID(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get chain ID: %w", err)
		}
		signer := signerv2.NewKMSSigner(context.Background(), kmsClient, pubKey, config.KMSKeyConfig.KeyID, chainID)
		if err != nil {
			return err
		}
		wallet, err = walletsdk.NewPrivateKeyWallet(client, signer, addr, logger)
		if err != nil {
			return err
		}
		logger.Info("Initialized KMS wallet", "address", addr.Hex())
	} else if len(config.EthClientConfig.PrivateKeyString) > 0 {
		privateKey, err := crypto.HexToECDSA(config.EthClientConfig.PrivateKeyString)
		if err != nil {
			return fmt.Errorf("failed to parse private key: %w", err)
		}
		client, err = geth.NewMultiHomingClient(config.EthClientConfig, gethcommon.Address{}, logger)
		if err != nil {
			logger.Error("Cannot create chain.Client", "err", err)
			return err
		}
		chainID, err := client.ChainID(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get chain ID: %w", err)
		}
		signerV2, address, err := signerv2.SignerFromConfig(signerv2.Config{PrivateKey: privateKey}, chainID)
		if err != nil {
			return err
		}
		wallet, err = walletsdk.NewPrivateKeyWallet(client, signerV2, address, logger.With("component", "PrivateKeyWallet"))
		if err != nil {
			return err
		}
		logger.Info("Initialized PrivateKey wallet", "address", address.Hex())
	} else {
		return errors.New("no wallet is configured. Either Fireblocks or PrivateKey wallet should be configured")
	}

	if wallet == nil {
		return errors.New("wallet is not configured")
	}
	if client == nil {
		return errors.New("eth client is not configured")
	}

	// 8. 设置区块链交互组件
	// 初始化交易管理器、链状态等
	// used by non graph indexer
	rpcClient, err := rpc.Dial(config.EthClientConfig.RPCURLs[0])
	if err != nil {
		return err
	}
	tx, err := coreeth.NewTransactor(logger, client, config.BLSOperatorStateRetrieverAddr, config.EigenDAServiceManagerAddr)
	if err != nil {
		return err
	}
	agg, err := core.NewStdSignatureAggregator(logger, tx)
	if err != nil {
		return err
	}
	// 从合约中，获取一个称为 "BLOCK_STALE_MEASURE" 的参数。这个参数可能用于确定区块在什么时候被认为是"过时"的。在分布式系统中，这种测量通常用于处理网络延迟和临时分叉的情况。
	blockStaleMeasure, err := tx.GetBlockStaleMeasure(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get BLOCK_STALE_MEASURE: %w", err)
	}
	// 从合约中，获取一个称为 "STORE_DURATION_BLOCKS" 的参数。这个参数可能表示数据应该被存储的区块数量。在 EigenDA 系统中，这可能用于确定数据应该被保留多长时间。
	storeDurationBlocks, err := tx.GetStoreDurationBlocks(context.Background())
	if err != nil || storeDurationBlocks == 0 {
		return fmt.Errorf("failed to get STORE_DURATION_BLOCKS: %w", err)
	}

	// 9. 设置 blob 存储
	blobMetadataStore := blobstore.NewBlobMetadataStore(dynamoClient, logger, config.BlobstoreConfig.TableName, config.BlobstoreConfig.ShadowTableName, time.Duration((storeDurationBlocks+blockStaleMeasure)*12)*time.Second)
	queue := blobstore.NewSharedStorage(bucketName, s3Client, blobMetadataStore, logger)

	// 10. 设置链状态和索引器
	cs := coreeth.NewChainState(tx, client)

	// 根据配置选择使用 Graph 节点或内置索引器
	var ics core.IndexedChainState
	if config.UseGraph {
		logger.Info("Using graph node")

		logger.Info("Connecting to subgraph", "url", config.ChainStateConfig.Endpoint)
		ics = thegraph.MakeIndexedChainState(config.ChainStateConfig, cs, logger)
	} else {
		logger.Info("Using built-in indexer")

		indexer, err := coreindexer.CreateNewIndexer(
			&config.IndexerConfig,
			client,
			rpcClient,
			config.EigenDAServiceManagerAddr,
			logger,
		)
		if err != nil {
			return err
		}
		ics, err = coreindexer.NewIndexedChainState(cs, indexer)
		if err != nil {
			return err
		}
	}

	if len(config.BatcherConfig.EncoderSocket) == 0 {
		return errors.New("encoder socket must be specified")
	}
	// 11. 设置编码器客户端
	encoderClient, err := encoder.NewEncoderClient(config.BatcherConfig.EncoderSocket, config.TimeoutConfig.EncodingTimeout)
	if err != nil {
		return err
	}

	// 12. 初始化终结器和交易管理器
	finalizer := batcher.NewFinalizer(config.TimeoutConfig.ChainReadTimeout, config.BatcherConfig.FinalizerInterval, queue, client, rpcClient, config.BatcherConfig.MaxNumRetriesPerBlob, 1000, config.BatcherConfig.FinalizerPoolSize, logger, metrics.FinalizerMetrics)
	txnManager := batcher.NewTxnManager(client, wallet, config.EthClientConfig.NumConfirmations, 20, config.TimeoutConfig.TxnBroadcastTimeout, config.TimeoutConfig.ChainWriteTimeout, logger, metrics.TxnManagerMetrics)

	// Enable Metrics Block
	// 13. 启用指标收集（如果配置了）
	if config.MetricsConfig.EnableMetrics {
		httpSocket := fmt.Sprintf(":%s", config.MetricsConfig.HTTPPort)
		metrics.Start(context.Background())
		logger.Info("Enabled metrics for Batcher", "socket", httpSocket)
	}
	// 14. 创建并启动 Batcher
	batcher, err := batcher.NewBatcher(config.BatcherConfig, config.TimeoutConfig, queue, dispatcher, ics, asgn, encoderClient, agg, client, finalizer, tx, txnManager, logger, metrics, handleBatchLivenessChan)
	if err != nil {
		return err
	}
	err = batcher.Start(context.Background())
	if err != nil {
		return err
	}

	// Signal readiness
	// 15. 标记服务就绪
	// 创建就绪文件，表示服务已准备好接受请求
	if _, err := os.Create(readinessProbePath); err != nil {
		log.Printf("Failed to create readiness file: %v at path %v \n", err, readinessProbePath)
	}
	return nil
}

// process liveness signal from handleBatch Go Routine
func heartbeatMonitor(filePath string, maxStallDuration time.Duration) {
	var lastHeartbeat time.Time
	stallTimer := time.NewTimer(maxStallDuration)

	for {
		select {
		// HeartBeat from Goroutine on Batcher Pull Interval
		case heartbeat, ok := <-handleBatchLivenessChan:
			if !ok {
				log.Println("handleBatchLivenessChan closed, stopping health probe")
				return
			}
			log.Printf("Received heartbeat from HandleBatch GoRoutine: %v\n", heartbeat)
			lastHeartbeat = heartbeat
			if err := os.WriteFile(filePath, []byte(lastHeartbeat.String()), 0666); err != nil {
				log.Printf("Failed to update heartbeat file: %v", err)
			} else {
				log.Printf("Updated heartbeat file: %v with time %v\n", filePath, lastHeartbeat)
			}
			stallTimer.Reset(maxStallDuration) // Reset timer on new heartbeat

		case <-stallTimer.C:
			// Instead of stopping the function, log a warning
			log.Println("Warning: No heartbeat received within max stall duration.")
			// Reset the timer to continue monitoring
			stallTimer.Reset(maxStallDuration)
		}
	}
}
