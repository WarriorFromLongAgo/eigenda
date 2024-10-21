package batcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/Layr-Labs/eigenda/common"
	"github.com/Layr-Labs/eigenda/core"
	"github.com/Layr-Labs/eigenda/disperser"
	"github.com/Layr-Labs/eigensdk-go/logging"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/gammazero/workerpool"
	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/wealdtech/go-merkletree/v2"
)

const (
	QuantizationFactor = uint(1)
	indexerWarmupDelay = 2 * time.Second
)

type BatchPlan struct {
	IncludedBlobs []*disperser.BlobMetadata
	Quorums       map[core.QuorumID]QuorumInfo
	State         *core.IndexedOperatorState
}

type QuorumInfo struct {
	Assignments        map[core.OperatorID]core.Assignment
	Info               core.AssignmentInfo
	QuantizationFactor uint
}

type TimeoutConfig struct {
	EncodingTimeout     time.Duration
	AttestationTimeout  time.Duration
	ChainReadTimeout    time.Duration
	ChainWriteTimeout   time.Duration
	ChainStateTimeout   time.Duration
	TxnBroadcastTimeout time.Duration
}

type Config struct {
	PullInterval             time.Duration
	FinalizerInterval        time.Duration
	FinalizerPoolSize        int
	EncoderSocket            string
	SRSOrder                 int
	NumConnections           int
	EncodingRequestQueueSize int
	// BatchSizeMBLimit is the maximum size of a batch in MB
	BatchSizeMBLimit     uint
	MaxNumRetriesPerBlob uint

	FinalizationBlockDelay uint

	TargetNumChunks          uint
	MaxBlobsToFetchFromStore int
}

type Batcher struct {
	Config
	TimeoutConfig

	Queue         disperser.BlobStore
	Dispatcher    disperser.Dispatcher
	EncoderClient disperser.EncoderClient

	ChainState            core.IndexedChainState
	AssignmentCoordinator core.AssignmentCoordinator
	Aggregator            core.SignatureAggregator
	EncodingStreamer      *EncodingStreamer
	Transactor            core.Transactor
	TransactionManager    TxnManager
	Metrics               *Metrics
	HeartbeatChan         chan time.Time

	ethClient common.EthClient
	finalizer Finalizer
	logger    logging.Logger
}

func NewBatcher(
	config Config,
	timeoutConfig TimeoutConfig,
	queue disperser.BlobStore,
	dispatcher disperser.Dispatcher,
	chainState core.IndexedChainState,
	assignmentCoordinator core.AssignmentCoordinator,
	encoderClient disperser.EncoderClient,
	aggregator core.SignatureAggregator,
	ethClient common.EthClient,
	finalizer Finalizer,
	transactor core.Transactor,
	txnManager TxnManager,
	logger logging.Logger,
	metrics *Metrics,
	heartbeatChan chan time.Time,
) (*Batcher, error) {
	batchTrigger := NewEncodedSizeNotifier(
		make(chan struct{}, 1),
		uint64(config.BatchSizeMBLimit)*1024*1024, // convert to bytes
	)
	streamerConfig := StreamerConfig{
		SRSOrder:                 config.SRSOrder,
		EncodingRequestTimeout:   config.PullInterval,
		EncodingQueueLimit:       config.EncodingRequestQueueSize,
		TargetNumChunks:          config.TargetNumChunks,
		MaxBlobsToFetchFromStore: config.MaxBlobsToFetchFromStore,
		FinalizationBlockDelay:   config.FinalizationBlockDelay,
		ChainStateTimeout:        timeoutConfig.ChainStateTimeout,
	}
	encodingWorkerPool := workerpool.New(config.NumConnections)
	encodingStreamer, err := NewEncodingStreamer(streamerConfig, queue, chainState, encoderClient, assignmentCoordinator, batchTrigger, encodingWorkerPool, metrics.EncodingStreamerMetrics, metrics, logger)
	if err != nil {
		return nil, err
	}

	return &Batcher{
		Config:        config,
		TimeoutConfig: timeoutConfig,

		Queue:         queue,
		Dispatcher:    dispatcher,
		EncoderClient: encoderClient,

		ChainState:            chainState,
		AssignmentCoordinator: assignmentCoordinator,
		Aggregator:            aggregator,
		EncodingStreamer:      encodingStreamer,
		Transactor:            transactor,
		TransactionManager:    txnManager,
		Metrics:               metrics,

		ethClient:     ethClient,
		finalizer:     finalizer,
		logger:        logger.With("component", "Batcher"),
		HeartbeatChan: heartbeatChan,
	}, nil
}

// RecoverState 方法的主要功能是恢复 Batcher 的状态，特别是处理那些在之前的运行中可能处于 "Dispersing" 状态的 blobs。
// 这个方法在 Batcher 启动时被调用，以确保系统能够从之前的中断中恢复。
func (b *Batcher) RecoverState(ctx context.Context) error {
	// 记录开始恢复状态的日志
	b.logger.Info("Recovering state...")
	start := time.Now()
	// 1. 获取所有处于 Dispersing 状态的 blob 元数据
	metas, err := b.Queue.GetBlobMetadataByStatus(ctx, disperser.Dispersing)
	if err != nil {
		return fmt.Errorf("failed to get blobs in dispersing state: %w", err)
	}
	// 初始化计数器
	expired := 0
	processing := 0
	// 2. 遍历所有处于 Dispersing 状态的 blob
	for _, meta := range metas {
		// 3. 检查 blob 是否已过期
		if meta.Expiry == 0 || meta.Expiry < uint64(time.Now().Unix()) {
			// 3a. 如果过期，将 blob 标记为失败
			err = b.Queue.MarkBlobFailed(ctx, meta.GetBlobKey())
			if err != nil {
				return fmt.Errorf("failed to mark blob (%s) as failed: %w", meta.GetBlobKey(), err)
			}
			expired += 1
		} else {
			// 3b. 如果未过期，将 blob 标记为正在处理
			err = b.Queue.MarkBlobProcessing(ctx, meta.GetBlobKey())
			if err != nil {
				return fmt.Errorf("failed to mark blob (%s) as processing: %w", meta.GetBlobKey(), err)
			}
			processing += 1
		}
	}
	// 4. 记录恢复状态的结果日志
	b.logger.Info("Recovering state took", "duration", time.Since(start), "numBlobs", len(metas), "expired", expired, "processing", processing)
	return nil
}

func (b *Batcher) Start(ctx context.Context) error {
	// 1. 恢复状态
	// 调用 RecoverState 方法恢复之前的状态，处理可能处于 "Dispersing" 状态的 blobs
	err := b.RecoverState(ctx)
	if err != nil {
		return fmt.Errorf("failed to recover state: %w", err)
	}

	// 2. 启动链状态
	// 调用 ChainState.Start 方法启动链状态组件，开始同步和监控区块链状态
	err = b.ChainState.Start(ctx)
	if err != nil {
		return err
	}
	// Wait for few seconds for indexer to index blockchain
	// This won't be needed when we switch to using Graph node
	// 3. 等待索引器预热
	// 暂停一段时间，让索引器有时间索引区块链
	// 注意：这在将来切换到使用 Graph 节点时可能不再需要
	time.Sleep(indexerWarmupDelay)
	// 4. 启动编码流处理器
	// 调用 EncodingStreamer.Start 方法启动编码流处理器，开始处理待编码的数据
	err = b.EncodingStreamer.Start(ctx)
	if err != nil {
		return err
	}
	batchTrigger := b.EncodingStreamer.EncodedSizeNotifier
	// 5. 启动交易处理协程
	// 启动一个 goroutine 来处理交易管理器的接收通道，处理确认的批次
	go func() {
		receiptChan := b.TransactionManager.ReceiptChan()
		for {
			select {
			case <-ctx.Done():
				return
			case receiptOrErr := <-receiptChan:
				b.logger.Info("received response from transaction manager", "receipt", receiptOrErr.Receipt, "err", receiptOrErr.Err)
				err := b.ProcessConfirmedBatch(ctx, receiptOrErr)
				if err != nil {
					b.logger.Error("failed to process confirmed batch", "err", err)
				}
			}
		}
	}()
	// 6. 启动交易管理器
	// 调用 TransactionManager.Start 方法启动交易管理器，开始处理交易
	b.TransactionManager.Start(ctx)
	// 7. 启动终结器
	// 调用 finalizer.Start 方法启动终结器，处理已确认的批次
	b.finalizer.Start(ctx)
	// 8. 启动主处理循环
	// 启动一个 goroutine，定期或在收到编码大小通知时处理单个批次
	go func() {
		ticker := time.NewTicker(b.PullInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 定期处理批次
				if err := b.HandleSingleBatch(ctx); err != nil {
					if errors.Is(err, errNoEncodedResults) {
						b.logger.Warn("no encoded results to make a batch with")
					} else {
						b.logger.Error("failed to process a batch", "err", err)
					}
				}
			case <-batchTrigger.Notify:
				// 收到编码大小通知时处理批次
				ticker.Stop()

				if err := b.HandleSingleBatch(ctx); err != nil {
					if errors.Is(err, errNoEncodedResults) {
						b.logger.Warn("no encoded results to make a batch with")
					} else {
						b.logger.Error("failed to process a batch", "err", err)
					}
				}
				ticker.Reset(b.PullInterval)
			}
		}
	}()

	return nil
}

// updateConfirmationInfo updates the confirmation info for each blob in the batch and returns failed blobs to retry.
func (b *Batcher) updateConfirmationInfo(
	ctx context.Context,
	batchData confirmationMetadata,
	txnReceipt *types.Receipt,
) ([]*disperser.BlobMetadata, error) {
	// 1. 验证输入参数
	if txnReceipt.BlockNumber == nil {
		return nil, errors.New("HandleSingleBatch: error getting transaction receipt block number")
	}
	if len(batchData.blobs) == 0 {
		return nil, errors.New("failed to process confirmed batch: no blobs from transaction manager metadata")
	}
	if batchData.batchHeader == nil {
		return nil, errors.New("failed to process confirmed batch: batch header from transaction manager metadata is nil")
	}
	if len(batchData.blobHeaders) == 0 {
		return nil, errors.New("failed to process confirmed batch: no blob headers from transaction manager metadata")
	}
	if batchData.merkleTree == nil {
		return nil, errors.New("failed to process confirmed batch: merkle tree from transaction manager metadata is nil")
	}
	if batchData.aggSig == nil {
		return nil, errors.New("failed to process confirmed batch: aggSig from transaction manager metadata is nil")
	}
	// 2. 获取批次ID
	headerHash, err := batchData.batchHeader.GetBatchHeaderHash()
	if err != nil {
		return nil, fmt.Errorf("HandleSingleBatch: error getting batch header hash: %w", err)
	}
	batchID, err := b.getBatchID(ctx, txnReceipt)
	if err != nil {
		return nil, fmt.Errorf("HandleSingleBatch: error fetching batch ID: %w", err)
	}

	// 3. 初始化需要重试的blob列表
	blobsToRetry := make([]*disperser.BlobMetadata, 0)
	var updateConfirmationInfoErr error

	// 4. 遍历批次中的每个blob
	for blobIndex, metadata := range batchData.blobs {
		// Mark the blob failed if it didn't get enough signatures.
		// 5. 确定blob的状态
		status := disperser.InsufficientSignatures

		var proof []byte
		if isBlobAttested(batchData.aggSig.QuorumResults, batchData.blobHeaders[blobIndex]) {
			status = disperser.Confirmed
			// generate inclusion proof
			// 6. 生成包含证明（如果blob已确认）
			if blobIndex >= len(batchData.blobHeaders) {
				b.logger.Error("HandleSingleBatch: error confirming blobs: blob header not found in batch", "index", blobIndex)
				blobsToRetry = append(blobsToRetry, batchData.blobs[blobIndex])
				continue
			}

			merkleProof, err := batchData.merkleTree.GenerateProofWithIndex(uint64(blobIndex), 0)
			if err != nil {
				b.logger.Error("HandleSingleBatch: failed to generate blob header inclusion proof", "err", err)
				blobsToRetry = append(blobsToRetry, batchData.blobs[blobIndex])
				continue
			}
			proof = serializeProof(merkleProof)
		}
		// 7. 创建确认信息
		confirmationInfo := &disperser.ConfirmationInfo{
			BatchHeaderHash:         headerHash,
			BlobIndex:               uint32(blobIndex),
			SignatoryRecordHash:     core.ComputeSignatoryRecordHash(uint32(batchData.batchHeader.ReferenceBlockNumber), batchData.aggSig.NonSigners),
			ReferenceBlockNumber:    uint32(batchData.batchHeader.ReferenceBlockNumber),
			BatchRoot:               batchData.batchHeader.BatchRoot[:],
			BlobInclusionProof:      proof,
			BlobCommitment:          &batchData.blobHeaders[blobIndex].BlobCommitments,
			BatchID:                 uint32(batchID),
			ConfirmationTxnHash:     txnReceipt.TxHash,
			ConfirmationBlockNumber: uint32(txnReceipt.BlockNumber.Uint64()),
			Fee:                     []byte{0}, // No fee
			QuorumResults:           batchData.aggSig.QuorumResults,
			BlobQuorumInfos:         batchData.blobHeaders[blobIndex].QuorumInfos,
		}

		// 8. 根据状态更新blob的确认信息
		if status == disperser.Confirmed {
			if _, updateConfirmationInfoErr = b.Queue.MarkBlobConfirmed(ctx, metadata, confirmationInfo); updateConfirmationInfoErr == nil {
				b.Metrics.UpdateCompletedBlob(int(metadata.RequestMetadata.BlobSize), disperser.Confirmed)
			}
		} else if status == disperser.InsufficientSignatures {
			if _, updateConfirmationInfoErr = b.Queue.MarkBlobInsufficientSignatures(ctx, metadata, confirmationInfo); updateConfirmationInfoErr == nil {
				b.Metrics.UpdateCompletedBlob(int(metadata.RequestMetadata.BlobSize), disperser.InsufficientSignatures)
			}
		} else {
			updateConfirmationInfoErr = fmt.Errorf("HandleSingleBatch: trying to update confirmation info for blob in status other than confirmed or insufficient signatures: %s", status.String())
		}
		// 9. 处理更新错误
		if updateConfirmationInfoErr != nil {
			b.logger.Error("HandleSingleBatch: error updating blob confirmed metadata", "err", updateConfirmationInfoErr)
			blobsToRetry = append(blobsToRetry, batchData.blobs[blobIndex])
		}
		requestTime := time.Unix(0, int64(metadata.RequestMetadata.RequestedAt))
		// 10. 更新指标
		b.Metrics.ObserveLatency("E2E", float64(time.Since(requestTime).Milliseconds()))
		b.Metrics.ObserveBlobAge("confirmed", float64(time.Since(requestTime).Milliseconds()))
		for _, quorumInfo := range batchData.blobHeaders[blobIndex].QuorumInfos {
			b.Metrics.IncrementBlobSize("confirmed", quorumInfo.QuorumID, int(metadata.RequestMetadata.BlobSize))
		}
	}

	return blobsToRetry, nil
}

// ProcessConfirmedBatch 主要功能是处理已确认的批次，更新相关的 blob 状态，并处理可能出现的错误。
// 这个方法通常在以下情况下被调用：
// 当一个批次的确认交易被成功执行并且收到交易回执时。
// 在 Batcher 的主循环中，当收到交易管理器的确认响应时。
func (b *Batcher) ProcessConfirmedBatch(ctx context.Context, receiptOrErr *ReceiptOrErr) error {
	// 1. 验证元数据是否存在
	if receiptOrErr.Metadata == nil {
		return errors.New("failed to process confirmed batch: no metadata from transaction manager response")
	}
	// 2. 提取确认元数据和 blob 列表
	confirmationMetadata := receiptOrErr.Metadata.(confirmationMetadata)
	blobs := confirmationMetadata.blobs
	if len(blobs) == 0 {
		return errors.New("failed to process confirmed batch: no blobs from transaction manager metadata")
	}
	// 3. 检查交易是否有错误
	if receiptOrErr.Err != nil {
		// 如果有错误，标记所有 blob 为失败状态
		_ = b.handleFailure(ctx, blobs, FailConfirmBatch)
		return fmt.Errorf("failed to confirm batch onchain: %w", receiptOrErr.Err)
	}
	// 4. 验证聚合签名是否存在
	if confirmationMetadata.aggSig == nil {
		// 如果聚合签名不存在，标记所有 blob 为失败状态
		_ = b.handleFailure(ctx, blobs, FailNoAggregatedSignature)
		return errors.New("failed to process confirmed batch: aggSig from transaction manager metadata is nil")
	}
	// 5. 记录确认交易的收据信息
	b.logger.Info("received ConfirmBatch transaction receipt", "blockNumber", receiptOrErr.Receipt.BlockNumber, "txnHash", receiptOrErr.Receipt.TxHash.Hex())

	// Mark the blobs as complete
	// 6. 更新确认信息
	stageTimer := time.Now()
	blobsToRetry, err := b.updateConfirmationInfo(ctx, confirmationMetadata, receiptOrErr.Receipt)
	if err != nil {
		// 如果更新失败，标记所有 blob 为失败状态
		_ = b.handleFailure(ctx, blobs, FailUpdateConfirmationInfo)
		return fmt.Errorf("failed to update confirmation info: %w", err)
	}
	// 7. 处理需要重试的 blob
	if len(blobsToRetry) > 0 {
		b.logger.Error("failed to update confirmation info", "failed", len(blobsToRetry), "total", len(blobs))
		_ = b.handleFailure(ctx, blobsToRetry, FailUpdateConfirmationInfo)
	}
	// 8. 记录更新确认信息的耗时
	b.logger.Debug("Update confirmation info took", "duration", time.Since(stageTimer).String())
	b.Metrics.ObserveLatency("UpdateConfirmationInfo", float64(time.Since(stageTimer).Milliseconds()))
	// 9. 计算批次大小
	batchSize := int64(0)
	for _, blobMeta := range blobs {
		batchSize += int64(blobMeta.RequestMetadata.BlobSize)
	}
	// 10. 更新批次确认指标
	b.Metrics.IncrementBatchCount(batchSize)

	return nil
}

// handleFailure 主要功能是处理批处理过程中出现的失败情况。它负责更新失败的 blob 的状态，并决定是否需要重试或将其标记为永久失败。
// 这个方法通常在以下情况下被调用：
// 当批处理过程中的某个步骤（如编码、分发、确认等）失败时。
// 当无法获取足够的签名或无法确认批次时。
// 当更新 blob 的确认信息失败时。
func (b *Batcher) handleFailure(ctx context.Context, blobMetadatas []*disperser.BlobMetadata, reason FailReason) error {
	// 初始化错误结果
	var result *multierror.Error
	// 初始化永久失败的 blob 计数
	numPermanentFailures := 0
	// 遍历所有失败的 blob
	for _, metadata := range blobMetadatas {
		// 从编码流处理器中移除已编码的 blob
		b.EncodingStreamer.RemoveEncodedBlob(metadata)
		// 处理 blob 失败，决定是否重试
		retry, err := b.Queue.HandleBlobFailure(ctx, metadata, b.MaxNumRetriesPerBlob)
		if err != nil {
			// 记录处理失败的错误日志
			b.logger.Error("HandleSingleBatch: error handling blob failure", "err", err)
			// Append the error
			// 将错误添加到结果中
			result = multierror.Append(result, err)
		}

		// 如果需要重试，继续处理下一个 blob
		if retry {
			continue
		}
		// 根据失败原因更新指标
		if reason == FailNoSignatures {
			b.Metrics.UpdateCompletedBlob(int(metadata.RequestMetadata.BlobSize), disperser.InsufficientSignatures)
		} else {
			b.Metrics.UpdateCompletedBlob(int(metadata.RequestMetadata.BlobSize), disperser.Failed)
		}
		// 增加永久失败的 blob 计数
		numPermanentFailures++
	}
	// 更新批处理错误指标
	b.Metrics.UpdateBatchError(reason, numPermanentFailures)

	// Return the error(s)
	// 返回累积的错误（如果有）
	return result.ErrorOrNil()
}

type confirmationMetadata struct {
	batchID     uuid.UUID
	batchHeader *core.BatchHeader
	blobs       []*disperser.BlobMetadata
	blobHeaders []*core.BlobHeader
	merkleTree  *merkletree.MerkleTree
	aggSig      *core.SignatureAggregation
}

func (b *Batcher) observeBlobAge(stage string, batch *batch) {
	for _, m := range batch.BlobMetadata {
		requestTime := time.Unix(0, int64(m.RequestMetadata.RequestedAt))
		b.Metrics.ObserveBlobAge(stage, float64(time.Since(requestTime).Milliseconds()))
	}
}

func (b *Batcher) observeBlobAgeAndSize(stage string, batch *batch) {
	for i, m := range batch.BlobMetadata {
		requestTime := time.Unix(0, int64(m.RequestMetadata.RequestedAt))
		b.Metrics.ObserveBlobAge(stage, float64(time.Since(requestTime).Milliseconds()))
		for _, quorumInfo := range batch.BlobHeaders[i].QuorumInfos {
			b.Metrics.IncrementBlobSize(stage, quorumInfo.QuorumID, int(m.RequestMetadata.BlobSize))
		}
	}
}

// HandleSingleBatch 方法的主要功能是处理单个批次的整个生命周期，包括创建批次、分发批次、获取签名、聚合签名和确认批次等步骤。
// 这个方法通常在以下情况下被调用：
// 定期处理：在 Batcher 的主循环中，由定时器触发（参见 batcher.go 文件的第 248-256 行）。
// 编码大小触发：当累积的编码数据达到一定大小时，由 EncodedSizeNotifier 触发（参见 batcher.go 文件的第 257-268 行）。
func (b *Batcher) HandleSingleBatch(ctx context.Context) error {
	// 1. 初始化日志记录器
	log := b.logger

	// Signal Liveness to indicate no stall
	// 2. 发送活跃信号，表示系统未停滞
	b.signalLiveness()

	// start a timer
	// 3. 开始计时，用于记录整个批处理过程的耗时
	timer := prometheus.NewTimer(prometheus.ObserverFunc(func(f float64) {
		b.Metrics.ObserveLatency("total", f*1000) // make milliseconds
	}))
	defer timer.ObserveDuration()
	// 4. 创建批次
	stageTimer := time.Now()
	batch, err := b.EncodingStreamer.CreateBatch(ctx)
	if err != nil {
		return err
	}
	log.Debug("CreateBatch took", "duration", time.Since(stageTimer))
	b.observeBlobAge("batched", batch)

	// Dispatch encoded batch
	// 5. 分发编码后的批次
	log.Debug("Dispatching enDisperseBatchcoded batch...")
	stageTimer = time.Now()
	update := b.Dispatcher.DisperseBatch(ctx, batch.State, batch.EncodedBlobs, batch.BatchHeader)
	log.Debug("DisperseBatch took", "duration", time.Since(stageTimer))
	b.observeBlobAge("attestation_requested", batch)
	// 6. 获取和记录操作员状态哈希
	h, err := batch.State.OperatorState.Hash()
	if err != nil {
		log.Error("HandleSingleBatch: error getting operator state hash", "err", err)
	}
	hStr := make([]string, 0, len(h))
	for q, hash := range h {
		hStr = append(hStr, fmt.Sprintf("%d: %x", q, hash))
	}
	log.Info("Dispatched encoded batch", "operatorStateHash", hStr)

	// Get the batch header hash
	// 7. 获取批次头哈希
	log.Debug("Getting batch header hash...")
	headerHash, err := batch.BatchHeader.GetBatchHeaderHash()
	// 9. 处理分发更新结果
	if err != nil {
		_ = b.handleFailure(ctx, batch.BlobMetadata, FailBatchHeaderHash)
		return fmt.Errorf("HandleSingleBatch: error getting batch header hash: %w", err)
	}

	// Aggregate the signatures
	// 8. 接收和验证签名
	log.Debug("Aggregating signatures...")
	stageTimer = time.Now()
	// 接收和验证来自各个操作员的签名
	// 生成一个 quorumAttestation 对象，包含签名信息和仲裁组结果
	quorumAttestation, err := b.Aggregator.ReceiveSignatures(ctx, batch.State, headerHash, update)
	if err != nil {
		_ = b.handleFailure(ctx, batch.BlobMetadata, FailAggregateSignatures)
		return fmt.Errorf("HandleSingleBatch: error receiving and validating signatures: %w", err)
	}
	// 9. 统计操作员和签名者数量
	operatorCount := make(map[core.QuorumID]int)
	signerCount := make(map[core.QuorumID]int)
	for quorumID, opState := range batch.State.Operators {
		operatorCount[quorumID] = len(opState)
		if _, ok := signerCount[quorumID]; !ok {
			signerCount[quorumID] = 0
		}
		for opID := range opState {
			if _, ok := quorumAttestation.SignerMap[opID]; ok {
				signerCount[quorumID]++
			}
		}
	}
	// 更新与签名相关的指标，如操作员数量和签名者数量
	b.Metrics.UpdateAttestation(operatorCount, signerCount, quorumAttestation.QuorumResults)
	// 10. 记录每个仲裁组的聚合结果
	for _, quorumResult := range quorumAttestation.QuorumResults {
		log.Info("Aggregated quorum result", "quorumID", quorumResult.QuorumID, "percentSigned", quorumResult.PercentSigned)
	}

	b.observeBlobAgeAndSize("attested", batch)
	// 11. 检查是否有足够的签名来确认批次，确定哪些仲裁组成功验证了数据
	numPassed, passedQuorums := numBlobsAttestedByQuorum(quorumAttestation.QuorumResults, batch.BlobHeaders)
	// TODO(mooselumph): Determine whether to confirm the batch based on the number of successes
	if numPassed == 0 {
		_ = b.handleFailure(ctx, batch.BlobMetadata, FailNoSignatures)
		return errors.New("HandleSingleBatch: no blobs received sufficient signatures")
	}

	// 12. 记录成功验证的仲裁组
	nonEmptyQuorums := []core.QuorumID{}
	for quorumID := range passedQuorums {
		log.Info("Quorums successfully attested", "quorumID", quorumID)
		nonEmptyQuorums = append(nonEmptyQuorums, quorumID)
	}

	// 13. 聚合非空仲裁组的签名，生成一个包含聚合签名和仲裁结果的 aggSig 对象
	// Aggregate the signatures across only the non-empty quorums. Excluding empty quorums reduces the gas cost.
	aggSig, err := b.Aggregator.AggregateSignatures(ctx, b.ChainState, batch.BatchHeader.ReferenceBlockNumber, quorumAttestation, nonEmptyQuorums)
	if err != nil {
		_ = b.handleFailure(ctx, batch.BlobMetadata, FailAggregateSignatures)
		return fmt.Errorf("HandleSingleBatch: error aggregating signatures: %w", err)
	}

	log.Debug("AggregateSignatures took", "duration", time.Since(stageTimer))
	b.Metrics.ObserveLatency("AggregateSignatures", float64(time.Since(stageTimer).Milliseconds()))

	// Confirm the batch
	// 14. 确认批次
	log.Debug("Confirming batch...")

	// 构建一个确认批次的交易对象
	// 使用批次头信息、仲裁结果和聚合签名作为输入。
	// 返回一个准备好的交易对象。
	txn, err := b.Transactor.BuildConfirmBatchTxn(ctx, batch.BatchHeader, aggSig.QuorumResults, aggSig)
	if err != nil {
		_ = b.handleFailure(ctx, batch.BlobMetadata, FailConfirmBatch)
		return fmt.Errorf("HandleSingleBatch: error building confirmBatch transaction: %w", err)
	}

	// 15. 处理确认批次交易
	// 处理并发送确认批次的交易。
	// 使用 NewTxnRequest 创建一个交易请求，
	// 将聚合的签名和批次信息提交到区块链上，以最终确认批次的有效性。
	err = b.TransactionManager.ProcessTransaction(ctx, NewTxnRequest(txn, "confirmBatch", big.NewInt(0), confirmationMetadata{
		batchID:     uuid.Nil,
		batchHeader: batch.BatchHeader,
		blobs:       batch.BlobMetadata,
		blobHeaders: batch.BlobHeaders,
		merkleTree:  batch.MerkleTree,
		aggSig:      aggSig,
	}))
	if err != nil {
		_ = b.handleFailure(ctx, batch.BlobMetadata, FailConfirmBatch)
		return fmt.Errorf("HandleSingleBatch: error sending confirmBatch transaction: %w", err)
	}

	return nil
}

func serializeProof(proof *merkletree.Proof) []byte {
	proofBytes := make([]byte, 0)
	for _, hash := range proof.Hashes {
		proofBytes = append(proofBytes, hash[:]...)
	}
	return proofBytes
}

// parseBatchIDFromReceipt 主要功能是从交易收据中解析出批次ID（BatchID）。这个方法通过分析交易日志，查找特定的事件（BatchConfirmed事件），并从中提取批次ID。
// 这个方法通常在以下情况下被调用：
// 当一个批次确认交易被执行后，需要获取该批次的ID时。
// 在 getBatchID 方法中，作为获取批次ID的第一步尝试。
func (b *Batcher) parseBatchIDFromReceipt(txReceipt *types.Receipt) (uint32, error) {
	// 1. 检查交易收据是否包含日志
	if len(txReceipt.Logs) == 0 {
		return 0, errors.New("failed to get transaction receipt with logs")
	}
	// 2. 遍历所有日志
	for _, log := range txReceipt.Logs {
		// 2.1 检查日志是否有主题
		if len(log.Topics) == 0 {
			b.logger.Debug("transaction receipt has no topics")
			continue
		}
		b.logger.Debug("[getBatchIDFromReceipt] ", "sigHash", log.Topics[0].Hex())
		// 2.2 检查是否为 BatchConfirmed 事件
		if log.Topics[0] == common.BatchConfirmedEventSigHash {
			// 3. 解析 ServiceManager ABI
			smAbi, err := abi.JSON(bytes.NewReader(common.ServiceManagerAbi))
			if err != nil {
				return 0, fmt.Errorf("failed to parse ServiceManager ABI: %w", err)
			}
			// 4. 获取 BatchConfirmed 事件的 ABI
			eventAbi, err := smAbi.EventByID(common.BatchConfirmedEventSigHash)
			if err != nil {
				return 0, fmt.Errorf("failed to parse BatchConfirmed event ABI: %w", err)
			}
			// 5. 解包日志数据
			unpackedData, err := eventAbi.Inputs.Unpack(log.Data)
			if err != nil {
				return 0, fmt.Errorf("failed to unpack BatchConfirmed log data: %w", err)
			}

			// There should be exactly one input in the data field, batchId.
			// Labs/eigenda/blob/master/contracts/src/interfaces/IEigenDAServiceManager.sol#L17
			// 6. 验证解包后的数据
			if len(unpackedData) != 1 {
				return 0, fmt.Errorf("BatchConfirmed log should contain exactly 1 inputs. Found %d", len(unpackedData))
			}
			// 7. 返回批次ID
			return unpackedData[0].(uint32), nil
		}
	}
	return 0, errors.New("failed to find BatchConfirmed log from the transaction")
}

func (b *Batcher) getBatchID(ctx context.Context, txReceipt *types.Receipt) (uint32, error) {
	const (
		maxRetries = 4
		baseDelay  = 1 * time.Second
	)
	var (
		batchID uint32
		err     error
	)

	batchID, err = b.parseBatchIDFromReceipt(txReceipt)
	if err == nil {
		return batchID, nil
	}

	txHash := txReceipt.TxHash
	for i := 0; i < maxRetries; i++ {
		retrySec := math.Pow(2, float64(i))
		b.logger.Warn("failed to get transaction receipt, retrying...", "retryIn", retrySec, "err", err)
		time.Sleep(time.Duration(retrySec) * baseDelay)

		txReceipt, err = b.ethClient.TransactionReceipt(ctx, txHash)
		if err != nil {
			continue
		}

		batchID, err = b.parseBatchIDFromReceipt(txReceipt)
		if err == nil {
			return batchID, nil
		}
	}

	if err != nil {
		b.logger.Warn("failed to get transaction receipt after retries", "numRetries", maxRetries, "err", err)
		return 0, err
	}

	return batchID, nil
}

// numBlobsAttestedByQuorum returns two values:
// 1. the number of blobs that have been successfully attested by all quorums
// 2. map[QuorumID]struct{} contains quorums that have been successfully attested by the quorum (has at least one blob attested in the quorum)
// numBlobsAttestedByQuorum 返回两个值：
// 1. 已由所有仲裁成功证明的 blob 数量
// 2. map[QuorumID]struct{} 包含已由仲裁成功证明的仲裁（仲裁中至少有一个 blob 已证明）
func numBlobsAttestedByQuorum(signedQuorums map[core.QuorumID]*core.QuorumResult, headers []*core.BlobHeader) (int, map[core.QuorumID]struct{}) {
	numPassed := 0
	quorums := make(map[core.QuorumID]struct{})
	for _, blob := range headers {
		thisPassed := true
		for _, quorum := range blob.QuorumInfos {
			if signedQuorums[quorum.QuorumID].PercentSigned < quorum.ConfirmationThreshold {
				thisPassed = false
			} else {
				quorums[quorum.QuorumID] = struct{}{}
			}
		}
		if thisPassed {
			numPassed++
		}
	}

	return numPassed, quorums
}

func isBlobAttested(signedQuorums map[core.QuorumID]*core.QuorumResult, header *core.BlobHeader) bool {
	for _, quorum := range header.QuorumInfos {
		if _, ok := signedQuorums[quorum.QuorumID]; !ok {
			return false
		}
		if signedQuorums[quorum.QuorumID].PercentSigned < quorum.ConfirmationThreshold {
			return false
		}
	}
	return true
}

func (b *Batcher) signalLiveness() {
	select {
	case b.HeartbeatChan <- time.Now():
		b.logger.Info("Heartbeat signal sent")
	default:
		// This case happens if there's no receiver ready to consume the heartbeat signal.
		// It prevents the goroutine from blocking if the channel is full or not being listened to.
		b.logger.Warn("Heartbeat signal skipped, no receiver on the channel")
	}
}
