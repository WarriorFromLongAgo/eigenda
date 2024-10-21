package node

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/Layr-Labs/eigenda/core"
	"github.com/Layr-Labs/eigensdk-go/logging"
	"github.com/ethereum/go-ethereum/crypto"
)

type Operator struct {
	Address             string
	Socket              string
	Timeout             time.Duration
	PrivKey             *ecdsa.PrivateKey
	KeyPair             *core.KeyPair
	OperatorId          core.OperatorID
	QuorumIDs           []core.QuorumID
	RegisterNodeAtStart bool
}

// RegisterOperator operator registers the operator with the given public key for the given quorum IDs.
func RegisterOperator(ctx context.Context, operator *Operator, transactor core.Transactor, churnerClient ChurnerClient, logger logging.Logger) error {
	// 检查提供的quorum IDs数量是否合法。
	if len(operator.QuorumIDs) > 1+core.MaxQuorumID {
		return fmt.Errorf("cannot provide more than %d quorums", 1+core.MaxQuorumID)
	}
	// 调用getQuorumIdsToRegister方法获取需要注册的quorum IDs。
	quorumsToRegister, err := operator.getQuorumIdsToRegister(ctx, transactor)
	if err != nil {
		return fmt.Errorf("failed to get quorum ids to register: %w", err)
	}
	if !operator.RegisterNodeAtStart {
		// For operator-initiated registration, the supplied quorums must be not registered yet.
		if len(quorumsToRegister) != len(operator.QuorumIDs) {
			return errors.New("quorums to register must be not registered yet")
		}
	}
	if len(quorumsToRegister) == 0 {
		return nil
	}

	logger.Info("Quorums to register for", "quorums", fmt.Sprint(quorumsToRegister))

	// register for quorums
	shouldCallChurner := false
	// check if one of the quorums to register for is full
	for _, quorumID := range quorumsToRegister {
		// 遍历每个要注册的quorum，检查是否已满。
		operatorSetParams, err := transactor.GetOperatorSetParams(ctx, quorumID)
		if err != nil {
			return err
		}

		numberOfRegisteredOperators, err := transactor.GetNumberOfRegisteredOperatorForQuorum(ctx, quorumID)
		if err != nil {
			return err
		}

		// if the quorum is full, we need to call the churner
		if operatorSetParams.MaxOperatorCount == numberOfRegisteredOperators {
			shouldCallChurner = true
			break
		}
	}

	logger.Info("Should call churner", "shouldCallChurner", shouldCallChurner)

	// Generate salt and expiry

	privateKeyBytes := []byte(operator.KeyPair.PrivKey.String())
	salt := [32]byte{}
	copy(salt[:], crypto.Keccak256([]byte("churn"), []byte(time.Now().String()), quorumsToRegister, privateKeyBytes))

	// Get the current block number
	// 使用当前时间、quorum IDs和私钥生成一个唯一的salt。 设置10分钟后的过期时间。
	expiry := big.NewInt((time.Now().Add(10 * time.Minute)).Unix())

	// if we should call the churner, call it
	// 如果需要churner：
	if shouldCallChurner {
		// 调用churner客户端的Churn方法获取churn批准。
		churnReply, err := churnerClient.Churn(ctx, operator.Address, operator.KeyPair, quorumsToRegister)
		if err != nil {
			return fmt.Errorf("failed to request churn approval: %w", err)
		}
		// 使用RegisterOperatorWithChurn方法注册操作员。
		return transactor.RegisterOperatorWithChurn(ctx, operator.KeyPair, operator.Socket, quorumsToRegister, operator.PrivKey, salt, expiry, churnReply)
	} else {
		// 直接使用RegisterOperator方法注册操作员。
		// other wise just register normally
		return transactor.RegisterOperator(ctx, operator.KeyPair, operator.Socket, quorumsToRegister, operator.PrivKey, salt, expiry)
	}
}

// DeregisterOperator deregisters the operator with the given public key from the specified quorums that it is registered with at the supplied block number.
// If the operator isn't registered with any of the specified quorums, this function will return error, and no quorum will be deregistered.
func DeregisterOperator(ctx context.Context, operator *Operator, KeyPair *core.KeyPair, transactor core.Transactor) error {
	if len(operator.QuorumIDs) > 1+core.MaxQuorumID {
		return fmt.Errorf("cannot provide more than %d quorums", 1+core.MaxQuorumID)
	}
	blockNumber, err := transactor.GetCurrentBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current block number: %w", err)
	}
	return transactor.DeregisterOperator(ctx, KeyPair.GetPubKeyG1(), blockNumber, operator.QuorumIDs)
}

// UpdateOperatorSocket updates the socket for the given operator
func UpdateOperatorSocket(ctx context.Context, transactor core.Transactor, socket string) error {
	return transactor.UpdateOperatorSocket(ctx, socket)
}

// getQuorumIdsToRegister returns the quorum ids that the operator is not registered in.
func (c *Operator) getQuorumIdsToRegister(ctx context.Context, transactor core.Transactor) ([]core.QuorumID, error) {
	if len(c.QuorumIDs) == 0 {
		return nil, fmt.Errorf("an operator should be in at least one quorum to be useful")
	}

	registeredQuorumIds, err := transactor.GetRegisteredQuorumIdsForOperator(ctx, c.OperatorId)
	if err != nil {
		return nil, fmt.Errorf("failed to get registered quorum ids for an operator: %w", err)
	}

	quorumIdsToRegister := make([]core.QuorumID, 0, len(c.QuorumIDs))
	for _, quorumID := range c.QuorumIDs {
		if !slices.Contains(registeredQuorumIds, quorumID) {
			quorumIdsToRegister = append(quorumIdsToRegister, quorumID)
		}
	}

	return quorumIdsToRegister, nil
}
