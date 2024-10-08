package meterer

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/Layr-Labs/eigenda/core"
	"github.com/Layr-Labs/eigenda/core/auth"
	"github.com/Layr-Labs/eigensdk-go/logging"
	"github.com/ethereum/go-ethereum/common"
)

// Config contains network parameters that should be published on-chain. We currently configure these params through disperser env vars.
type Config struct {
	// GlobalSymbolsPerSecond rate limit in symbols per second for on-demand payments
	GlobalSymbolsPerSecond uint64
	// MinNumSymbols is the minimum number of symbols charged, round up for all smaller requests (must be in power of 2)
	MinNumSymbols uint32
	// PricePerSymbol is the price per symbol in gwei, used for on-demand payments
	PricePerSymbol uint32
	// ReservationWindow is the duration of all reservations in seconds, used to calculate bin indices
	ReservationWindow uint32

	// ChainReadTimeout is the timeout for reading payment state from chain
	ChainReadTimeout time.Duration
	// ChainID indicate the network in which meterer(payment) is handled
	ChainID *big.Int
	// VerifyingContract is the address of the PaymentVault contract that verifies signatures
	VerifyingContract common.Address
}

// Meterer handles payment accounting across different accounts. Disperser API server receives requests from clients and each request contains a blob header
// with payments information (CumulativePayments, BinIndex, and Signature). Disperser will pass the blob header to the meterer, which will check if the
// payments information is valid.
type Meterer struct {
	Config

	// ChainState reads on-chain payment state periodically and cache it in memory
	ChainState OnchainPayment
	// OffchainStore uses DynamoDB to track metering and used to validate requests
	OffchainStore OffchainStore

	signer auth.EIP712Signer
	logger logging.Logger
}

func NewMeterer(
	config Config,
	paymentChainState OnchainPayment,
	offchainStore OffchainStore,
	logger logging.Logger,
) (*Meterer, error) {
	// TODO: create a separate thread to pull from the chain and update chain state

	return &Meterer{
		Config: config,

		ChainState:    paymentChainState,
		OffchainStore: offchainStore,

		signer: auth.NewEIP712Signer(config.ChainID, config.VerifyingContract),
		logger: logger.With("component", "Meterer"),
	}, nil
}

// MeterRequest validates a blob header and adds it to the meterer's state
// TODO: return error if there's a rejection (with reasoning) or internal error (should be very rare)
func (m *Meterer) MeterRequest(ctx context.Context, header core.PaymentMetadata) error {
	// TODO: validate signing
	if err := m.ValidateSignature(ctx, header); err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	// Validate against the payment method
	if header.CumulativePayment == 0 {
		fmt.Println("reservation: ", header.AccountID)
		reservation, err := m.ChainState.GetActiveReservationByAccount(ctx, header.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get active reservation by account: %w", err)
		}
		if err := m.ServeReservationRequest(ctx, header, &reservation); err != nil {
			return fmt.Errorf("invalid reservation: %w", err)
		}
	} else {
		onDemandPayment, err := m.ChainState.GetOnDemandPaymentByAccount(ctx, header.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get on-demand payment by account: %w", err)
		}
		if err := m.ServeOnDemandRequest(ctx, header, &onDemandPayment); err != nil {
			return fmt.Errorf("invalid on-demand request: %w", err)
		}
	}

	return nil
}

// TODO: mocked EIP712 domain, change to the real thing when available
// ValidateSignature checks if the signature is valid against all other fields in the header
// Assuming the signature is an eip712 signature
func (m *Meterer) ValidateSignature(ctx context.Context, header core.PaymentMetadata) error {
	recoveredAddress, err := m.signer.RecoverSender(&header)
	if err != nil {
		return fmt.Errorf("failed to recover sender: %w", err)
	}

	accountAddress := common.HexToAddress(header.AccountID)

	if recoveredAddress != accountAddress {
		return fmt.Errorf("invalid signature: recovered address %s does not match account ID %s", recoveredAddress.Hex(), accountAddress.Hex())
	}

	return nil
}

// ServeReservationRequest handles the rate limiting logic for incoming requests
func (m *Meterer) ServeReservationRequest(ctx context.Context, header core.PaymentMetadata, reservation *core.ActiveReservation) error {
	if err := m.ValidateQuorum(header, reservation.QuorumNumbers); err != nil {
		return fmt.Errorf("invalid quorum for reservation: %w", err)
	}
	if !m.ValidateBinIndex(header, reservation) {
		return fmt.Errorf("invalid bin index for reservation")
	}

	// Update bin usage atomically and check against reservation's data rate as the bin limit
	if err := m.IncrementBinUsage(ctx, header, reservation); err != nil {
		return fmt.Errorf("bin overflows: %w", err)
	}

	return nil
}

// ValidateQuorums ensures that the quorums listed in the blobHeader are present within allowedQuorums
func (m *Meterer) ValidateQuorum(header core.PaymentMetadata, allowedQuorums []uint8) error {
	if len(header.QuorumNumbers) == 0 {
		return fmt.Errorf("no quorum params in blob header")
	}

	// check that all the quorum ids are in ActiveReservation's
	for _, q := range header.QuorumNumbers {
		if !slices.Contains(allowedQuorums, q) {
			// fail the entire request if there's a quorum number mismatch
			return fmt.Errorf("quorum number mismatch: %d", q)
		}
	}
	return nil
}

// ValidateBinIndex checks if the provided bin index is valid
func (m *Meterer) ValidateBinIndex(header core.PaymentMetadata, reservation *core.ActiveReservation) bool {
	now := uint64(time.Now().Unix())
	currentBinIndex := GetBinIndex(now, m.ReservationWindow)
	// Valid bin indexes are either the current bin or the previous bin
	if (header.BinIndex != currentBinIndex && header.BinIndex != (currentBinIndex-1)) || (GetBinIndex(reservation.StartTimestamp, m.ReservationWindow) > header.BinIndex || header.BinIndex > GetBinIndex(reservation.EndTimestamp, m.ReservationWindow)) {
		return false
	}
	return true
}

// IncrementBinUsage increments the bin usage atomically and checks for overflow
// TODO: Bin limit should be direct write to the Store
func (m *Meterer) IncrementBinUsage(ctx context.Context, header core.PaymentMetadata, reservation *core.ActiveReservation) error {
	numSymbols := uint64(max(header.DataLength, m.MinNumSymbols))
	newUsage, err := m.OffchainStore.UpdateReservationBin(ctx, header.AccountID, uint64(header.BinIndex), numSymbols)
	if err != nil {
		return fmt.Errorf("failed to increment bin usage: %w", err)
	}

	// metered usage stays within the bin limit
	usageLimit := m.GetReservationBinLimit(reservation)
	if newUsage <= usageLimit {
		return nil
	} else if newUsage-numSymbols >= usageLimit {
		// metered usage before updating the size already exceeded the limit
		return fmt.Errorf("bin has already been filled")
	}
	if newUsage <= 2*usageLimit && header.BinIndex+2 <= GetBinIndex(reservation.EndTimestamp, m.ReservationWindow) {
		m.OffchainStore.UpdateReservationBin(ctx, header.AccountID, uint64(header.BinIndex+2), newUsage-usageLimit)
		return nil
	}
	return fmt.Errorf("overflow usage exceeds bin limit")
}

// GetBinIndex returns the current bin index by chunking time by the bin interval;
// bin interval used by the disperser should be public information
func GetBinIndex(timestamp uint64, binInterval uint32) uint32 {
	return uint32(timestamp) / binInterval
}

// ServeOnDemandRequest handles the rate limiting logic for incoming requests
func (m *Meterer) ServeOnDemandRequest(ctx context.Context, header core.PaymentMetadata, onDemandPayment *core.OnDemandPayment) error {
	quorumNumbers, err := m.ChainState.GetOnDemandQuorumNumbers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get on-demand quorum numbers: %w", err)
	}

	if err := m.ValidateQuorum(header, quorumNumbers); err != nil {
		return fmt.Errorf("invalid quorum for On-Demand Request: %w", err)
	}
	// update blob header to use the miniumum chargeable size
	symbolsCharged := m.SymbolsCharged(header.DataLength)
	err = m.OffchainStore.AddOnDemandPayment(ctx, header, symbolsCharged)
	if err != nil {
		return fmt.Errorf("failed to update cumulative payment: %w", err)
	}
	// Validate payments attached
	err = m.ValidatePayment(ctx, header, onDemandPayment)
	if err != nil {
		// No tolerance for incorrect payment amounts; no rollbacks
		return fmt.Errorf("invalid on-demand payment: %w", err)
	}

	// Update bin usage atomically and check against bin capacity
	if err := m.IncrementGlobalBinUsage(ctx, uint64(symbolsCharged)); err != nil {
		//TODO: conditionally remove the payment based on the error type (maybe if the error is store-op related)
		m.OffchainStore.RemoveOnDemandPayment(ctx, header.AccountID, header.CumulativePayment)
		return fmt.Errorf("failed global rate limiting")
	}

	return nil
}

// ValidatePayment checks if the provided payment header is valid against the local accounting
// prevPmt is the largest  cumulative payment strictly less    than PaymentMetadata.cumulativePayment if exists
// nextPmt is the smallest cumulative payment strictly greater than PaymentMetadata.cumulativePayment if exists
// nextPmtDataLength is the dataLength of corresponding to nextPmt if exists
// prevPmt + PaymentMetadata.DataLength * m.FixedFeePerByte
// <= PaymentMetadata.CumulativePayment
// <= nextPmt - nextPmtDataLength * m.FixedFeePerByte > nextPmt
func (m *Meterer) ValidatePayment(ctx context.Context, header core.PaymentMetadata, onDemandPayment *core.OnDemandPayment) error {
	if header.CumulativePayment > uint64(onDemandPayment.CumulativePayment) {
		return fmt.Errorf("request claims a cumulative payment greater than the on-chain deposit")
	}

	prevPmt, nextPmt, nextPmtDataLength, err := m.OffchainStore.GetRelevantOnDemandRecords(ctx, header.AccountID, header.CumulativePayment) // zero if DNE
	if err != nil {
		return fmt.Errorf("failed to get relevant on-demand records: %w", err)
	}
	// the current request must increment cumulative payment by a magnitude sufficient to cover the blob size
	if prevPmt+m.PaymentCharged(header.DataLength) > header.CumulativePayment {
		return fmt.Errorf("insufficient cumulative payment increment")
	}
	// the current request must not break the payment magnitude for the next payment if the two requests were delivered out-of-order
	if nextPmt != 0 && header.CumulativePayment+m.PaymentCharged(nextPmtDataLength) > nextPmt {
		return fmt.Errorf("breaking cumulative payment invariants")
	}
	// check passed: blob can be safely inserted into the set of payments
	return nil
}

// PaymentCharged returns the chargeable price for a given data length
func (m *Meterer) PaymentCharged(dataLength uint32) uint64 {
	return uint64(m.SymbolsCharged(dataLength)) * uint64(m.PricePerSymbol)
}

// SymbolsCharged returns the number of symbols charged for a given data length
// being at least MinNumSymbols or the nearest rounded-up multiple of MinNumSymbols.
func (m *Meterer) SymbolsCharged(dataLength uint32) uint32 {
	if dataLength <= m.MinNumSymbols {
		return m.MinNumSymbols
	}
	// Round up to the nearest multiple of MinNumSymbols
	return uint32(core.RoundUpDivide(uint(dataLength), uint(m.MinNumSymbols))) * m.MinNumSymbols
}

// ValidateBinIndex checks if the provided bin index is valid
func (m *Meterer) ValidateGlobalBinIndex(header core.PaymentMetadata) (uint32, error) {
	// Deterministic function: local clock -> index (1second intervals)
	currentBinIndex := uint32(time.Now().Unix())

	// Valid bin indexes are either the current bin or the previous bin (allow this second or prev sec)
	if header.BinIndex != currentBinIndex && header.BinIndex != (currentBinIndex-1) {
		return 0, fmt.Errorf("invalid bin index for on-demand request")
	}
	return currentBinIndex, nil
}

// IncrementBinUsage increments the bin usage atomically and checks for overflow
func (m *Meterer) IncrementGlobalBinUsage(ctx context.Context, symbolsCharged uint64) error {
	globalIndex := uint64(time.Now().Unix())
	newUsage, err := m.OffchainStore.UpdateGlobalBin(ctx, globalIndex, symbolsCharged)
	if err != nil {
		return fmt.Errorf("failed to increment global bin usage: %w", err)
	}
	if newUsage > m.GlobalSymbolsPerSecond {
		return fmt.Errorf("global bin usage overflows")
	}
	return nil
}

// GetReservationBinLimit returns the bin limit for a given reservation
func (m *Meterer) GetReservationBinLimit(reservation *core.ActiveReservation) uint64 {
	return reservation.SymbolsPerSec * uint64(m.ReservationWindow)
}