package indexer

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Layr-Labs/eigensdk-go/logging"
)

type Status uint

const (
	Good Status = iota
	Broken
)

const (
	maxUint       uint64 = math.MaxUint64
	maxSyncBlocks        = 10
)

type Indexer interface {
	Index(ctx context.Context) error
	HandleAccumulator(acc Accumulator, f Filterer, headers Headers) error
	GetLatestHeader(finalized bool) (*Header, error)
	GetObject(header *Header, handlerIndex int) (AccumulatorObject, error)
}

type AccumulatorHandler struct {
	Acc      Accumulator
	Filterer Filterer
	Status   Status
}

type indexer struct {
	Logger logging.Logger

	Handlers           []AccumulatorHandler
	HeaderService      HeaderService
	HeaderStore        HeaderStore
	UpgradeForkWatcher UpgradeForkWatcher

	PullInterval time.Duration
}

var _ Indexer = (*indexer)(nil)

func New(
	config *Config,
	handlers []AccumulatorHandler,
	headerSrvc HeaderService,
	headerStore HeaderStore,
	upgradeForkWatcher UpgradeForkWatcher,
	logger logging.Logger,
) *indexer {

	for _, h := range handlers {
		h.Status = Good
	}

	return &indexer{
		Handlers:           handlers,
		HeaderService:      headerSrvc,
		HeaderStore:        headerStore,
		UpgradeForkWatcher: upgradeForkWatcher,
		PullInterval:       config.PullInterval,
		Logger:             logger,
	}
}

func (i *indexer) Index(ctx context.Context) error {

	// Check if any of the accumulators are uninitialized
	// 1. 初始化检查
	// 检查所有累加器是否已初始化，它主要用于跟踪和累积区块链上的状态变化
	// 与累加器相关的还有一个过滤器（Filterer），用于筛选出相关的区块头和事件。
	initialized := true
	for _, h := range i.Handlers {
		_, _, err := i.HeaderStore.GetLatestObject(h.Acc, false)
		if err != nil {
			initialized = false
		}
	}

	// Find the latest block that we can fast forward to.
	// 2. 获取最新区块头
	// 从客户端获取最新的区块头
	clientLatestHeader, err := i.HeaderService.PullLatestHeader(true)
	if err != nil {
		i.Logger.Error("Error pulling latest header", "err", err)
		return err
	}

	syncFromBlock := maxUint
	// 3. 确定同步起始点
	// 找到可以快速前进到的最新区块
	for _, h := range i.Handlers {
		bn, err := h.Filterer.GetSyncPoint(clientLatestHeader)
		if err != nil {
			return err
		}
		if syncFromBlock > bn {
			syncFromBlock = bn
		}
	}
	// 4. 检查升级点
	// 获取最新的升级区块号
	bn := i.UpgradeForkWatcher.GetLatestUpgrade(clientLatestHeader)
	if syncFromBlock > bn {
		syncFromBlock = bn
	}
	// 5. 快速前进（如果需要）
	myLatestHeader, err := i.HeaderStore.GetLatestHeader(true)
	if err != nil || !initialized || syncFromBlock-myLatestHeader.Number > maxSyncBlocks {
		i.Logger.Info("Fast forwarding to sync block", "block", syncFromBlock)
		// This probably just wipes the HeaderStore clean
		// 清除 HeaderStore
		ffErr := i.HeaderStore.FastForward()

		if ffErr != nil && !errors.Is(ffErr, ErrNoHeaders) {
			return ffErr
		}
		// 为每个处理器设置同步点
		for _, h := range i.Handlers {
			err := h.Filterer.SetSyncPoint(clientLatestHeader)
			if err != nil {
				i.Logger.Error("Error setting sync point", "err", err)
				return err
			}
		}

	}
	if err == nil {
		i.Logger.Debug("Index", "finalized", myLatestHeader.Number)
	}
	// 6. 启动主循环
	go func() {
	loop:
		for {
			select {
			case <-ctx.Done():
				// 上下文取消时退出循环
				break loop // returning not to leak the goroutine
			default:
				// 7. 获取最新的已确认区块头
				latestFinalizedHeader, err := i.HeaderStore.GetLatestHeader(true)
				if errors.Is(err, ErrNoHeaders) {
					// TODO: Set the latestFinalized to a config value reflecting the point at which the contract was deployed
					latestFinalizedHeader = &Header{
						Number: 0,
					}
				} else if err != nil {
					i.Logger.Error("Error getting latest header", "err", err)
					time.Sleep(i.PullInterval)
					continue loop
				}
				// 8. 拉取新的区块头
				headers, isHead, err := i.HeaderService.PullNewHeaders(latestFinalizedHeader)
				if err != nil {
					i.Logger.Error("Error pulling new headers", "err", err)
					time.Sleep(i.PullInterval)
					continue loop
				}

				if len(headers) > 0 {
					// 9. 检测升级
					headers = i.UpgradeForkWatcher.DetectUpgrade(headers)
					// 10. 添加新的区块头到存储
					newHeaders, err := i.HeaderStore.AddHeaders(headers)
					if err != nil {
						i.Logger.Error("Error adding headers", "err", err)
						// TODO: Properly think through error handling
						continue loop
					}
					// 11. 处理每个累加器
					for _, h := range i.Handlers {
						if h.Status == Good {
							err := i.HandleAccumulator(h.Acc, h.Filterer, newHeaders)
							if err != nil {
								// TODO: Add Name() field to Accumulator interface so we can log which accumulator is broken
								i.Logger.Error("Error handling accumulator", "err", err)
								h.Status = Broken
							}
						}
					}
				}
				// 12. 如果到达最新区块，等待一段时间后继续
				if isHead {
					time.Sleep(i.PullInterval)
				}
			}
		}

	}()

	return nil
}

func (i *indexer) HandleAccumulator(acc Accumulator, f Filterer, headers Headers) error {

	// Handle fast mode
	initHeader, remainingHeaders, err := f.FilterFastMode(headers)
	if err != nil {
		i.Logger.Error("Error filtering fast mode", "err", err)
		return err
	}

	if initHeader != nil {
		object, err := acc.InitializeObject(*initHeader)
		if err != nil {
			i.Logger.Error("Error initializing object", "err", err)
			return err
		}
		err = i.HeaderStore.AttachObject(object, initHeader, acc)
		if err != nil {
			i.Logger.Error("Error attaching object", "err", err)
			return err
		}
	}

	if len(remainingHeaders) == 0 {
		return nil
	}

	// Get the starting accumulator object
	object, _, err := i.HeaderStore.GetLatestObject(acc, false)
	if err != nil {
		i.Logger.Error("Error getting latest object", "err", err)
		return err
	}

	// Process headers
	headersAndEvents, err := f.FilterHeaders(headers)
	if err != nil {
		return err
	}

	// Register these accumulator objects
	for _, item := range headersAndEvents {
		for _, event := range item.Events {
			i.Logger.Debug("Handling event", "event", event)
			object, err = acc.UpdateObject(object, item.Header, event)
			if err != nil {

				return err
			}
		}

		err := i.HeaderStore.AttachObject(object, item.Header, acc)
		if err != nil {
			i.Logger.Error("Error attaching object", "err", err)
			return err
		}
	}

	return nil
}

func (i *indexer) GetLatestHeader(finalized bool) (*Header, error) {
	return i.HeaderStore.GetLatestHeader(false)
}

func (i *indexer) GetObject(header *Header, handlerIndex int) (AccumulatorObject, error) {
	if len(i.Handlers) <= handlerIndex {
		return nil, errors.New("handler index out of bounds")
	}

	obj, _, err := i.HeaderStore.GetObject(header, i.Handlers[handlerIndex].Acc)
	if err != nil {
		return nil, err
	}
	return obj, nil
}
