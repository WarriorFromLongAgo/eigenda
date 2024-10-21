package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Layr-Labs/eigenda/common"
	"github.com/Layr-Labs/eigenda/disperser/apiserver"
	"github.com/Layr-Labs/eigenda/disperser/common/blobstore"
	"github.com/Layr-Labs/eigenda/encoding/fft"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Layr-Labs/eigenda/common/aws/dynamodb"
	"github.com/Layr-Labs/eigenda/common/aws/s3"
	"github.com/Layr-Labs/eigenda/common/geth"
	"github.com/Layr-Labs/eigenda/common/ratelimit"
	"github.com/Layr-Labs/eigenda/common/store"
	"github.com/Layr-Labs/eigenda/core/eth"
	"github.com/Layr-Labs/eigenda/disperser"
	"github.com/Layr-Labs/eigenda/disperser/cmd/apiserver/flags"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli"
)

var (
	// version is the version of the binary.
	version   string
	gitCommit string
	gitDate   string
)

func main() {
	app := cli.NewApp()
	app.Flags = flags.Flags
	app.Version = fmt.Sprintf("%s-%s-%s", version, gitCommit, gitDate)
	app.Name = "disperser"
	app.Usage = "EigenDA Disperser Server"
	app.Description = "Service for accepting blobs for dispersal"

	app.Action = RunDisperserServer
	err := app.Run(os.Args)
	if err != nil {
		log.Fatalf("application failed: %v", err)
	}

	select {}
}

func RunDisperserServer(ctx *cli.Context) error {
	//从命令行上下文读取配置信息,确保服务器使用正确的设置运行。
	config, err := NewConfig(ctx)
	if err != nil {
		return err
	}
	//创建日志记录器,用于记录服务器运行过程中的重要信息,便于调试和监控。
	logger, err := common.NewLogger(config.LoggerConfig)
	if err != nil {
		return err
	}

	//如果是V2版本,直接创建并启动新的分散服务器:
	if config.DisperserVersion == V2 {
		server := apiserver.NewDispersalServerV2(config.ServerConfig, logger)
		return server.Start(context.Background())
	}

	// 建立与以太坊网络的连接,用于与智能合约交互和获取区块链信息。
	client, err := geth.NewMultiHomingClient(config.EthClientConfig, gethcommon.Address{}, logger)
	if err != nil {
		logger.Error("Cannot create chain.Client", "err", err)
		return err
	}
	// 创建处理以太坊交易的组件,用于与EigenDA相关的智能合约交互。
	transactor, err := eth.NewTransactor(logger, client, config.BLSOperatorStateRetrieverAddr, config.EigenDAServiceManagerAddr)
	if err != nil {
		return err
	}
	// 从智能合约获取重要参数,用于确定数据存储时间和区块确认数。
	blockStaleMeasure, err := transactor.GetBlockStaleMeasure(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get BLOCK_STALE_MEASURE: %w", err)
	}
	storeDurationBlocks, err := transactor.GetStoreDurationBlocks(context.Background())
	if err != nil || storeDurationBlocks == 0 {
		return fmt.Errorf("failed to get STORE_DURATION_BLOCKS: %w", err)
	}
	// 创建与AWS服务的连接,S3用于存储blob数据,DynamoDB用于存储元数据。
	s3Client, err := s3.NewClient(context.Background(), config.AwsClientConfig, logger)
	if err != nil {
		return err
	}
	dynamoClient, err := dynamodb.NewClient(config.AwsClientConfig, logger)
	if err != nil {
		return err
	}

	// 设置blob数据的存储系统,包括实际数据存储和元数据管理。
	bucketName := config.BlobstoreConfig.BucketName
	logger.Info("Creating blob store", "bucket", bucketName)
	blobMetadataStore := blobstore.NewBlobMetadataStore(dynamoClient, logger, config.BlobstoreConfig.TableName, config.BlobstoreConfig.ShadowTableName, time.Duration((storeDurationBlocks+blockStaleMeasure)*12)*time.Second)
	blobStore := blobstore.NewSharedStorage(bucketName, s3Client, blobMetadataStore, logger)

	// 创建Prometheus注册表,用于收集和导出监控指标。
	reg := prometheus.NewRegistry()

	// 如果启用,创建速率限制器以控制API请求频率,防止滥用。
	var ratelimiter common.RateLimiter
	if config.EnableRatelimiter {
		globalParams := config.RatelimiterConfig.GlobalRateParams

		var bucketStore common.KVStore[common.RateBucketParams]
		if config.BucketTableName != "" {
			dynamoClient, err := dynamodb.NewClient(config.AwsClientConfig, logger)
			if err != nil {
				return err
			}
			bucketStore = store.NewDynamoParamStore[common.RateBucketParams](dynamoClient, config.BucketTableName)
		} else {
			bucketStore, err = store.NewLocalParamStore[common.RateBucketParams](config.BucketStoreSize)
			if err != nil {
				return err
			}
		}
		ratelimiter = ratelimit.NewRateLimiter(reg, globalParams, bucketStore, logger)
	}

	// 确保配置的最大blob大小在合理范围内,防止处理过大的数据。
	if config.MaxBlobSize <= 0 || config.MaxBlobSize > 32*1024*1024 {
		return fmt.Errorf("configured max blob size is invalid %v", config.MaxBlobSize)
	}

	if !fft.IsPowerOfTwo(uint64(config.MaxBlobSize)) {
		return fmt.Errorf("configured max blob size must be power of 2 %v", config.MaxBlobSize)
	}

	// 设置指标收集系统,用于监控服务器性能和使用情况。
	metrics := disperser.NewMetrics(reg, config.MetricsConfig.HTTPPort, logger)

	// 创建并启动分散服务器:
	// 创建主服务器实例,整合所有初始化的组件,并启动服务。
	server := apiserver.NewDispersalServer(
		config.ServerConfig,
		blobStore,
		transactor,
		logger,
		metrics,
		ratelimiter,
		config.RateConfig,
		config.MaxBlobSize,
	)

	// Enable Metrics Block
	// 如果配置了指标收集,启动一个HTTP服务器来暴露这些指标,便于监控系统收集数据。
	if config.MetricsConfig.EnableMetrics {
		httpSocket := fmt.Sprintf(":%s", config.MetricsConfig.HTTPPort)
		metrics.Start(context.Background())
		logger.Info("Enabled metrics for Disperser", "socket", httpSocket)
	}

	return server.Start(context.Background())
}
